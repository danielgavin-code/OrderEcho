package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/fix/transport"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

// connFlags are the flags every connecting command takes.
type connFlags struct {
	session    *string
	fixVersion *string
	reset      *bool
	reconnect  *bool
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		session:    fs.String("session", "", "session id"),
		fixVersion: fs.String("fix-version", "", "override the session's fix_version (FIX.4.2 or FIX.4.4)"),
		reset:      fs.Bool("reset", false, "reset sequence numbers; the Logon carries 141=Y"),
		reconnect:  fs.Bool("reconnect", false, "reconnect after a dropped connection"),
	}
}

// parseInterleaved parses flags that may come before, between or after
// positional arguments, returning the positionals.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// resolveSession loads config and applies --session / --fix-version /
// --reconnect. ok=false means it already printed the error (exit 2).
func resolveSession(configPath string, cf connFlags, stderr io.Writer) (*config.Config, config.Session, bool) {
	cfg, ok := loadConfig(configPath, stderr)
	if !ok {
		return nil, config.Session{}, false
	}
	if *cf.session == "" {
		fmt.Fprintln(stderr, "orderecho: this command needs --session ID")
		return nil, config.Session{}, false
	}
	sc, err := cfg.Session(*cf.session)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return nil, config.Session{}, false
	}
	if sc, err = sc.WithFixVersion(*cf.fixVersion); err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return nil, config.Session{}, false
	}
	if *cf.reconnect {
		sc.Reconnect = true
	}
	return cfg, sc, true
}

// conn is a running agent plus the plumbing a command's event loop needs.
type conn struct {
	a      *agent.Agent
	sc     config.Session
	stdout io.Writer
	ctx    context.Context
	cancel context.CancelFunc
	resCh  chan transport.Result
	events chan session.Evidence
	sigs   chan os.Signal
	res    *transport.Result
	forced bool
	ints   int
	// echo is true when FIX and engine lines are echoed to the console; the
	// order commands then need not print report lines a second time.
	echo bool
}

func openConn(cfg *config.Config, sc config.Session, reset bool, stdout, stderr io.Writer) (*conn, bool) {
	var console io.Writer
	if cfg.Logging.Console {
		console = stdout
	}
	c := &conn{sc: sc, stdout: stdout, resCh: make(chan transport.Result, 1),
		events: make(chan session.Evidence, 4096), sigs: make(chan os.Signal, 2)}
	a, err := agent.New(agent.Options{Config: cfg, Session: sc, Clock: clock.SystemClock{}, Console: console, Reset: reset,
		OnEvidence: func(e session.Evidence) {
			select {
			case c.events <- e:
			default: // never block the session; the logs still have everything
			}
		}})
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return nil, false
	}
	c.a = a
	c.echo = cfg.Logging.Console
	clk := clock.SystemClock{}
	fmt.Fprintf(stdout, "OrderEcho agent %s (%s) - session %s\n", version.Version, version.Build, sc.ID)
	fmt.Fprintf(stdout, "  config         : %s\n", cfg.Path)
	fmt.Fprintf(stdout, "  route          : %s -> %s  %s  %s\n", sc.SenderCompID, sc.TargetCompID, sc.FixVersion, sc.Addr())
	resetNote := ""
	if reset {
		resetNote = " (--reset)"
	}
	fmt.Fprintf(stdout, "  heartbeat      : %ds  reset_on_logon=%v%s  reconnect=%v  heartbeat_mismatch=%s\n",
		sc.HeartbeatSec, sc.ResetOnLogon, resetNote, sc.Reconnect, sc.HeartbeatMismatch)
	fmt.Fprintf(stdout, "  seqnums        : %s (next_out=%d next_in=%d)\n", a.SeqStore.Path, a.Session.NextOut(), a.Session.NextIn())
	fmt.Fprintf(stdout, "  evidence file  : %s\n", a.Evidence.Path)
	fmt.Fprintf(stdout, "  fix log        : %s\n", a.FixLog.PathFor(clk.Now()))
	fmt.Fprintf(stdout, "  engine log     : %s/engine/orderecho_%s.log\n", strings.TrimRight(cfg.Logging.LogDir, "/"), clk.Now().UTC().Format("20060102"))
	fmt.Fprintln(stdout, "  Ctrl+C to log out; Ctrl+C again to exit at once.")
	startup := fmt.Sprintf("version=%s build=%s config=%s session=%s %s %s->%s@%s evidence=%s",
		version.Version, version.Build, cfg.Path, sc.ID, sc.FixVersion, sc.SenderCompID, sc.TargetCompID, sc.Addr(), a.Evidence.Path)
	a.EngineLog.Info("engine", "Startup: "+startup)
	a.Evidence.Event(sc.ID, "startup", startup, false)

	c.ctx, c.cancel = context.WithCancel(context.Background())
	signal.Notify(c.sigs, os.Interrupt, syscall.SIGTERM)
	go func() { c.resCh <- a.Run(c.ctx) }()
	return c, true
}

// onSignal handles Ctrl+C: the first logs out, the second exits at once.
// It returns true when the command should stop waiting for anything else.
func (c *conn) onSignal() {
	c.ints++
	if c.ints == 1 {
		fmt.Fprintln(c.stdout, "\n>>> Ctrl+C: logging out (Ctrl+C again to exit at once)")
		c.logout("OrderEcho agent shutting down")
		return
	}
	fmt.Fprintln(c.stdout, "\n>>> Ctrl+C again: exiting without waiting")
	c.forced = true
	c.a.Init.Stop("forced exit")
	c.cancel()
}

func (c *conn) logout(text string) {
	if err := c.a.Init.Logout(text); err != nil {
		c.a.Init.Stop("stopping while not connected")
	}
}

// done reports whether Run has returned (and records its result).
func (c *conn) done() bool {
	if c.res != nil {
		return true
	}
	select {
	case r := <-c.resCh:
		c.res = &r
		return true
	default:
		return false
	}
}

// waitLogon waits for the first accepted Logon; false if the session ended.
func (c *conn) waitLogon(print func(session.Evidence)) bool {
	for {
		select {
		case e := <-c.events:
			if print != nil {
				print(e)
			}
			if e.Event == "logon accepted" {
				snap := c.a.Init.Snapshot()
				fmt.Fprintf(c.stdout, ">>> Logged on to %s as %s (HeartBtInt=%ds, next_out=%d next_in=%d)\n",
					c.sc.TargetCompID, c.sc.SenderCompID, snap.HeartBtInt, snap.NextOut, snap.NextIn)
				return true
			}
		case r := <-c.resCh:
			c.res = &r
			return false
		case <-c.sigs:
			c.onSignal()
		}
	}
}

// finish logs out (if still connected), waits for Run, prints the verdict
// line and returns the session exit code.
func (c *conn) finish(text string, stderr io.Writer, print func(session.Evidence)) int {
	if c.res == nil {
		c.logout(text)
	}
	for c.res == nil {
		select {
		case e := <-c.events:
			if print != nil {
				print(e)
			}
		case r := <-c.resCh:
			c.res = &r
		case <-c.sigs:
			c.onSignal()
		}
	}
	// Drain what is left so the last lines are printed.
	for {
		select {
		case e := <-c.events:
			if print != nil {
				print(e)
			}
			continue
		default:
		}
		break
	}
	c.a.EngineLog.Info("engine", "Shutdown complete")
	signal.Stop(c.sigs)
	c.a.Close()
	if c.forced {
		return agent.ExitForced
	}
	code := agent.SessionExitCode(*c.res)
	line := agent.Explain(*c.res, c.sc)
	fmt.Fprintf(c.stdout, ">>> %s\n", line)
	if code != agent.ExitOK {
		fmt.Fprintf(stderr, "orderecho: %s\n", line)
	}
	return code
}

// ------------------------------------------------------------- connect

func cmdConnect(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("connect", &configPath, stderr)
	cf := addConnFlags(fs)
	duration := fs.Duration("duration", 0, "log out cleanly after this long once logged on (0 = until Ctrl+C)")
	testRequest := fs.Bool("test-request", false, "send one TestRequest after logon")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "orderecho: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return exitConfig
	}
	cfg, sc, ok := resolveSession(configPath, cf, stderr)
	if !ok {
		return exitConfig
	}
	c, ok := openConn(cfg, sc, *cf.reset, stdout, stderr)
	if !ok {
		return exitFailed
	}
	if !c.waitLogon(nil) {
		return c.finish("", stderr, nil)
	}
	testReqSent := map[string]time.Time{}
	if *testRequest {
		if id, err := c.a.Init.TestRequest(); err != nil {
			fmt.Fprintf(stdout, ">>> TestRequest not sent: %v\n", err)
		} else {
			testReqSent[id] = time.Now()
			fmt.Fprintf(stdout, ">>> TestRequest %s sent\n", id)
		}
	}
	var durationC <-chan time.Time
	if *duration > 0 {
		durationC = time.After(*duration)
	}
	for c.res == nil {
		select {
		case e := <-c.events:
			switch e.Event {
			case "logon accepted":
				fmt.Fprintf(stdout, ">>> Logged on again (reconnect)\n")
			case "testrequest answered":
				id := strings.TrimPrefix(e.Detail, "112=")
				if at, ok := testReqSent[id]; ok {
					fmt.Fprintf(stdout, ">>> TestRequest %s answered in %s\n", id, time.Since(at).Round(time.Millisecond))
				}
			}
		case <-durationC:
			durationC = nil
			fmt.Fprintf(stdout, ">>> Duration %s elapsed; logging out\n", *duration)
			c.logout("OrderEcho agent: duration elapsed")
		case <-c.sigs:
			c.onSignal()
		case r := <-c.resCh:
			c.res = &r
		}
	}
	return c.finish("", stderr, nil)
}
