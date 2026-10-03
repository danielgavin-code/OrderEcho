//go:build interop

package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A4 interop: the real emulator, the real agent service, and a real MCP
// client from the Go SDK. Over stdio the client spawns "orderecho mcp",
// which starts the service in the background on its own.

// mcpDir is certDir plus the A4 config: a free service port and the
// emulator's control API behind the emulator tools.
func mcpDir(t *testing.T, e *emulator) (dir string, servicePort int) {
	t.Helper()
	dir = certDir(t, e)
	servicePort = freePort(t)
	extra := fmt.Sprintf("service: { port: %d }\nemulator: { control_api: \"http://127.0.0.1:%d\", sessions: { emu42: agent42, emu44: agent44, strict: strict-broker } }\n",
		servicePort, e.apiPort)
	f, err := os.OpenFile(filepath.Join(dir, "orderecho.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(extra)
	f.Close()
	t.Cleanup(func() { stopService(t, servicePort) })
	return dir, servicePort
}

type health struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
	MCPHTTP bool   `json:"mcp_http"`
}

func serviceHealth(port int) (*health, error) {
	resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var h health
	return &h, json.NewDecoder(resp.Body).Decode(&h)
}

// stopService stops whatever service answers on port (SIGTERM, then KILL).
func stopService(t *testing.T, port int) {
	h, err := serviceHealth(port)
	if err != nil {
		return
	}
	p, _ := os.FindProcess(h.PID)
	p.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := serviceHealth(port); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.Kill()
}

func killService(t *testing.T, port int, sig syscall.Signal) int {
	t.Helper()
	h, err := serviceHealth(port)
	if err != nil {
		t.Fatalf("no service to kill: %v", err)
	}
	p, _ := os.FindProcess(h.PID)
	p.Signal(sig)
	waitFor(t, 40*time.Second, "service gone", func() bool { _, err := serviceHealth(port); return err != nil })
	return h.PID
}

// ------------------------------------------------------------ client

type mcpClient struct {
	t      *testing.T
	cs     *mcp.ClientSession
	stderr *lockedBuffer
}

// stdioClient spawns "orderecho mcp" in dir as Claude Desktop would.
func stdioClient(t *testing.T, dir string) *mcpClient {
	t.Helper()
	cmd := exec.Command(agentBin, "mcp", "--config", filepath.Join(dir, "orderecho.yaml"))
	cmd.Dir = dir
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ORDERECHO_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "orderecho-interop", Version: "a4"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("starting orderecho mcp: %v\n%s", err, stderr.String())
	}
	c := &mcpClient{t: t, cs: cs, stderr: stderr}
	t.Cleanup(func() {
		cs.Close()
		if t.Failed() {
			t.Logf("orderecho mcp stderr:\n%s", stderr.String())
			for _, p := range globAll(filepath.Join(dir, "logs", "engine", "*")) {
				data, _ := os.ReadFile(p)
				t.Logf("%s:\n%s", p, data)
			}
		}
	})
	return c
}

func summaryOf(r *mcp.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (c *mcpClient) raw(name string, args map[string]any) *mcp.CallToolResult {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := c.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		c.t.Fatalf("%s: %v", name, err)
	}
	a, _ := json.Marshal(args)
	c.t.Logf("MCP %s %s\n    -> %s", name, a, summaryOf(res))
	return res
}

// call expects success and returns (structured content, summary).
func (c *mcpClient) call(name string, args map[string]any) (map[string]any, string) {
	c.t.Helper()
	res := c.raw(name, args)
	if res.IsError {
		c.t.Fatalf("%s failed: %s", name, summaryOf(res))
	}
	return structured(c.t, res), summaryOf(res)
}

// callErr expects a tool error and returns its {error, detail, hint}.
func (c *mcpClient) callErr(name string, args map[string]any) (map[string]any, string) {
	c.t.Helper()
	res := c.raw(name, args)
	if !res.IsError {
		c.t.Fatalf("%s should have failed: %s", name, summaryOf(res))
	}
	return structured(c.t, res), summaryOf(res)
}

func structured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	data, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("structured content is not an object: %s", data)
	}
	return m
}

func get(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[p]
	}
	return v
}

func str(m map[string]any, path ...string) string { s, _ := get(m, path...).(string); return s }

func list(m map[string]any, path ...string) []any { l, _ := get(m, path...).([]any); return l }

// allChecksPass asserts the 11 checks are all PASS.
func allChecksPass(t *testing.T, checks []any) {
	t.Helper()
	if len(checks) != 11 {
		t.Fatalf("%d checks", len(checks))
	}
	for _, c := range checks {
		cm := c.(map[string]any)
		if cm["status"] != "PASS" {
			t.Errorf("check %v %v: %v", cm["name"], cm["status"], cm["explanation"])
		}
	}
}

// waitRun polls cert_run_status until the run is no longer running.
func (c *mcpClient) waitRun(runID string, timeout time.Duration) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := c.cs.CallTool(ctx, &mcp.CallToolParams{Name: "cert_run_status", Arguments: map[string]any{"run_id": runID}})
		cancel()
		if err != nil || res.IsError {
			c.t.Fatalf("cert_run_status: %v %s", err, summaryOf(res))
		}
		out := structured(c.t, res)
		if str(out, "run", "state") != "running" {
			c.t.Logf("MCP cert_run_status {\"run_id\":%q}\n    -> %s", runID, summaryOf(res))
			return out
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("run %s still running after %s: %s", runID, timeout, summaryOf(res))
		}
		time.Sleep(time.Second)
	}
}

func counts(m map[string]any, key string) map[string]int {
	out := map[string]int{}
	for k, v := range get(m, key).(map[string]any) {
		out[k] = int(v.(float64))
	}
	return out
}

// ------------------------------------------------------------ 1-3

// A4 interop 1-3: stdio (auto-started service), an order, the emulator
// filling a resting order twice, a replace on the hold symbol.
func TestMCPStdioOrdersAndEmulatorTools(t *testing.T) {
	e := startEmulator(t)
	dir, port := mcpDir(t, e)
	if _, err := serviceHealth(port); err == nil {
		t.Fatal("a service is already running on the test port")
	}
	c := stdioClient(t, dir)
	h, err := serviceHealth(port)
	if err != nil || h.Version != "0.4.0" {
		t.Fatalf("orderecho mcp did not start the service: %v", err)
	}
	if !strings.Contains(c.stderr.String(), "no agent service on") {
		t.Fatalf("stderr: %s", c.stderr.String())
	}
	if outs := globAll(filepath.Join(dir, "logs", "engine", "service_*.out")); len(outs) != 1 {
		t.Fatalf("service output not under logs/engine: %v", outs)
	}
	tools, err := c.cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 20 {
		t.Fatalf("%v %d tools", err, len(tools.Tools))
	}

	t.Run("1 stdio order", func(t *testing.T) {
		c.t = t
		out, sum := c.call("list_sessions", nil)
		if len(list(out, "sessions")) != 5 || !strings.Contains(sum, "emu42 (FIX.4.2 AGENT -> ORDERECHO) DISCONNECTED [emulator]") {
			t.Fatal(sum)
		}
		out, sum = c.call("connect_session", map[string]any{"session_id": "emu42"})
		if str(out, "state") != "ACTIVE" || !strings.Contains(sum, "emu42 logged on to ORDERECHO as AGENT") {
			t.Fatal(sum)
		}
		out, sum = c.call("send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL", "qty": 100, "side": "buy", "ord_type": "mkt", "wait_for": "terminal"})
		if str(out, "state") != "FILLED" || str(out, "checks", "verdict") != "PASS" || get(out, "timed_out") != false {
			t.Fatalf("%s", sum)
		}
		allChecksPass(t, list(out, "checks", "results"))
		if len(list(out, "reports")) < 2 || str(out, "shadow", "cum_qty_expected") != "100" || str(out, "shadow", "avg_px_reported") == "" {
			t.Fatalf("%v", out)
		}
		root := str(out, "cl_ord_id")
		out, sum = c.call("order_timeline", map[string]any{"session_id": "emu42", "cl_ord_id": "last"})
		if str(out, "seed") != root || str(out, "verdict") != "PASS" || !regexp.MustCompile(`: D -> 8\(New\) -> 8\(Fill\); 11/11 checks PASS`).MatchString(sum) {
			t.Fatalf("%s", sum)
		}
		allChecksPass(t, list(out, "checks"))
		for _, ck := range list(out, "checks") {
			if str(ck.(map[string]any), "explanation") == "" {
				t.Fatal("check without explanation")
			}
		}
		out, sum = c.call("recent_messages", map[string]any{"session_id": "emu42", "limit": 10})
		msgs := list(out, "messages")
		if len(msgs) == 0 || !strings.Contains(sum, "ExecutionReport") {
			t.Fatal(sum)
		}
		last := msgs[len(msgs)-1].(map[string]any)
		if last["msg_type_name"] != "ExecutionReport" || last["direction"] != "in" {
			t.Fatalf("%v", last)
		}
		named := false
		for _, f := range last["fields"].([]any) {
			fm := f.(map[string]any)
			if fm["name"] == "OrdStatus" && fm["value"] == "2" && fm["meaning"] == "Filled" {
				named = true
			}
		}
		if !named {
			t.Fatalf("fields not decoded by name: %v", last["fields"])
		}
		out, _ = c.call("recent_messages", map[string]any{"session_id": "emu42", "direction": "out", "msg_types": []string{"NewOrderSingle"}})
		if m := list(out, "messages"); len(m) != 1 || m[0].(map[string]any)["msg_type"] != "D" {
			t.Fatalf("filter: %v", m)
		}
	})

	t.Run("2 emulator fills and cancel", func(t *testing.T) {
		c.t = t
		// The emulator alters its next ExecutionReport (a text we can spot).
		out, sum := c.call("emulator_inject_next", map[string]any{"session": "emu42", "msg_type": "8", "set": map[string]string{"58": "altered by the interop test"}})
		if str(out, "emulator_session") != "agent42" || !strings.Contains(sum, "emulator agent42 will alter the next 35=8") {
			t.Fatal(sum)
		}
		out, sum = c.call("send_order", map[string]any{"session_id": "emu42", "symbol": "ZWZZT", "qty": 1000, "side": "buy", "ord_type": "lmt", "price": "10.00", "wait_for": "ack"})
		orderID := str(out, "order_id")
		if str(out, "state") != "NEW" || orderID == "" || !strings.Contains(strings.Join(func() []string {
			var r []string
			for _, x := range list(out, "reports") {
				r = append(r, x.(string))
			}
			return r
		}(), " "), "altered by the interop test") {
			t.Fatal(sum)
		}
		out, sum = c.call("emulator_fill_order", map[string]any{"order_id": orderID, "qty": 100, "price": "10.00"})
		if str(out, "acted_on") != "counterparty emulator (not a real venue)" || str(out, "our_view", "order", "state") != "PARTIALLY_FILLED" {
			t.Fatal(sum)
		}
		out, sum = c.call("emulator_fill_order", map[string]any{"order_id": orderID, "qty": 300, "price": "9.95"})
		if str(out, "our_view", "order", "shadow", "cum_qty_expected") != "400" {
			t.Fatal(sum)
		}
		out, sum = c.call("order_timeline", map[string]any{"session_id": "emu42", "order_id": orderID})
		// (100 x 10.00 + 300 x 9.95) / 400 = 9.9625 exactly, both sides.
		if str(out, "shadow", "avg_px_expected") != "9.9625" || str(out, "shadow", "avg_px_reported") != "9.9625" {
			t.Fatalf("AvgPx: expected %s reported %s", str(out, "shadow", "avg_px_expected"), str(out, "shadow", "avg_px_reported"))
		}
		steps := list(out, "steps")
		if lastStep := steps[len(steps)-1].(map[string]any); lastStep["avg_px"] != "9.9625" || lastStep["cum_qty"] != "400" || lastStep["leaves_qty"] != "600" {
			t.Fatalf("%v", lastStep)
		}
		for _, ck := range list(out, "checks") {
			if cm := ck.(map[string]any); cm["name"] == "avg_px" && cm["status"] != "PASS" {
				t.Fatalf("avg_px %v", cm)
			}
		}
		allChecksPass(t, list(out, "checks"))
		out, sum = c.call("cancel_order", map[string]any{"session_id": "emu42", "cl_ord_id": "last"})
		if str(out, "state") != "CANCELED" || str(out, "shadow", "cum_qty_expected") != "400" || str(out, "shadow", "leaves_qty_expected") != "0" || str(out, "checks", "verdict") != "PASS" {
			t.Fatal(sum)
		}
		// The emulator refuses to fill what is over; the error says what to do.
		errOut, _ := c.callErr("emulator_fill_order", map[string]any{"order_id": orderID, "qty": 1})
		if !strings.HasPrefix(str(errOut, "error"), "emulator_") || str(errOut, "hint") == "" {
			t.Fatalf("%v", errOut)
		}
	})

	t.Run("3 replace on the hold symbol", func(t *testing.T) {
		c.t = t
		out, sum := c.call("send_order", map[string]any{"session_id": "emu42", "symbol": "ZWZZT", "qty": 1000, "side": "buy", "ord_type": "lmt", "price": "10.00"})
		if str(out, "state") != "NEW" {
			t.Fatal(sum)
		}
		c.call("emulator_hold_order", map[string]any{"order_id": str(out, "order_id")})
		out, sum = c.call("replace_order", map[string]any{"session_id": "emu42", "cl_ord_id": "last", "qty": 800, "price": "10.50"})
		if str(out, "state") != "NEW" || str(out, "order_qty") != "800" || str(out, "price") != "10.50" || !strings.Contains(sum, "150=5(Replaced)") {
			t.Fatal(sum)
		}
		out, sum = c.call("order_timeline", map[string]any{"session_id": "emu42", "cl_ord_id": "last"})
		var types []string
		for _, s := range list(out, "steps") {
			sm := s.(map[string]any)
			types = append(types, sm["msg_type"].(string))
		}
		if strings.Join(types, " ") != "D 8 G 8" || len(list(out, "cl_ord_ids")) != 2 {
			t.Fatalf("chain %v", types)
		}
		g := list(out, "steps")[2].(map[string]any)
		if g["order_qty"] != "800" || g["price"] != "10.50" || g["orig_cl_ord_id"] != str(out, "seed") {
			t.Fatalf("G %v", g)
		}
		allChecksPass(t, list(out, "checks"))
		c.call("cancel_order", map[string]any{"session_id": "emu42", "cl_ord_id": "last"})
		out, sum = c.call("list_orders", map[string]any{"session_id": "emu42", "status": "open"})
		if len(list(out, "orders")) != 0 {
			t.Fatal(sum)
		}
		_, sum = c.call("disconnect_session", map[string]any{"session_id": "emu42"})
		if !strings.Contains(sum, "logged out cleanly") {
			t.Fatal(sum)
		}
	})
	recordVerdict("%-44s %s", "A4-1..3 MCP stdio orders + emulator tools", "PASS")
}

// ------------------------------------------------------------ 4

// A4 interop 4: a cert section through the tools matches the CLI; then
// attestation of a manual case with and without user_confirmed.
func TestMCPCertRunMatchesCLI(t *testing.T) {
	e := startEmulator(t)
	dir, _ := mcpDir(t, e)
	c := stdioClient(t, dir)
	out, sum := c.call("start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42", "section": "ORD"})
	runID := str(out, "run_id")
	if runID == "" || str(out, "state") != "running" || get(out, "total") != float64(9) {
		t.Fatal(sum)
	}
	// The session is the run's while it runs.
	errOut, _ := c.callErr("send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL", "qty": 1, "side": "buy", "ord_type": "mkt"})
	if str(errOut, "error") != "session_busy" || !strings.Contains(str(errOut, "hint"), "cert_run_status") {
		t.Fatalf("%v", errOut)
	}
	// The MCP client goes away mid-run (Claude Desktop quits): the run goes on
	// in the service, and a new client picks it up.
	c.cs.Close()
	c = stdioClient(t, dir)
	if st, _ := c.call("cert_run_status", map[string]any{"run_id": runID}); str(st, "run", "state") != "running" && get(st, "run", "done") == float64(0) {
		t.Fatalf("run did not survive the client: %v", st)
	}
	st := c.waitRun(runID, 5*time.Minute)
	if str(st, "run", "state") != "finished" {
		t.Fatalf("%v", st)
	}
	res, sum := c.call("cert_run_results", map[string]any{"run_id": runID, "only": "all"})
	mcpCounts := counts(res, "counts")
	if res["exit_code"] != float64(0) || mcpCounts["FAIL"] != 0 || mcpCounts["ERROR"] != 0 || mcpCounts["PASS"] < 5 || len(list(res, "cases")) != 9 {
		t.Fatal(sum)
	}
	if !exists(filepath.Join(dir, str(res, "results_json"))) {
		t.Fatalf("no results.json at %s", str(res, "results_json"))
	}

	// The same section from the CLI: the same counts.
	run, cli, _ := runCert(t, dir, 5*time.Minute, "--suite", "certs/order_entry_fix42.yaml", "--target", "certs/targets/emulator.yaml", "--session", "emu42", "--section", "ORD")
	if run.code != 0 || fmt.Sprint(cli.Counts) != fmt.Sprint(mcpCounts) {
		t.Fatalf("CLI exit %d counts %v, MCP counts %v", run.code, cli.Counts, mcpCounts)
	}
	for i, cs := range list(res, "cases") {
		if m := cs.(map[string]any); m["id"] != cli.Cases[i].ID || m["status"] != cli.Cases[i].Status {
			t.Fatalf("case %v %v vs CLI %s %s", m["id"], m["status"], cli.Cases[i].ID, cli.Cases[i].Status)
		}
	}

	// Attestation: a run with a manual case.
	out, _ = c.call("start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42", "cases": []string{"1.1", "4.1"}})
	run2 := str(out, "run_id")
	c.waitRun(run2, 3*time.Minute)
	res, sum = c.call("cert_run_results", map[string]any{"run_id": run2})
	if res["exit_code"] != float64(7) || len(list(res, "attestable_unattested")) != 1 || list(res, "attestable_unattested")[0] != "1.1" {
		t.Fatal(sum)
	}
	args := map[string]any{"run_id": run2, "case_id": "1.1", "status": "pass", "by": "interop test", "note": "cert host and docs received"}
	errOut, sum = c.callErr("attest_cert_case", args)
	if str(errOut, "error") != "confirmation_required" || !strings.Contains(str(errOut, "hint"), "ask them to confirm") {
		t.Fatal(sum)
	}
	if res, _ := c.call("cert_run_results", map[string]any{"run_id": run2}); res["exit_code"] != float64(7) {
		t.Fatal("a rejected attestation changed the run")
	}
	errOut, _ = c.callErr("attest_cert_case", map[string]any{"run_id": run2, "case_id": "4.1", "status": "pass", "by": "x", "note": "y", "user_confirmed": true})
	if str(errOut, "error") != "not_attestable" {
		t.Fatalf("%v", errOut)
	}
	args["user_confirmed"] = true
	out, sum = c.call("attest_cert_case", args)
	if str(out, "case_status") != "PASS" || out["exit_code"] != float64(0) {
		t.Fatal(sum)
	}
	var disk certResults
	json.Unmarshal([]byte(mustRead(t, filepath.Join(dir, "data", "certs", run2, "results.json"))), &disk)
	if disk.Exit != 0 || disk.Cases[0].Status != "PASS" || disk.Cases[0].Attestation == nil || disk.Cases[0].Attestation.By != "interop test" {
		t.Fatalf("%+v", disk)
	}
	recordVerdict("%-44s %s", "A4-4 cert via MCP == CLI; attestation", cert4Line(mcpCounts))
}

func cert4Line(c map[string]int) string { return "ORD " + countsLine(c) + "; attest exit 7 -> 0" }

// ------------------------------------------------------------ 5

// A4 interop 5: MCP over HTTP needs the bearer token.
func TestMCPOverHTTPNeedsToken(t *testing.T) {
	e := startEmulator(t)
	dir, port := mcpDir(t, e)
	env := func(token string) []string {
		var out []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "ORDERECHO_") {
				out = append(out, kv)
			}
		}
		if token != "" {
			out = append(out, "ORDERECHO_MCP_TOKEN="+token)
		}
		return out
	}
	// No token anywhere: refused up front.
	cmd := exec.Command(agentBin, "--config", "orderecho.yaml", "serve", "--mcp-http")
	cmd.Dir, cmd.Env = dir, env("")
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || !strings.Contains(string(out), "needs a bearer token") {
		t.Fatalf("%v %s", err, out)
	}
	const token = "interop-token-7f3a"
	cmd = exec.Command(agentBin, "--config", "orderecho.yaml", "serve", "--mcp-http")
	cmd.Dir, cmd.Env = dir, env(token)
	var logs lockedBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(20 * time.Second):
			cmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("serve output:\n%s", logs.String())
		}
	})
	waitFor(t, 15*time.Second, "service up", func() bool { h, err := serviceHealth(port); return err == nil && h.MCPHTTP })
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}`
	for _, auth := range []string{"", "Bearer nope"} {
		req, _ := http.NewRequest("POST", endpoint, strings.NewReader(init))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth %q: HTTP %d", auth, resp.StatusCode)
		}
		t.Logf("POST /mcp with auth %q -> %d", auth, resp.StatusCode)
	}
	hc := &http.Client{Transport: bearerRT{token}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "orderecho-interop-http", Version: "a4"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	c := &mcpClient{t: t, cs: cs, stderr: &logs}
	_, sum := c.call("list_sessions", nil)
	if !strings.Contains(sum, "5 session(s)") {
		t.Fatal(sum)
	}
	c.call("connect_session", map[string]any{"session_id": "emu44"})
	res, sum := c.call("send_order", map[string]any{"session_id": "emu44", "symbol": "AAPL", "qty": 100, "side": "buy", "ord_type": "mkt", "wait_for": "terminal"})
	if str(res, "state") != "FILLED" || str(res, "checks", "verdict") != "PASS" {
		t.Fatal(sum)
	}
	c.call("order_timeline", map[string]any{"session_id": "emu44", "cl_ord_id": "last"})
	c.call("disconnect_session", map[string]any{"session_id": "emu44"})
	// Every call went into the engine log and the evidence.
	engine := strings.Join(readAll(globAll(filepath.Join(dir, "logs", "engine", "orderecho_*.log"))), "")
	if !strings.Contains(engine, "MCP  send_order ord_type=mkt qty=100 session_id=emu44 side=buy symbol=AAPL wait_for=terminal -> sent: BUY 100 AAPL MKT") {
		t.Fatalf("engine log lacks the MCP line")
	}
	ev := strings.Join(readAll(globAll(filepath.Join(dir, "data", "evidence", "service-*.jsonl"))), "")
	if !strings.Contains(ev, "tool call: client=mcp-http send_order") {
		t.Fatal("evidence lacks the tool call")
	}
	recordVerdict("%-44s %s", "A4-5 MCP over HTTP (bearer token)", "no token 401, bad token 401, token OK")
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func readAll(paths []string) []string {
	var out []string
	for _, p := range paths {
		data, _ := os.ReadFile(p)
		out = append(out, string(data))
	}
	return out
}

// ------------------------------------------------------------ 6

// A4 interop 6: the service dies mid-run -> the run is ERROR once a new
// service starts; finished runs stay readable. Also a graceful stop.
func TestMCPServiceRestartMidRun(t *testing.T) {
	e := startEmulator(t)
	dir, port := mcpDir(t, e)
	c := stdioClient(t, dir)
	out, _ := c.call("start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42", "cases": []string{"4.1"}})
	done := str(out, "run_id")
	c.waitRun(done, 3*time.Minute)

	// Killed hard mid-run (a crash): the next service marks it ERROR.
	out, _ = c.call("start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42"})
	killed := str(out, "run_id")
	waitFor(t, 2*time.Minute, "a few cases done", func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "data", "certs", killed, "run.json"))
		var info struct {
			Done int `json:"done"`
		}
		return err == nil && json.Unmarshal(data, &info) == nil && info.Done >= 3
	})
	oldPID := killService(t, port, syscall.SIGKILL)
	// The next call finds no service: orderecho mcp starts a new one.
	st, sum := c.call("cert_run_status", map[string]any{"run_id": killed})
	if str(st, "run", "state") != "error" || str(st, "run", "error") != "service restarted" || !strings.Contains(sum, "ERROR: service restarted") {
		t.Fatalf("%s", sum)
	}
	if h, _ := serviceHealth(port); h == nil || h.PID == oldPID {
		t.Fatal("no new service")
	}
	out, sum = c.call("cert_run_results", map[string]any{"run_id": done, "only": "all"})
	if out["exit_code"] != float64(0) || len(list(out, "cases")) != 1 || str(out, "state") != "finished" {
		t.Fatalf("finished run not readable after the restart: %s", sum)
	}
	// Sessions start disconnected after a restart.
	if _, sum := c.call("session_status", map[string]any{"session_id": "emu42"}); !strings.HasPrefix(sum, "emu42 DISCONNECTED") {
		t.Fatal(sum)
	}

	// A graceful stop (SIGTERM) mid-run: logs out, marks the run ERROR, writes what it has.
	out, _ = c.call("start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42"})
	stopped := str(out, "run_id")
	waitFor(t, 2*time.Minute, "a case done", func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "data", "certs", stopped, "run.json"))
		var info struct {
			Done int `json:"done"`
		}
		return err == nil && json.Unmarshal(data, &info) == nil && info.Done >= 2
	})
	killService(t, port, syscall.SIGTERM)
	st, sum = c.call("cert_run_status", map[string]any{"run_id": stopped})
	if str(st, "run", "state") != "error" || !strings.Contains(str(st, "run", "error"), "service stopped while the run was in progress") {
		t.Fatalf("%s", sum)
	}
	out, sum = c.call("cert_run_results", map[string]any{"run_id": stopped, "only": "pending"})
	if !strings.Contains(str(out, "run_error"), "service stopped") || out["exit_code"] != float64(8) {
		t.Fatalf("%s", sum)
	}
	notRun := 0
	for _, cs := range list(out, "cases") {
		if m := cs.(map[string]any); m["status"] == "NOT_RUN" && strings.Contains(m["reason"].(string), "service stopped") {
			notRun++
		}
	}
	if notRun < 10 {
		t.Fatalf("%d NOT_RUN", notRun)
	}
	// The session was logged out cleanly on the way down.
	fix := strings.Join(readAll(globAll(filepath.Join(dir, "logs", "fix", "emu42_*.log"))), "")
	if !strings.Contains(fix, "58=OrderEcho agent service stopping") {
		t.Fatal("no clean logout on SIGTERM")
	}
	recordVerdict("%-44s %s", "A4-6 service restart mid-run", "killed -> ERROR: service restarted; SIGTERM -> ERROR, logout; finished run readable")
}

// ------------------------------------------------------------ 7

// A4 interop 7: the scripted operator conversation. What an LLM would do
// for "connect emu44, buy 100 AAPL at market, then certify the order-entry
// section", through MCP only, every step asserted.
func TestMCPScriptedOperatorConversation(t *testing.T) {
	e := startEmulator(t)
	dir, _ := mcpDir(t, e)
	c := stdioClient(t, dir)
	var transcript bytes.Buffer
	step := func(name string, args map[string]any) (map[string]any, string) {
		out, sum := c.call(name, args)
		a, _ := json.Marshal(args)
		fmt.Fprintf(&transcript, "-> %s %s\n<- %s\n", name, a, sum)
		return out, sum
	}

	// "connect emu44"
	out, sum := step("list_sessions", nil)
	var emu44 map[string]any
	for _, s := range list(out, "sessions") {
		if s.(map[string]any)["session_id"] == "emu44" {
			emu44 = s.(map[string]any)
		}
	}
	if emu44 == nil || emu44["fix_version"] != "FIX.4.4" || emu44["external"] != false || emu44["state"] != "DISCONNECTED" {
		t.Fatal(sum)
	}
	out, sum = step("connect_session", map[string]any{"session_id": "emu44"})
	if str(out, "state") != "ACTIVE" {
		t.Fatal(sum)
	}
	// "buy 100 AAPL at market"
	out, sum = step("send_order", map[string]any{"session_id": "emu44", "symbol": "AAPL", "side": "buy", "qty": 100, "ord_type": "mkt", "wait_for": "terminal"})
	if str(out, "state") != "FILLED" || str(out, "side") != "BUY" || str(out, "order_qty") != "100" || str(out, "checks", "verdict") != "PASS" {
		t.Fatal(sum)
	}
	allChecksPass(t, list(out, "checks", "results"))
	// "then certify the order-entry section": find the suite for FIX 4.4 and the target.
	out, sum = step("list_cert_suites", nil)
	suite := ""
	for _, s := range list(out, "suites") {
		sm := s.(map[string]any)
		if sm["fix_version"] == "FIX.4.4" && strings.HasPrefix(sm["suite"].(string), "order-entry") {
			suite = sm["suite"].(string)
			hasORD := false
			for _, sec := range sm["sections"].([]any) {
				if sec.(map[string]any)["id"] == "ORD" && sec.(map[string]any)["name"] == "Order Entry" {
					hasORD = true
				}
			}
			if !hasORD {
				t.Fatalf("no Order Entry section: %v", sm["sections"])
			}
		}
	}
	if suite != "order-entry-fix44" {
		t.Fatal(sum)
	}
	out, sum = step("list_cert_targets", nil)
	if !strings.Contains(sum, "emulator (orderecho-emulator, control API") {
		t.Fatal(sum)
	}
	out, sum = step("start_cert_run", map[string]any{"suite": suite, "target": "emulator", "session_id": "emu44", "section": "ORD"})
	runID := str(out, "run_id")
	if runID == "" || out["took_over_session"] != true || !strings.Contains(sum, "the run logged it out") {
		t.Fatal(sum)
	}
	st := c.waitRun(runID, 5*time.Minute)
	fmt.Fprintf(&transcript, "-> cert_run_status {\"run_id\":%q} (polled until finished)\n<- state %s, %v/%v done\n", runID, str(st, "run", "state"), get(st, "run", "done"), get(st, "run", "total"))
	if str(st, "run", "state") != "finished" || get(st, "run", "done") != get(st, "run", "total") {
		t.Fatalf("%v", st)
	}
	out, sum = step("cert_run_results", map[string]any{"run_id": runID, "only": "all"})
	cn := counts(out, "counts")
	if out["exit_code"] != float64(0) || cn["FAIL"] != 0 || cn["ERROR"] != 0 || cn["PASS"] != 5 || cn["N/A"] != 4 || str(out, "fix_version") != "FIX.4.4" {
		t.Fatal(sum)
	}
	for _, cs := range list(out, "cases") {
		m := cs.(map[string]any)
		if m["section"] != "ORD" || (m["status"] != "PASS" && m["status"] != "N/A") {
			t.Fatalf("%v", m)
		}
	}
	// The run leaves the session disconnected; the order bought before it is still on record.
	out, sum = step("session_status", map[string]any{"session_id": "emu44"})
	if str(out, "session", "state") != "DISCONNECTED" {
		t.Fatal(sum)
	}
	out, sum = step("list_orders", map[string]any{"session_id": "emu44"})
	if rows := list(out, "orders"); len(rows) != 1 || rows[0].(map[string]any)["state"] != "FILLED" {
		t.Fatal(sum)
	}
	t.Logf("scripted operator conversation:\n%s", transcript.String())
	if p := os.Getenv("ORDERECHO_TRANSCRIPT"); p != "" {
		os.WriteFile(p, transcript.Bytes(), 0o644)
	}
	recordVerdict("%-44s %s", "A4-7 scripted operator conversation", "emu44 FILLED PASS; ORD "+countsLine(cn)+" exit 0")
}
