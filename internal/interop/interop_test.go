//go:build interop

package interop

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
)

func regexpIn(s, pattern string) bool { return regexp.MustCompile(pattern).MatchString(s) }

// Scenario 1: logon to a 4.2 and a 4.4 session, TestRequest round trip, clean
// logout in both directions.
func TestScenario1LogonTestRequestLogout(t *testing.T) {
	e := startEmulator(t)

	// 4.2: we log out.
	a42 := startAgent(t, agentSpec{id: "emu42", version: "FIX.4.2", target: "ORDERECHO", port: e.fixPort, reset: true})
	a42.waitActive(10*time.Second, 1)
	if st := e.status("agent42"); st.State != "ACTIVE" {
		t.Fatalf("emulator agent42 state %s", st.State)
	}
	a42.testRequestRoundTrip()
	inSync(t, a42, e, "agent42")
	if err := a42.ini.Logout("interop: agent logout"); err != nil {
		t.Fatal(err)
	}
	r := a42.wait(15 * time.Second)
	if !r.Outcome.CleanLogout || !r.Outcome.LogoutByUs {
		t.Fatalf("4.2 logout not clean: %+v", r.Outcome)
	}
	waitFor(t, 5*time.Second, "emulator agent42 DISCONNECTED", func() bool { return e.status("agent42").State == "DISCONNECTED" })

	// 4.4: the emulator logs out.
	a44 := startAgent(t, agentSpec{id: "emu44", version: "FIX.4.4", target: "ORDERECHO", port: e.fixPort, reset: true})
	a44.waitActive(10*time.Second, 1)
	if st := e.status("agent44"); st.State != "ACTIVE" {
		t.Fatalf("emulator agent44 state %s", st.State)
	}
	a44.testRequestRoundTrip()
	inSync(t, a44, e, "agent44")
	e.post("/sessions/agent44/logout", map[string]string{"text": "interop: emulator logout"})
	r = a44.wait(15 * time.Second)
	if !r.Outcome.CleanLogout || r.Outcome.LogoutByUs || r.Outcome.LogoutText != "interop: emulator logout" {
		t.Fatalf("4.4 logout not clean: %+v", r.Outcome)
	}
	for _, id := range []string{"agent42", "agent44"} {
		log := e.fixLog(id)
		if !strings.Contains(log, "35=5") {
			t.Fatalf("emulator %s FIX log has no Logout:\n%s", id, log)
		}
	}
	// FIX 4.4 wire check: the agent's 4.4 messages really say FIX.4.4.
	data, _ := os.ReadFile(a44.fixLogPath())
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.Contains(line, " 8=FIX.4.4|") {
			t.Fatalf("non-4.4 line in 4.4 log: %s", line)
		}
	}

	// And through the CLI: --test-request --duration, exit 0.
	dir := cliDir(t, e)
	for _, id := range []string{"emu42", "emu44"} {
		run := runCLI(t, dir, 30*time.Second, "connect", "--session", id, "--test-request", "--duration", "1s")
		if run.code != 0 {
			t.Fatalf("%s: exit %d", id, run.code)
		}
		for _, want := range []string{">>> Logged on", ">>> TestRequest TEST-1 answered", ">>> Logged out cleanly (initiated by us)"} {
			if !strings.Contains(run.out, want) {
				t.Fatalf("%s: output lacks %q", id, want)
			}
		}
	}
	// CLI with the emulator initiating the logout, exit 0.
	done := make(chan cliRun, 1)
	go func() { done <- runCLI(t, dir, 30*time.Second, "connect", "--session", "emu42") }()
	waitFor(t, 10*time.Second, "CLI logged on", func() bool { return e.status("agent42").State == "ACTIVE" })
	e.post("/sessions/agent42/logout", map[string]string{"text": "bye from emulator"})
	run := <-done
	if run.code != 0 || !strings.Contains(run.out, "Logged out cleanly (initiated by counterparty)") {
		t.Fatalf("emulator-initiated CLI logout: exit %d", run.code)
	}
}

// Scenario 2: an emulator-initiated TestRequest is answered.
func TestScenario2EmulatorTestRequest(t *testing.T) {
	e := startEmulator(t)
	a := startAgent(t, agentSpec{id: "emu42", version: "FIX.4.2", target: "ORDERECHO", port: e.fixPort, reset: true})
	a.waitActive(10*time.Second, 1)
	resp := e.post("/sessions/agent42/test-request", nil)
	id, _ := resp["test_req_id"].(string)
	if id == "" {
		t.Fatalf("no test_req_id in %v", resp)
	}
	waitFor(t, 5*time.Second, "emulator TestRequest answered", func() bool {
		return e.status("agent42").PendingTestReqID == ""
	})
	data, _ := os.ReadFile(a.fixLogPath())
	if !regexpIn(string(data), `IN\s+seq=\d+\s+35=1\s+.*\|112=`+regexp.QuoteMeta(id)+`\|`) ||
		!regexpIn(string(data), `OUT\s+seq=\d+\s+35=0\s+.*\|112=`+regexp.QuoteMeta(id)+`\|`) {
		t.Fatalf("agent log lacks TestRequest/Heartbeat for %s:\n%s", id, data)
	}
	inSync(t, a, e, "agent42")
}

// Scenario 3: the emulator skips 3 outbound seqs; we ask for a resend, it
// gap-fills, and both sides are in sync.
func TestScenario3EmulatorSeqGap(t *testing.T) {
	e := startEmulator(t)
	a := startAgent(t, agentSpec{id: "emu42", version: "FIX.4.2", target: "ORDERECHO", port: e.fixPort, reset: true})
	a.waitActive(10*time.Second, 1)
	before := a.snap().NextIn
	e.post("/sessions/agent42/inject/seq-gap", map[string]int{"skip": 3})
	// Something must carry the gap to us: an emulator TestRequest.
	e.post("/sessions/agent42/test-request", nil)

	waitFor(t, 5*time.Second, "agent ResendRequest + gap fill applied", func() bool {
		s := a.snap()
		return s.NextIn >= before+4 && s.State == session.Active
	})
	data, _ := os.ReadFile(a.fixLogPath())
	log := string(data)
	// A3 3.1: closed range, up to the seq before the one that revealed the gap.
	if !regexpIn(log, `OUT\s+seq=\d+\s+35=2\s+.*\|7=`+strconv.Itoa(before)+`\|16=`+strconv.Itoa(before+2)+`\|`) {
		t.Fatalf("no ResendRequest 7=%d 16=%d in agent log:\n%s", before, before+2, log)
	}
	if !regexpIn(log, `IN\s+seq=`+strconv.Itoa(before)+`\s+35=4\s+.*\|43=Y\|.*\|123=Y\|36=`) {
		t.Fatalf("no gap fill received at seq %d:\n%s", before, log)
	}
	if n := strings.Count(log, " 35=2  "); n != 1 {
		t.Fatalf("%d ResendRequests sent, want 1", n)
	}
	inSync(t, a, e, "agent42")
	a.testRequestRoundTrip()
	resp := e.post("/sessions/agent42/test-request", nil)
	waitFor(t, 5*time.Second, "emulator TestRequest answered after gap", func() bool {
		return e.status("agent42").PendingTestReqID == ""
	})
	_ = resp
	inSync(t, a, e, "agent42")
}

// Scenario 4: we skip 3 outbound seqs; the emulator asks for a resend; we
// gap-fill; both sides are in sync.
func TestScenario4AgentSkipOutboundSeq(t *testing.T) {
	e := startEmulator(t)
	a := startAgent(t, agentSpec{id: "emu44", version: "FIX.4.4", target: "ORDERECHO", port: e.fixPort, reset: true})
	a.waitActive(10*time.Second, 1)
	skipFrom := a.snap().NextOut
	if err := a.ini.SkipOutboundSeq(3); err != nil {
		t.Fatal(err)
	}
	// Carry the gap: a TestRequest on seq skipFrom+3.
	if _, err := a.ini.TestRequest(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "emulator ResendRequest answered", func() bool {
		data, _ := os.ReadFile(a.fixLogPath())
		return regexpIn(string(data), `IN\s+seq=\d+\s+35=2\s+.*\|7=`+strconv.Itoa(skipFrom)+`\|16=0\|`) &&
			regexpIn(string(data), `OUT\s+seq=`+strconv.Itoa(skipFrom)+`\s+35=4\s+`)
	})
	data, _ := os.ReadFile(a.fixLogPath())
	log := string(data)
	// One gap fill from skipFrom covering the skipped seqs and the (admin)
	// TestRequest, to our next_out.
	m := regexp.MustCompile(`OUT\s+seq=` + strconv.Itoa(skipFrom) + `\s+35=4\s+(\S+)`).FindStringSubmatch(log)
	gf, err := codec.DecodeOne(codec.FromPipe(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if gf.Value(43) != "Y" || gf.Value(123) != "Y" || gf.Value(36) != strconv.Itoa(skipFrom+4) || !gf.Has(122) {
		t.Fatalf("gap fill %s", gf.Pipe())
	}
	evData, _ := os.ReadFile(a.ev.Path)
	if !strings.Contains(string(evData), `"injected":true`) || !strings.Contains(string(evData), "injected seq gap") {
		t.Fatal("SkipOutboundSeq not in evidence as injected")
	}
	inSync(t, a, e, "agent44")
	a.testRequestRoundTrip()
	inSync(t, a, e, "agent44")
	// Replay of real application messages also works against the emulator:
	// SendRaw an app message the emulator will BusinessMessageReject, skip,
	// and let the emulator ask again.
	appSeq := a.snap().NextOut
	if err := a.ini.SendRaw([]codec.Field{codec.F(35, "H"), codec.F(37, "*"), codec.F(11, "X"), codec.F(55, "AAPL"), codec.F(54, "1")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "emulator answers the injected H", func() bool {
		return e.status("agent44").NextIn == appSeq+1
	})
	inSync(t, a, e, "agent44")
}

// Scenario 5: the emulator drops the connection; with reconnect the agent
// logs on again without a reset and the sequence continues.
func TestScenario5ReconnectWithoutReset(t *testing.T) {
	e := startEmulator(t)
	a := startAgent(t, agentSpec{id: "emu42", version: "FIX.4.2", target: "ORDERECHO", port: e.fixPort, reset: true, reconnect: true})
	a.waitActive(10*time.Second, 1)
	a.testRequestRoundTrip()
	inSync(t, a, e, "agent42")
	before := a.snap()
	e.post("/sessions/agent42/disconnect", nil)
	a.waitActive(15*time.Second, 2)
	after := a.snap()
	if after.Connects != 2 {
		t.Fatalf("connects %d", after.Connects)
	}
	// Second Logon continued the sequence: no 141, seq = the old next_out.
	data, _ := os.ReadFile(a.fixLogPath())
	var logons []string
	for _, line := range strings.Split(string(data), "\n") {
		if regexpIn(line, `^\S+ OUT\s+seq=\d+\s+35=A `) {
			logons = append(logons, line)
		}
	}
	if len(logons) != 2 {
		t.Fatalf("%d outbound Logons:\n%s", len(logons), data)
	}
	if !strings.Contains(logons[0], "|141=Y|") || strings.Contains(logons[1], "141=") {
		t.Fatalf("reset flags wrong:\n%s\n%s", logons[0], logons[1])
	}
	if !strings.Contains(logons[1], "|34="+strconv.Itoa(before.NextOut)+"|") {
		t.Fatalf("second logon not on seq %d: %s", before.NextOut, logons[1])
	}
	if after.NextIn != before.NextIn+1 {
		t.Fatalf("next_in %d -> %d, want +1 (their Logon)", before.NextIn, after.NextIn)
	}
	inSync(t, a, e, "agent42")
	a.testRequestRoundTrip()
	inSync(t, a, e, "agent42")
	if err := a.ini.Logout("done"); err != nil {
		t.Fatal(err)
	}
	if r := a.wait(15 * time.Second); !r.Outcome.CleanLogout {
		t.Fatalf("final logout: %+v", r.Outcome)
	}
}

// Scenario 6: 4.4 against the strict (4.2) session -> Logout "Incorrect
// BeginString..." surfaced; CLI exit 1.
func TestScenario6WrongVersion(t *testing.T) {
	e := startEmulator(t)
	dir := cliDir(t, e)
	run := runCLI(t, dir, 30*time.Second, "connect", "--session", "strict44")
	if run.code != 1 {
		t.Fatalf("exit %d, want 1", run.code)
	}
	for _, want := range []string{"LOGON REFUSED by counterparty", "Incorrect BeginString, expected FIX.4.2"} {
		if !strings.Contains(run.out, want) {
			t.Fatalf("output lacks %q", want)
		}
	}
	// Evidence recorded the refusal text.
	evs := globAll(filepath.Join(dir, "data", "evidence", "*.jsonl"))
	if len(evs) != 1 {
		t.Fatalf("evidence files %v", evs)
	}
	data, _ := os.ReadFile(evs[0])
	if !strings.Contains(string(data), "logon refused: counterparty answered Logon with Logout: Incorrect BeginString, expected FIX.4.2") {
		t.Fatalf("evidence lacks refusal:\n%s", data)
	}
	// The same session on 4.2 works.
	if run := runCLI(t, dir, 30*time.Second, "connect", "--session", "strict", "--duration", "500ms"); run.code != 0 {
		t.Fatalf("strict 4.2 exit %d", run.code)
	}
}

// Scenario 7: unknown CompIDs -> the emulator drops the connection; CLI exit 1
// with a clear message.
func TestScenario7UnknownCompIDs(t *testing.T) {
	e := startEmulator(t)
	dir := cliDir(t, e)
	run := runCLI(t, dir, 30*time.Second, "connect", "--session", "unknown")
	if run.code != 1 {
		t.Fatalf("exit %d, want 1", run.code)
	}
	if !strings.Contains(run.out, "closed the connection without answering our Logon") ||
		!strings.Contains(run.out, "sender_comp_id=NOBODY") {
		t.Fatalf("unclear message")
	}
	engine := ""
	for _, p := range globAll(filepath.Join(e.dir, "logs", "engine", "*.log")) {
		d, _ := os.ReadFile(p)
		engine += string(d)
	}
	if !strings.Contains(engine, "unknown session") {
		t.Fatalf("emulator did not log an unknown session:\n%s", engine)
	}
}

// Scenario 8: the emulator's own log viewer reads the agent's FIX log and
// evidence file.
func TestScenario8ViewerReadsAgentLogs(t *testing.T) {
	e := startEmulator(t)
	a := startAgent(t, agentSpec{id: "emu42", version: "FIX.4.2", target: "ORDERECHO", port: e.fixPort, reset: true})
	a.waitActive(10*time.Second, 1)
	a.testRequestRoundTrip()
	e.post("/sessions/agent42/test-request", nil)
	waitFor(t, 5*time.Second, "emulator TestRequest answered", func() bool { return e.status("agent42").PendingTestReqID == "" })
	// Exercise resend traffic in both directions so the log has 2 and 4.
	e.post("/sessions/agent42/inject/seq-gap", map[string]int{"skip": 2})
	e.post("/sessions/agent42/test-request", nil)
	waitFor(t, 5*time.Second, "emulator gap handled", func() bool { return a.snap().NextIn == e.status("agent42").NextOut })
	// Our own gap: the TestRequest that carries it is gap-filled, not answered.
	a.ini.SkipOutboundSeq(2)
	a.ini.TestRequest()
	waitFor(t, 5*time.Second, "agent gap handled", func() bool { return e.status("agent42").NextIn == a.snap().NextOut })
	inSync(t, a, e, "agent42")
	a.testRequestRoundTrip()
	inSync(t, a, e, "agent42")
	a.ini.Logout("viewer test done")
	if r := a.wait(15 * time.Second); !r.Outcome.CleanLogout {
		t.Fatalf("logout: %+v", r.Outcome)
	}

	logPath := a.fixLogPath()
	data, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	in, out := 0, 0
	for _, l := range lines {
		f := strings.Fields(l)
		switch f[1] {
		case "IN":
			in++
			if !strings.Contains(l, "|56=AGENT|") {
				t.Fatalf("IN line not addressed to us: %s", l)
			}
		case "OUT":
			out++
			if !strings.Contains(l, "|49=AGENT|") {
				t.Fatalf("OUT line not from us: %s", l)
			}
		default:
			t.Fatalf("unexpected direction: %s", l)
		}
	}

	viewer := filepath.Join(emulatorDir, "orderecho_LogView.py")
	stats := runPython(t, viewer, "stats", "--no-color", logPath)
	t.Logf("viewer stats:\n%s", stats)
	for _, want := range []string{
		strconv.Itoa(len(lines)) + " message(s) from 1 file(s)",
		"unparseable lines 0", "bad checksum      0", "bad body length   0",
	} {
		if !strings.Contains(stats, want) {
			t.Fatalf("viewer stats lack %q", want)
		}
	}
	if !regexpIn(stats, `(?m)^\s+in\s+`+strconv.Itoa(in)+`$`) || !regexpIn(stats, `(?m)^\s+out\s+`+strconv.Itoa(out)+`$`) {
		t.Fatalf("viewer directions differ from the log (in=%d out=%d)", in, out)
	}
	if !regexpIn(stats, `resend requests\s+[1-9]`) || !regexpIn(stats, `gap fills\s+[1-9]`) {
		t.Fatal("viewer saw no resend/gap-fill traffic")
	}

	view := runPython(t, viewer, "view", "--no-color", logPath)
	t.Logf("viewer view:\n%s", view)
	viewLines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(viewLines) != len(lines) || strings.Contains(view, "unparseable") {
		t.Fatalf("view printed %d lines for %d messages", len(viewLines), len(lines))
	}
	for i, l := range lines {
		dir := strings.Fields(l)[1]
		want := map[string]string{"IN": " --> ", "OUT": " <-- "}[dir]
		if !strings.Contains(viewLines[i], want) || !strings.Contains(viewLines[i], " emu42 ") {
			t.Fatalf("view line %d direction/session wrong: %q for %s", i, viewLines[i], dir)
		}
	}

	// The evidence JSONL parses: in Go, and in the viewer.
	f, _ := os.Open(a.ev.Path)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	msgs := 0
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("evidence line: %v: %s", err, sc.Text())
		}
		if k := rec["kind"]; k == "in" || k == "out" || k == "discarded" {
			msgs++
		}
	}
	if msgs != len(lines) {
		t.Fatalf("evidence has %d messages, FIX log %d", msgs, len(lines))
	}
	evStats := runPython(t, viewer, "stats", "--no-color", a.ev.Path)
	t.Logf("viewer stats on evidence:\n%s", evStats)
	if !strings.Contains(evStats, strconv.Itoa(msgs)+" message(s) from 1 file(s)") || !strings.Contains(evStats, "unparseable lines 0") {
		t.Fatal("viewer could not read the evidence file")
	}
}

func runPython(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(pythonBin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Ctrl+C on a logged-on CLI session sends a clean Logout and exits 0.
func TestCtrlCLogsOutCleanly(t *testing.T) {
	e := startEmulator(t)
	dir := cliDir(t, e)
	cmd := exec.Command(agentBin, "--config", "orderecho.yaml", "connect", "--session", "emu44")
	cmd.Dir = dir
	var out lockedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "CLI logged on", func() bool { return e.status("agent44").State == "ACTIVE" })
	cmd.Process.Signal(os.Interrupt)
	err := cmd.Wait()
	t.Logf("CLI output:\n%s", out.String())
	if err != nil {
		t.Fatalf("exit: %v", err)
	}
	if !strings.Contains(out.String(), "Logged out cleanly (initiated by us)") || !strings.Contains(out.String(), "58=OrderEcho agent shutting down") {
		t.Fatal("no clean logout on Ctrl+C")
	}
	waitFor(t, 5*time.Second, "emulator DISCONNECTED", func() bool { return e.status("agent44").State == "DISCONNECTED" })
}
