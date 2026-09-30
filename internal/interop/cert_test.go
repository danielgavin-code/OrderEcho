//go:build interop

package interop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// certDir prepares a CLI directory for cert runs against e: an agent config
// (HeartBtInt 10s so the heartbeat case is quick), the shipped suites, and
// targets pointing at this emulator's control API.
func certDir(t *testing.T, e *emulator) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cert")
	os.MkdirAll(filepath.Join(dir, "certs", "targets"), 0o755)
	cfg := strings.ReplaceAll(fmt.Sprintf(agentConfig, e.fixPort, e.strictPort), "heartbeat_sec: 30", "heartbeat_sec: 10")
	cfg = strings.Replace(cfg, "console: true", "console: false", 1)
	if err := os.WriteFile(filepath.Join(dir, "orderecho.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"order_entry_fix42.yaml", "order_entry_fix44.yaml"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, "certs", name))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "certs", name), data, 0o644)
	}
	emuTarget := mustRead(t, filepath.Join(repoRoot, "certs", "targets", "emulator.yaml"))
	emuTarget = strings.Replace(emuTarget, "http://127.0.0.1:8090", fmt.Sprintf("http://127.0.0.1:%d", e.apiPort), 1)
	os.WriteFile(filepath.Join(dir, "certs", "targets", "emulator.yaml"), []byte(emuTarget), 0o644)
	generic := mustRead(t, filepath.Join(repoRoot, "certs", "targets", "generic.yaml"))
	os.WriteFile(filepath.Join(dir, "certs", "targets", "generic.yaml"), []byte(generic), 0o644)
	return dir
}

const manualAttestations = `"1.1": { status: pass, by: "interop test", note: "emulator is local" }
"1.4": { status: na, by: "interop test", note: "same connection" }
"1.5": { status: pass, by: "interop test", note: "AGENT/ORDERECHO" }
"1.6": { status: pass, by: "interop test", note: "clear-text loopback" }
"1.7": { status: pass, by: "interop test", note: "emulator = UAT" }
"9.1": { status: pass, by: "interop test", note: "reviewed" }
"9.2": { status: pass, by: "interop test", note: "N/A reasons" }
"9.3": { status: pass, by: "interop test", note: "simulated" }
"9.4": { status: pass, by: "interop test", note: "simulated" }
"9.5": { status: na, by: "interop test", note: "test" }
"9.6": { status: na, by: "interop test", note: "test" }
`

type certResults struct {
	Counts   map[string]int `json:"counts"`
	Required map[string]int `json:"required_counts"`
	Exit     int            `json:"exit_code"`
	Session  string         `json:"session"`
	Cases    []struct {
		ID          string `json:"id"`
		Mode        string `json:"mode"`
		Section     string `json:"section"`
		Required    bool   `json:"required"`
		Status      string `json:"status"`
		Reason      string `json:"reason"`
		Attestation *struct {
			By string `json:"by"`
		} `json:"attestation"`
		Steps []struct{ Type, Status, Detail string } `json:"steps"`
	} `json:"cases"`
}

var resultsLine = []byte("results       : ")

func runCert(t *testing.T, dir string, timeout time.Duration, args ...string) (cliRun, certResults, string) {
	t.Helper()
	run := runCLI(t, dir, timeout, append([]string{"cert", "run"}, args...)...)
	i := bytes.LastIndex([]byte(run.out), resultsLine)
	if i < 0 {
		t.Fatalf("no results path in output")
	}
	rest := run.out[i+len(resultsLine):]
	resDir := filepath.Join(dir, strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0]))
	var res certResults
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(resDir, "results.json"))), &res); err != nil {
		t.Fatal(err)
	}
	return run, res, resDir
}

// The emulator run must end with every auto case PASS or N/A, the emulator
// control steps PASS, and only the manual cases PENDING.
func checkEmulatorRun(t *testing.T, res certResults, wantManual string) {
	t.Helper()
	for _, c := range res.Cases {
		switch c.Mode {
		case "manual":
			if wantManual == "attested" {
				if c.Status != "PASS" && c.Status != "N/A" {
					t.Errorf("attested manual %s: %s (%s)", c.ID, c.Status, c.Reason)
				}
			} else if c.Status != wantManual {
				t.Errorf("manual %s: %s (%s)", c.ID, c.Status, c.Reason)
			}
		default:
			if c.Status != "PASS" && c.Status != "N/A" {
				t.Errorf("%s %s: %s — %s", c.Mode, c.ID, c.Status, c.Reason)
			}
		}
	}
	if res.Counts["PASS"] < 40 {
		t.Errorf("only %d PASS", res.Counts["PASS"])
	}
}

// A3 interop 1: the Order Entry suite vs the emulator on agent42, then with
// attestations for the manual steps.
func TestCertEmulatorFIX42(t *testing.T) {
	e := startEmulator(t)
	dir := certDir(t, e)
	run, res, resDir := runCert(t, dir, 10*time.Minute, "--suite", "certs/order_entry_fix42.yaml", "--target", "certs/targets/emulator.yaml", "--session", "emu42")
	if run.code != 7 || res.Exit != 7 {
		t.Fatalf("exit %d (results %d), want 7", run.code, res.Exit)
	}
	checkEmulatorRun(t, res, "PENDING")
	// The assisted cases the emulator can do ran through its control API.
	for _, c := range res.Cases {
		if c.Mode == "assisted" && c.Status == "PASS" {
			found := false
			for _, s := range c.Steps {
				if s.Type == "control" && s.Status == "PASS" && strings.Contains(s.Detail, "-> 200") {
					found = true
				}
			}
			if !found {
				t.Errorf("assisted %s passed without a control call", c.ID)
			}
		}
	}
	// A case folder has its own FIX log slice and evidence.
	fix := mustRead(t, filepath.Join(resDir, "7.7", "fix.log"))
	ev := mustRead(t, filepath.Join(resDir, "7.7", "evidence.jsonl"))
	if strings.Count(fix, " 35=D ") != 2 || !strings.Contains(fix, "|103=6|") || !strings.Contains(ev, "cert case start: 7.7") || !strings.Contains(ev, "cert case end: 7.7 PASS") {
		t.Fatalf("7.7 slice:\n%s", fix)
	}
	if sum := mustRead(t, filepath.Join(resDir, "summary.txt")); !strings.Contains(sum, "exit code     : 7") {
		t.Fatal("summary")
	}
	recordVerdict("%-44s exit %d  %s", "A3-1 cert FIX 4.2 emulator (no attest)", res.Exit, countsLine(res.Counts))

	os.WriteFile(filepath.Join(dir, "attest.yaml"), []byte(manualAttestations), 0o644)
	run, res, _ = runCert(t, dir, 10*time.Minute, "--suite", "certs/order_entry_fix42.yaml", "--target", "certs/targets/emulator.yaml", "--session", "emu42", "--attest", "attest.yaml")
	if run.code != 0 || res.Exit != 0 {
		t.Fatalf("attested run exit %d, want 0", run.code)
	}
	for _, c := range res.Cases {
		if c.Mode == "manual" && (c.Attestation == nil || c.Attestation.By != "interop test") {
			t.Errorf("manual %s: attestation not recorded", c.ID)
		}
	}
	checkEmulatorRun(t, res, "attested")
	recordVerdict("%-44s exit %d  %s", "A3-1 cert FIX 4.2 emulator (attested)", res.Exit, countsLine(res.Counts))
}

// A3 interop 2: the FIX 4.4 variant on agent44.
func TestCertEmulatorFIX44(t *testing.T) {
	e := startEmulator(t)
	dir := certDir(t, e)
	run, res, _ := runCert(t, dir, 10*time.Minute, "--suite", "certs/order_entry_fix44.yaml", "--target", "certs/targets/emulator.yaml", "--session", "emu44")
	if run.code != 7 {
		t.Fatalf("exit %d, want 7", run.code)
	}
	checkEmulatorRun(t, res, "PENDING")
	if !strings.Contains(run.out, "(FIX.4.4, 68 cases)") {
		t.Fatal("not the 4.4 suite")
	}
	recordVerdict("%-44s exit %d  %s", "A3-2 cert FIX 4.4 emulator", res.Exit, countsLine(res.Counts))
	// Mismatched suite/session versions are refused up front.
	if r := runCLI(t, dir, time.Minute, "cert", "run", "--suite", "certs/order_entry_fix42.yaml", "--target", "certs/targets/emulator.yaml", "--session", "emu44"); r.code != 2 {
		t.Fatalf("version mismatch exit %d", r.code)
	}
}

// A3 interop 3: negative control against the strict broker.
func TestCertStrictBrokerNegativeControl(t *testing.T) {
	e := startEmulator(t)
	dir := certDir(t, e)
	run, res, _ := runCert(t, dir, 10*time.Minute, "--suite", "certs/order_entry_fix42.yaml", "--target", "certs/targets/emulator.yaml", "--session", "strict")
	if run.code != 5 {
		t.Fatalf("exit %d, want 5", run.code)
	}
	orderFail, sessionPass := 0, 0
	for _, c := range res.Cases {
		switch {
		case c.Section == "ORD" || c.Section == "LCY" || c.Section == "CXL":
			if c.Status == "FAIL" {
				orderFail++
				if !strings.Contains(c.Reason, "rejected the order") || !strings.Contains(c.Reason, "Strict broker rejects everything") {
					t.Errorf("%s FAIL reason unclear: %s", c.ID, c.Reason)
				}
			} else if c.Status != "N/A" {
				t.Errorf("order case %s: %s (want FAIL)", c.ID, c.Status)
			}
		case c.Section == "SES" || c.ID == "3.1" || c.ID == "3.2" || c.ID == "3.3" || c.ID == "3.4" || c.ID == "3.6" || c.ID == "3.7" || c.ID == "3.8" || c.ID == "8.6":
			if c.Status != "PASS" {
				t.Errorf("session case %s: %s — %s", c.ID, c.Status, c.Reason)
			} else {
				sessionPass++
			}
		}
	}
	if orderFail < 15 || sessionPass < 15 {
		t.Fatalf("%d order FAIL, %d session PASS", orderFail, sessionPass)
	}
	recordVerdict("%-44s exit %d  %s", "A3-3 cert strict broker (negative control)", res.Exit, countsLine(res.Counts))
}

// A3 interop 4: a target without a control API blocks the assisted cases.
func TestCertGenericTargetBlocksAssisted(t *testing.T) {
	e := startEmulator(t)
	dir := certDir(t, e)
	run, res, _ := runCert(t, dir, 5*time.Minute, "--suite", "certs/order_entry_fix42.yaml", "--target", "certs/targets/generic.yaml", "--session", "emu42",
		"--case", "3.4,5.3,5.5,5.6,5.8,6.4,8.1,8.7,4.1", "--var", "hold_symbol=ZWZZT", "--var", "limit_symbol=ZWZZT")
	blocked := 0
	for _, c := range res.Cases {
		if c.Mode == "assisted" {
			if c.Status != "BLOCKED" || !strings.HasPrefix(c.Reason, "BLOCKED: needs counterparty action: the counterparty") {
				t.Errorf("assisted %s: %s %s", c.ID, c.Status, c.Reason)
			}
			blocked++
		} else if c.Status != "PASS" {
			t.Errorf("auto %s: %s %s", c.ID, c.Status, c.Reason)
		}
	}
	if blocked != 8 || run.code != 7 {
		t.Fatalf("%d blocked, exit %d (want 8, 7)", blocked, run.code)
	}
	recordVerdict("%-44s exit %d  %s", "A3-4 cert generic target (assisted BLOCKED)", res.Exit, countsLine(res.Counts))
}

const brokenSuite = `suite: broken
title: Deliberately broken cases (test only)
fix_version: FIX.4.2
sections: [ { id: T, name: Test } ]
cases:
  - id: "B.1"
    section: T
    title: "Expects FILLED on a hold symbol"
    task: "test only"
    required: true
    level: basic
    mode: auto
    steps:
      - send: { msg: D, ref: o1, side: buy, ord_type: lmt, symbol: ZWZZT, qty: "100", price: "1.00" }
      - expect: { exec_type: NEW, within: 5s }
      - expect: { ord_status: FILLED, within: 3s }
  - id: "B.2"
    section: T
    title: "A long wait the emulator dies during"
    task: "test only"
    required: true
    level: basic
    mode: auto
    steps:
      - session: { wait: 20s }
      - session: testreq
  - id: "B.3"
    section: T
    title: "After the counterparty is gone"
    task: "test only"
    required: true
    level: basic
    mode: auto
    steps:
      - session: testreq
`

// A3 interop 5: a broken case FAILs with its step and timeout reason; an
// emulator killed mid-case gives ERROR and the session exit code.
func TestCertBrokenCaseAndError(t *testing.T) {
	e := startEmulator(t)
	dir := certDir(t, e)
	os.WriteFile(filepath.Join(dir, "certs", "broken.yaml"), []byte(brokenSuite), 0o644)
	run, res, _ := runCert(t, dir, 3*time.Minute, "--suite", "certs/broken.yaml", "--target", "certs/targets/emulator.yaml", "--session", "emu42", "--case", "B.1")
	if run.code != 5 || res.Cases[0].Status != "FAIL" ||
		!strings.Contains(res.Cases[0].Reason, "step 3 (expect): timed out after 3s waiting for ord_status=FILLED") ||
		!strings.Contains(res.Cases[0].Reason, "last relevant message: 35=8") {
		t.Fatalf("exit %d: %+v", run.code, res.Cases[0])
	}
	recordVerdict("%-44s exit %d  %s", "A3-5 broken case (FAIL + reason)", res.Exit, res.Cases[0].Reason)

	// The ERROR path: kill the emulator during B.2's wait.
	cmd := exec.Command(agentBin, "--config", "orderecho.yaml", "cert", "run", "--suite", "certs/broken.yaml",
		"--target", "certs/targets/emulator.yaml", "--session", "emu42", "--case", "B.2,B.3")
	cmd.Dir = dir
	var out lockedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "cert run logged on", func() bool { return e.status("agent42").State == "ACTIVE" })
	time.Sleep(2 * time.Second)
	e.cmd.Process.Signal(syscall.SIGKILL)
	err := cmd.Wait()
	t.Logf("cert run with the emulator killed:\n%s", out.String())
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	if code != 3 {
		t.Fatalf("exit %d, want 3 (session dropped and not recovered)", code)
	}
	o := out.String()
	if !strings.Contains(o, "B.2   auto     ERROR    ") || !strings.Contains(o, "session dropped") || !strings.Contains(o, "B.3   auto     ERROR    ") {
		t.Fatal("ERROR statuses not reported")
	}
	recordVerdict("%-44s exit %d  B.2/B.3 ERROR (session dropped)", "A3-5 emulator killed mid-case", code)
}

func countsLine(c map[string]int) string {
	var parts []string
	for _, s := range []string{"PASS", "FAIL", "BLOCKED", "PENDING", "N/A", "ERROR"} {
		if c[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", s, c[s]))
		}
	}
	return strings.Join(parts, ", ")
}
