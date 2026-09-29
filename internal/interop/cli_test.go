//go:build interop

package interop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func runCLIInput(t *testing.T, dir, input string, timeout time.Duration, args ...string) cliRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, agentBin, append([]string{"--config", "orderecho.yaml"}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !asExitError(err, &ee) {
			t.Fatalf("running CLI: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	t.Logf("$ orderecho %s  -> exit %d\n%s", strings.Join(args, " "), code, out)
	return cliRun{code: code, out: string(out)}
}

// The CLI order command: exit 0 on PASS, 6 when the order stays working,
// 5 when the counterparty's reports FAIL the checks, 1 on --fix-version
// against the strict 4.2 broker.
func TestCLIOrderExitCodes(t *testing.T) {
	e := startEmulator(t)
	dir := cliDir(t, e)

	run := runCLI(t, dir, 60*time.Second, "order", "--session", "emu44", "AAPL", "100", "buy", "mkt")
	if run.code != 0 {
		t.Fatalf("exit %d", run.code)
	}
	for _, want := range []string{">> D 11=OE-", "150=F(Trade) 39=2(Filled) last=100@227.50", "checks: PASS", "verdict: PASS", "Logged out cleanly"} {
		if !strings.Contains(run.out, want) {
			t.Fatalf("output lacks %q", want)
		}
	}

	run = runCLI(t, dir, 60*time.Second, "order", "--session", "emu42", "EFG", "1000", "buy", "lmt", "10.00", "--wait", "2500ms")
	if run.code != 6 || !strings.Contains(run.out, "still PARTIALLY_FILLED") {
		t.Fatalf("partials: exit %d, want 6", run.code)
	}

	// A held order; while the CLI waits, the emulator's next ER is made to
	// claim CumQty 999 and the rest is filled: 39=2 with CumQty != OrderQty.
	done := make(chan cliRun, 1)
	go func() {
		done <- runCLI(t, dir, 60*time.Second, "order", "--session", "emu42", "ZWZZT", "1000", "buy", "lmt", "10.00", "--wait", "20s")
	}()
	var oid string
	waitFor(t, 15*time.Second, "held order open on the emulator", func() bool {
		var body struct {
			Orders []struct {
				OrderID string `json:"order_id"`
				Symbol  string `json:"symbol"`
			} `json:"orders"`
		}
		e.getJSON("/sessions/agent42/orders?status=open", &body)
		for _, o := range body.Orders {
			if o.Symbol == "ZWZZT" {
				oid = o.OrderID
			}
		}
		return oid != ""
	})
	e.post("/sessions/agent42/inject/next", map[string]any{"msg_type": "8", "set": map[string]string{"14": "999"}})
	e.post("/orders/"+oid+"/fill-rest", map[string]any{})
	run = <-done
	if run.code != 5 || !strings.Contains(run.out, "FAIL terminal_quantities") || !strings.Contains(run.out, "verdict: FAIL") {
		t.Fatalf("tampered report: exit %d, want 5", run.code)
	}

	run = runCLI(t, dir, 30*time.Second, "order", "--session", "strict", "--fix-version", "FIX.4.4", "AAPL", "100", "buy", "mkt")
	if run.code != 1 || !strings.Contains(run.out, "Incorrect BeginString, expected FIX.4.2") {
		t.Fatalf("fix-version override: exit %d, want 1", run.code)
	}
	// 3.5: we answered their Logout with ours.
	log := mustRead(t, filepath.Join(dir, "logs", "fix", "strict_"+time.Now().UTC().Format("20060102")+".log"))
	if !regexp.MustCompile(`IN\s+seq=\d+\s+35=5 .*\n.*OUT\s+seq=2\s+35=5 .*58=Logout acknowledged`).MatchString(log) {
		t.Fatalf("no Logout reply to the refusal:\n%s", log)
	}

	run = runCLI(t, dir, 10*time.Second, "order", "--session", "emu42", "AAPL", "0", "buy", "mkt")
	if run.code != 2 {
		t.Fatalf("bad input: exit %d, want 2", run.code)
	}
	run = runCLI(t, dir, 10*time.Second, "--help")
	if !strings.Contains(run.out, "6  timed out waiting for an order") {
		t.Fatal("exit codes not in --help")
	}
}

// The piped session from REPORT_A2 section 3, plus the offline timeline.
func TestCLISessionPipedAndTimeline(t *testing.T) {
	e := startEmulator(t)
	dir := cliDir(t, e)
	input := "order EFG 1000 buy lmt 10.00\norder ZWZZT 500 buy lmt 10.00\nreplace last 800 10.50\ncancel last\nstatus\ntimeline last\nresend 1 2\ntestreq\nquit\n"
	run := runCLIInput(t, dir, input, 60*time.Second, "session", "--session", "emu42")
	if run.code != 0 {
		t.Fatalf("exit %d", run.code)
	}
	for _, want := range []string{"> order EFG", "150=5(Replaced)", "150=4(Canceled)", "CANCELED / PASS", "NEW / PASS",
		"Order chain for OE-", "  verdict: PASS", ">> ResendRequest 7=1 16=2 sent", ">> TestRequest TEST-1 answered", "Logged out cleanly"} {
		if !strings.Contains(run.out, want) {
			t.Fatalf("output lacks %q", want)
		}
	}
	m := regexp.MustCompile(`>> G 11=(\S+) 41=(\S+)`).FindStringSubmatch(run.out)
	if m == nil {
		t.Fatal("no G line")
	}
	logPath := filepath.Join(dir, "logs", "fix", "emu42_"+time.Now().UTC().Format("20060102")+".log")
	tl := runCLI(t, dir, 10*time.Second, "timeline", logPath, "--clordid", m[1], "--json")
	if tl.code != 0 || !strings.Contains(tl.out, `"verdict": "PASS"`) || !strings.Contains(tl.out, `"steps": 6`) {
		t.Fatalf("timeline: exit %d", tl.code)
	}
	// A WARN (unanswered request) exits 1, nothing found exits 2.
	lines := strings.Split(strings.TrimRight(mustRead(t, logPath), "\n"), "\n")
	var kept []string
	for _, l := range lines {
		if strings.Contains(l, " IN ") && strings.Contains(l, "|150=4|") {
			continue // drop the cancel's answer
		}
		kept = append(kept, l)
	}
	warnPath := filepath.Join(t.TempDir(), filepath.Base(logPath))
	os.WriteFile(warnPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
	if tl := runCLI(t, dir, 10*time.Second, "timeline", warnPath, "--clordid", m[1]); tl.code != 1 || !strings.Contains(tl.out, "[WARN] requests_answered") {
		t.Fatalf("warn timeline: exit %d", tl.code)
	}
	if tl := runCLI(t, dir, 10*time.Second, "timeline", logPath, "--clordid", "NOPE"); tl.code != 2 {
		t.Fatalf("empty timeline: exit %d", tl.code)
	}
	// Parity on the CLI's own log for every order it sent.
	var roots []string
	for _, mm := range regexp.MustCompile(`>> D 11=(\S+)`).FindAllStringSubmatch(run.out, -1) {
		roots = append(roots, mm[1])
	}
	var cases []parityCase
	for _, r := range roots {
		cases = append(cases, parityCase{Name: "CLI session / agent FIX log / " + r, Files: []string{logPath}, ClOrdID: r})
		cases = append(cases, parityCase{Name: "CLI session / emulator FIX log / " + r, Files: globAll(filepath.Join(e.dir, "logs", "fix", "agent42_*.log")), ClOrdID: r})
	}
	assertParity(t, cases)
}
