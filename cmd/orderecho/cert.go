package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"

	"flag"
	"fmt"
	"github.com/danielgavin-code/OrderEcho/internal/report"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/evidence"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

const certUsage = `usage: orderecho cert <list|show|run> [flags]

  cert list                              suites (certs/*.yaml) and targets (certs/targets/*.yaml)
  cert show --suite FILE                 cases with section, mode and required
  cert run --suite FILE --target FILE --session ID
           [--case 4.1,4.3 | --section ORD] [--attest FILE] [--stop-on-fail]
           [--var k=v ...] [--case-timeout 60s] [--verbose]
                                         (writes data/certs/<run_id>/report.html at the end)
  cert report <run_id|results_dir> [--open]
                                         (re)write the run's report.html; --open shows it
  cert verify <run_id|results_dir>       recompute the run's SHA-256 digests

cert verify exit codes: 0 intact, 9 tampered (a digest differs, a file is missing or unrecorded),
  2 usage error or a run with no integrity record (written before 0.5.0).

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
	case "report":
		return cmdCertReport(args[1:], configPath, stdout, stderr)
	case "verify":
		return cmdCertVerify(args[1:], configPath, stdout, stderr)
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
	clk := clock.SystemClock{}
	runID := evidence.MakeRunID(clk.Now())
	var console io.Writer
	if *verbose {
		console = stdout
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	agentCh := make(chan *agent.Agent, 1)
	go func() {
		<-sigs
		fmt.Fprintln(stdout, "\n>>> interrupted: stopping the run")
		select {
		case a := <-agentCh:
			a.Init.Stop("interrupted")
		default:
		}
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

	res, dir, err := cert.Execute(cert.ExecOptions{
		Ctx: ctx, Config: cfg, Session: sc, Suite: suite, Target: target, Attest: attest, CLIVars: cliVars,
		Select: selectFn, CaseTimeout: *caseTimeout, StopOnFail: *stopOnFail, RunID: runID, Console: console,
		OnAgent:  func(a *agent.Agent) { agentCh <- a },
		Progress: func(done, total int, r *cert.CaseResult) { fmt.Fprintln(stdout, cert.ProgressLine(done, total, r)) },
	})
	if res == nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: writing results: %v\n", err)
		return cert.ExitRunnerError
	}
	fmt.Fprintln(stdout)
	cert.WriteSummary(stdout, res)
	fmt.Fprintf(stdout, "results       : %s\n", dir)
	if path, m, err := report.Write(dir); err != nil {
		fmt.Fprintf(stderr, "orderecho: writing the report: %v\n", err)
	} else {
		fmt.Fprintf(stdout, "report        : %s (%s)\n", path, m.Verdict)
	}
	return res.Exit
}

// exitTampered: cert verify found a digest that does not match.
const exitTampered = 9

// runDir resolves a run id or a results directory.
func runDir(arg, configPath string, stderr io.Writer) (string, bool) {
	if st, err := os.Stat(filepath.Join(arg, "results.json")); err == nil && !st.IsDir() {
		return arg, true
	}
	cfg, ok := loadConfig(configPath, stderr)
	if !ok {
		return "", false
	}
	dir := filepath.Join(cfg.Storage.CertsDir, filepath.Base(arg))
	if _, err := os.Stat(filepath.Join(dir, "results.json")); err != nil {
		fmt.Fprintf(stderr, "orderecho: no results.json for %q (looked in %s)\n", arg, dir)
		return "", false
	}
	return dir, true
}

func cmdCertReport(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("cert report", &configPath, stderr)
	open := fs.Bool("open", false, "open the report in the default browser")
	pos, err := parseInterleaved(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprint(stderr, certUsage)
		return exitConfig
	}
	dir, ok := runDir(pos[0], configPath, stderr)
	if !ok {
		return exitConfig
	}
	path, m, err := report.Write(dir)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	st, _ := os.Stat(path)
	fmt.Fprintf(stdout, "report : %s (%d bytes)\nverdict: %s — %s\n", path, st.Size(), m.Verdict, m.VerdictReason)
	if *open {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, path).Start(); err != nil {
			fmt.Fprintf(stderr, "orderecho: opening the report: %v\n", err)
		}
	}
	return exitOK
}

func cmdCertVerify(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("cert verify", &configPath, stderr)
	pos, err := parseInterleaved(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprint(stderr, certUsage)
		return exitConfig
	}
	dir, ok := runDir(pos[0], configPath, stderr)
	if !ok {
		return exitConfig
	}
	rep, err := cert.Verify(dir)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %s: %v\n", dir, err)
		return exitConfig
	}
	writeVerify(stdout, rep)
	if !rep.OK {
		return exitTampered
	}
	return exitOK
}

func writeVerify(w io.Writer, rep *cert.VerifyReport) {
	fmt.Fprintf(w, "verifying %s\n", rep.Dir)
	for _, f := range rep.Files {
		switch f.Status {
		case "ok":
			fmt.Fprintf(w, "  ok          %-28s %s\n", f.Path, f.Got)
		case "MISMATCH":
			fmt.Fprintf(w, "  MISMATCH    %-28s recorded %s, now %s\n", f.Path, f.Want, f.Got)
		case "MISSING":
			fmt.Fprintf(w, "  MISSING     %-28s recorded %s\n", f.Path, f.Want)
		default:
			fmt.Fprintf(w, "  %-11s %-28s %s (never recorded)\n", f.Status, f.Path, f.Got)
		}
	}
	if rep.OK {
		fmt.Fprintf(w, "intact: %d file(s) match their recorded SHA-256\n", len(rep.Files))
	} else {
		fmt.Fprintf(w, "TAMPERED: %d of %d file(s) do not match the record\n", len(rep.Problems()), len(rep.Files))
	}
}
