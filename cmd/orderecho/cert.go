package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/evidence"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

const certUsage = `usage: orderecho cert <list|show|run> [flags]

  cert list                              suites (certs/*.yaml) and targets (certs/targets/*.yaml)
  cert show --suite FILE                 cases with section, mode and required
  cert run --suite FILE --target FILE --session ID
           [--case 4.1,4.3 | --section ORD] [--attest FILE] [--stop-on-fail]
           [--var k=v ...] [--case-timeout 60s] [--verbose]

cert run exit codes:
  0  every required case PASS or N/A
  5  a required case FAILed
  7  no required FAIL, but required cases are BLOCKED or PENDING
  8  a runner ERROR (infrastructure, not the counterparty's fault)
  1/3/4  the session itself failed (logon failed / dropped and not recovered / logout timeout)
  2  config, suite, target or usage error
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func cmdCert(args []string, configPath string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, certUsage)
		return exitConfig
	}
	switch args[0] {
	case "list":
		suites, targets := cert.Discover(".")
		fmt.Fprintln(stdout, "suites:")
		for _, p := range suites {
			s, err := cert.LoadSuite(p)
			if err != nil {
				fmt.Fprintf(stdout, "  %-34s INVALID: %v\n", p, err)
				continue
			}
			fmt.Fprintf(stdout, "  %-34s %-20s %s  %d cases  %s\n", p, s.Name, s.FixVersion, len(s.Cases), s.Title)
		}
		fmt.Fprintln(stdout, "targets:")
		for _, p := range targets {
			t, err := cert.LoadTarget(p)
			if err != nil {
				fmt.Fprintf(stdout, "  %-34s INVALID: %v\n", p, err)
				continue
			}
			api := "no control API"
			if t.ControlAPI != "" {
				api = "control API " + t.ControlAPI
			}
			fmt.Fprintf(stdout, "  %-34s %-20s %s, %d N/A override(s)\n", p, t.Name, api, len(t.NotApplicable))
		}
		return exitOK
	case "show":
		fs := flag.NewFlagSet("orderecho cert show", flag.ContinueOnError)
		fs.SetOutput(stderr)
		suitePath := fs.String("suite", "", "suite file")
		if err := fs.Parse(args[1:]); err != nil || *suitePath == "" {
			fmt.Fprint(stderr, certUsage)
			return exitConfig
		}
		s, err := cert.LoadSuite(*suitePath)
		if err != nil {
			fmt.Fprintf(stderr, "orderecho: %v\n", err)
			return exitConfig
		}
		fmt.Fprintf(stdout, "%s — %s (%s), source %s\n\n", s.Name, s.Title, s.FixVersion, s.Source)
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSECTION\tMODE\tREQ\tLEVEL\tSTEPS\tTITLE")
		counts := map[string]int{}
		for _, c := range s.Cases {
			req := "opt"
			if c.Required {
				req = "req"
			}
			counts[c.Mode]++
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", c.ID, c.Section, c.Mode, req, c.Level, len(c.Steps), c.Title)
		}
		tw.Flush()
		fmt.Fprintf(stdout, "\n%d cases: %d auto, %d assisted, %d manual\n", len(s.Cases), counts[cert.ModeAuto], counts[cert.ModeAssisted], counts[cert.ModeManual])
		return exitOK
	case "run":
		return cmdCertRun(args[1:], configPath, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, certUsage)
		return exitOK
	}
	fmt.Fprintf(stderr, "orderecho: unknown cert command %q\n", args[0])
	fmt.Fprint(stderr, certUsage)
	return exitConfig
}

func cmdCertRun(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("cert run", &configPath, stderr)
	suitePath := fs.String("suite", "", "suite file")
	targetPath := fs.String("target", "", "target file")
	sessionID := fs.String("session", "", "session id")
	caseList := fs.String("case", "", "comma-separated case ids")
	section := fs.String("section", "", "run one section")
	attestPath := fs.String("attest", "", "attestation file")
	stopOnFail := fs.Bool("stop-on-fail", false, "stop after the first FAIL or ERROR")
	caseTimeout := fs.Duration("case-timeout", 60*time.Second, "overall limit per case")
	verbose := fs.Bool("verbose", false, "echo FIX and engine lines to the console")
	var vars multiFlag
	fs.Var(&vars, "var", "k=v variable override (repeatable)")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if *suitePath == "" || *targetPath == "" || *sessionID == "" {
		fmt.Fprintln(stderr, "orderecho: cert run needs --suite, --target and --session")
		return exitConfig
	}
	suite, err := cert.LoadSuite(*suitePath)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitConfig
	}
	target, err := cert.LoadTarget(*targetPath)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitConfig
	}
	var attest map[string]cert.Attestation
	if *attestPath != "" {
		if attest, err = cert.LoadAttestations(*attestPath); err != nil {
			fmt.Fprintf(stderr, "orderecho: %v\n", err)
			return exitConfig
		}
		for id := range attest {
			if suite.Case(id) == nil {
				fmt.Fprintf(stderr, "orderecho: %s: attestation for unknown case %q\n", *attestPath, id)
				return exitConfig
			}
		}
	}
	cliVars := map[string]string{}
	for _, kv := range vars {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			fmt.Fprintf(stderr, "orderecho: --var wants k=v, got %q\n", kv)
			return exitConfig
		}
		cliVars[k] = v
	}
	var selectFn func(*cert.Case) bool
	if *caseList != "" || *section != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(*caseList, ",") {
			if id = strings.TrimSpace(id); id != "" {
				if suite.Case(id) == nil {
					fmt.Fprintf(stderr, "orderecho: --case: no case %q in %s\n", id, *suitePath)
					return exitConfig
				}
				want[id] = true
			}
		}
		selectFn = func(c *cert.Case) bool { return want[c.ID] || (*section != "" && c.Section == *section) }
	}
	cfg, ok := loadConfig(configPath, stderr)
	if !ok {
		return exitConfig
	}
	sc, err := cfg.Session(*sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitConfig
	}
	if sc.FixVersion != suite.FixVersion {
		fmt.Fprintf(stderr, "orderecho: suite %s is %s but session %s is %s (use the matching suite, e.g. order_entry_fix44.yaml)\n",
			suite.Name, suite.FixVersion, sc.ID, sc.FixVersion)
		return exitConfig
	}
	sc.Reconnect = false // the runner decides when to reconnect

	clk := clock.SystemClock{}
	runID := evidence.MakeRunID(clk.Now())
	hist := cert.NewHistory(clk)
	var console io.Writer
	if *verbose {
		console = stdout
	}
	a, err := agent.New(agent.Options{Config: cfg, Session: sc, Clock: clk, Console: console, RunID: runID,
		OnWire: func(dir string, m *codec.Message) { hist.Add(dir, m) }})
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		fmt.Fprintln(stdout, "\n>>> interrupted: stopping the run")
		a.Init.Stop("interrupted")
		cancel()
	}()

	fmt.Fprintf(stdout, "OrderEcho %s (%s) cert run %s\n", version.Version, version.Build, runID)
	fmt.Fprintf(stdout, "  suite   : %s (%s, %d cases) %s\n", suite.Name, suite.FixVersion, len(suite.Cases), *suitePath)
	fmt.Fprintf(stdout, "  target  : %s %s\n", target.Name, *targetPath)
	fmt.Fprintf(stdout, "  session : %s %s -> %s @ %s\n", sc.ID, sc.SenderCompID, sc.TargetCompID, sc.Addr())
	if *attestPath != "" {
		fmt.Fprintf(stdout, "  attest  : %s (%d case(s))\n", *attestPath, len(attest))
	}
	fmt.Fprintln(stdout)

	drv := cert.NewLive(ctx, a, hist)
	res := cert.Run(cert.Options{
		Suite: suite, Target: target, Attest: attest, Driver: drv, Clock: clk, CLIVars: cliVars,
		CaseTimeout: *caseTimeout, StopOnFail: *stopOnFail, Select: selectFn, RunID: runID,
		Version: version.Version, Build: version.Build, ConnectAtStart: true, ResetAtStart: true,
		Progress: func(done, total int, r *cert.CaseResult) {
			line := fmt.Sprintf("[%2d/%d] %-5s %-8s %-8s %s", done, total, r.ID, r.Mode, r.Status, r.Title)
			if r.Status != cert.StatusPass && r.Reason != "" {
				reason := r.Reason
				if len(reason) > 160 {
					reason = reason[:159] + "…"
				}
				line += " — " + reason
			} else if len(r.Warnings) > 0 {
				line += " (warning)"
			}
			fmt.Fprintln(stdout, line)
		},
	})
	sessionCode := agent.ExitOK
	if r, err := drv.Close(); err != nil {
		fmt.Fprintf(stderr, "orderecho: closing the session: %v\n", err)
	} else if r != nil && !res.NeverLoggedOn && r.Outcome.DisconnectCause == "Logout timeout" {
		sessionCode = agent.ExitLogoutTimeout
	}
	res.Exit = cert.ExitCode(res, sessionCode)
	dir := filepath.Join(cfg.Storage.CertsDir, runID)
	if err := cert.WriteResults(dir, res); err != nil {
		fmt.Fprintf(stderr, "orderecho: writing results: %v\n", err)
		return cert.ExitRunnerError
	}
	fmt.Fprintln(stdout)
	cert.WriteSummary(stdout, res)
	fmt.Fprintf(stdout, "results       : %s\n", dir)
	return res.Exit
}
