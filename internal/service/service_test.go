package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/order"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
)

// No test here opens a socket: sessions are never connected, HTTP goes
// through httptest recorders.

const testConfig = `sessions:
  - { id: emu42, fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: ORDERECHO, host: 127.0.0.1, port: 9, heartbeat_sec: 30 }
  - { id: emu44, fix_version: FIX.4.4, sender_comp_id: AGENT, target_comp_id: ORDERECHO, host: localhost, port: 9, heartbeat_sec: 30 }
  - { id: venue, fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: BROKER, host: 10.20.30.40, port: 9, heartbeat_sec: 30 }
storage: { seqnum_dir: %[1]s/seq, msgstore_dir: %[1]s/store, evidence_dir: %[1]s/ev, certs_dir: %[1]s/certs }
logging: { log_dir: %[1]s/logs, console: false }
%[2]s
`

func newTestService(t *testing.T, extra string) *Service {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(fmt.Sprintf(testConfig, dir, extra)))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Path = filepath.Join(dir, "orderecho.yaml")
	s, err := New(Options{Config: cfg, Root: "../.."})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(0) })
	return s
}

const emulatorBlock = `emulator: { control_api: "http://127.0.0.1:1", sessions: { emu42: agent42, emu44: agent44 } }`

func invoke(t *testing.T, s *Service, client, tool string, args any) Response {
	t.Helper()
	raw, _ := json.Marshal(args)
	return s.Dispatch(context.Background(), client, tool, raw)
}

func mustErr(t *testing.T, r Response, code string, hintHas ...string) *APIError {
	t.Helper()
	if r.Error == nil {
		t.Fatalf("want error %s, got success %q", code, r.Summary)
	}
	if r.Error.Code != code {
		t.Fatalf("want %s, got %s: %s (hint %q)", code, r.Error.Code, r.Error.Detail, r.Error.Hint)
	}
	for _, h := range hintHas {
		if !strings.Contains(r.Error.Hint, h) {
			t.Fatalf("hint %q lacks %q", r.Error.Hint, h)
		}
	}
	return r.Error
}

// ------------------------------------------------------------ schemas

func TestToolSchemasValidate(t *testing.T) {
	want := []string{"list_sessions", "connect_session", "disconnect_session", "session_status", "send_order", "cancel_order",
		"replace_order", "list_orders", "order_timeline", "recent_messages", "list_cert_suites", "list_cert_targets",
		"start_cert_run", "cert_run_status", "cert_run_results", "attest_cert_case",
		"emulator_fill_order", "emulator_cancel_order", "emulator_hold_order", "emulator_inject_next"}
	if len(Catalog()) != len(want) {
		t.Fatalf("%d tools", len(Catalog()))
	}
	for i, tl := range Catalog() {
		if tl.Name != want[i] {
			t.Fatalf("tool %d is %s, want %s", i, tl.Name, want[i])
		}
		if tl.Schema.Type != "object" || tl.resolved == nil {
			t.Fatalf("%s: schema type %q", tl.Name, tl.Schema.Type)
		}
		if len(tl.Description) < 150 || tl.Title == "" {
			t.Errorf("%s: description too thin for an LLM reader", tl.Name)
		}
		for name, p := range tl.Schema.Properties {
			if p.Description == "" {
				t.Errorf("%s.%s has no description", tl.Name, name)
			}
		}
		// Unknown arguments are refused.
		if err := tl.resolved.Validate(map[string]any{"bogus_argument": 1}); err == nil {
			t.Errorf("%s accepts unknown arguments", tl.Name)
		}
		// The schema JSON is what MCP clients see.
		if _, err := json.Marshal(tl.Schema); err != nil {
			t.Fatal(err)
		}
	}
	so, _ := Lookup("send_order")
	valid := []map[string]any{
		{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": 100, "ord_type": "mkt"},
		{"session_id": "emu42", "symbol": "AAPL", "side": "sell", "qty": "100", "ord_type": "lmt", "price": "10.50", "tif": "day", "wait_for": "terminal", "timeout_sec": 5},
		{"session_id": "emu42", "symbol": "AAPL", "side": "short", "qty": 1, "ord_type": "lmt", "price": 10.5},
	}
	for _, v := range valid {
		if err := so.resolved.Validate(v); err != nil {
			t.Errorf("valid %v: %v", v, err)
		}
	}
	invalid := []map[string]any{
		{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "ord_type": "mkt"},                                        // no qty
		{"session_id": "emu42", "symbol": "AAPL", "side": "BUY!", "qty": 1, "ord_type": "mkt"},                             // side enum
		{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": 1, "ord_type": "stop"},                             // ord_type enum
		{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": 1, "ord_type": "mkt", "wait_for": "forever"},       // wait_for enum
		{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": true, "ord_type": "mkt"},                           // qty type
		{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": 1, "ord_type": "mkt", "timeout_sec": float64(900)}, // max
	}
	for _, v := range invalid {
		if err := so.resolved.Validate(v); err == nil {
			t.Errorf("invalid accepted: %v", v)
		}
	}
	at, _ := Lookup("attest_cert_case")
	if err := at.resolved.Validate(map[string]any{"run_id": "r", "case_id": "1.1", "status": "maybe", "by": "x", "note": "y"}); err == nil {
		t.Error("attest status enum")
	}
}

func TestArgumentValidationAndHints(t *testing.T) {
	s := newTestService(t, emulatorBlock)
	e := mustErr(t, invoke(t, s, ClientAPI, "send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL"}), "invalid_arguments",
		"send_order takes required session_id, symbol, side, qty, ord_type", "side is one of buy|sell|short", "ord_type is one of mkt|lmt")
	if e.Status != 400 {
		t.Fatal(e.Status)
	}
	mustErr(t, invoke(t, s, ClientAPI, "send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": 100, "ord_type": "mkt", "price": "1.00"}),
		"invalid_order", "mkt takes no price")
	mustErr(t, invoke(t, s, ClientAPI, "send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": "1.5", "ord_type": "mkt"}),
		"invalid_order", "qty is a positive whole number")
	mustErr(t, invoke(t, s, ClientAPI, "send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": 100, "ord_type": "lmt"}),
		"invalid_order", "lmt needs a positive price")
	mustErr(t, invoke(t, s, ClientAPI, "send_order", map[string]any{"session_id": "nope", "symbol": "AAPL", "side": "buy", "qty": 100, "ord_type": "mkt"}),
		"unknown_session", "emu42, emu44, venue")
	mustErr(t, invoke(t, s, ClientAPI, "send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": 100, "ord_type": "mkt"}),
		"not_connected", `connect_session {session_id: "emu42"}`)
	mustErr(t, invoke(t, s, ClientAPI, "replace_order", map[string]any{"session_id": "emu42", "cl_ord_id": "last"}), "invalid_arguments", "give qty")
	mustErr(t, invoke(t, s, ClientAPI, "no_such_tool", nil), "unknown_tool", "send_order")
	mustErr(t, invoke(t, s, ClientAPI, "start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu44"}),
		"version_mismatch", "order-entry-fix44")
	mustErr(t, invoke(t, s, ClientAPI, "start_cert_run", map[string]any{"suite": "nope", "target": "emulator", "session_id": "emu42"}),
		"unknown_suite", "order-entry-fix42, order-entry-fix44")
	mustErr(t, invoke(t, s, ClientAPI, "start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "nope", "session_id": "emu42"}),
		"unknown_target", "emulator, generic")
	mustErr(t, invoke(t, s, ClientAPI, "start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42", "section": "XYZ"}),
		"unknown_section", "ORD")
	mustErr(t, invoke(t, s, ClientAPI, "cert_run_status", map[string]any{"run_id": "20990101-000000.000"}), "unknown_run", "recent runs")
	mustErr(t, invoke(t, s, ClientAPI, "emulator_inject_next", map[string]any{"session": "venue", "set": map[string]string{"58": "x"}}),
		"unknown_session", "emu42 (agent42)")
	mustErr(t, invoke(t, s, ClientAPI, "recent_messages", map[string]any{"session_id": "emu42", "limit": 0}), "invalid_arguments")
	// A read-only call with no arguments at all is fine.
	if r := s.Dispatch(context.Background(), ClientAPI, "list_sessions", nil); r.Error != nil || !strings.Contains(r.Summary, "3 session(s)") {
		t.Fatalf("%+v", r)
	}
}

// ------------------------------------------------------------ last

// addConn gives the session a (never connected) connection whose order
// manager already knows some orders.
func addConn(t *testing.T, s *Service, id string, symbols ...string) *conn {
	t.Helper()
	st := s.sessions[id]
	sc := st.cfg
	a, err := agent.New(agent.Options{Config: s.cfg, Session: sc, Clock: clock.SystemClock{}, RunID: s.newRunID()})
	if err != nil {
		t.Fatal(err)
	}
	c := &conn{a: a, live: cert.NewLive(context.Background(), a, cert.NewHistory(clock.SystemClock{}))}
	for _, sym := range symbols {
		if _, _, err := a.Orders.NewOrder(order.Spec{Symbol: sym, Qty: "100", Side: "buy", OrdType: "mkt"}); err != nil {
			t.Fatal(err)
		}
	}
	st.mu.Lock()
	st.conns = append(st.conns, c)
	st.mu.Unlock()
	t.Cleanup(func() { a.Close() })
	return c
}

func TestLastResolution(t *testing.T) {
	s := newTestService(t, "")
	st := s.sessions["emu42"]
	if _, _, e := st.findOrder("last"); e == nil || e.Code != "no_orders" {
		t.Fatalf("%v", e)
	}
	c1 := addConn(t, s, "emu42", "AAPL", "MSFT")
	c2 := addConn(t, s, "emu42") // a later connection with no orders yet
	c, o, e := st.findOrder("last")
	if e != nil || c != c1 || o.Symbol != "MSFT" {
		t.Fatalf("last on an order-less newest connection: %v %v", o, e)
	}
	addOrder := func(c *conn, sym string) *order.Order {
		o, _, err := c.a.Orders.NewOrder(order.Spec{Symbol: sym, Qty: "5", Side: "sell", OrdType: "mkt"})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	ibm := addOrder(c2, "IBM")
	if c, o, _ := st.findOrder("LAST"); c != c2 || o != ibm {
		t.Fatal("last is the newest connection's latest order")
	}
	// A ClOrdID of an older connection still resolves (newest first).
	first := c1.a.Orders.Orders()[0]
	if c, o, _ := st.findOrder(first.Root); c != c1 || o != first {
		t.Fatal("ClOrdID lookup")
	}
	if _, _, e := st.findOrder("OE-nope-1"); e == nil || e.Code != "unknown_order" || !strings.Contains(e.Hint, "list_orders") {
		t.Fatalf("%v", e)
	}
	// The tools resolve "last" the same way.
	r := invoke(t, s, ClientAPI, "order_timeline", map[string]any{"session_id": "emu42", "cl_ord_id": "last"})
	if r.Error != nil || !strings.Contains(r.Summary, "order "+ibm.Root) {
		t.Fatalf("%+v %v", r.Summary, r.Error)
	}
	r = invoke(t, s, ClientAPI, "order_timeline", map[string]any{"session_id": "emu42"}) // neither: last
	if r.Error != nil || !strings.Contains(r.Summary, ibm.Root) {
		t.Fatalf("%+v", r)
	}
	r = invoke(t, s, ClientAPI, "list_orders", map[string]any{"session_id": "emu42", "status": "open"})
	rows := r.Result.(map[string]any)["orders"].([]OrderRow)
	if r.Error != nil || len(rows) != 3 || rows[2].ClOrdID != ibm.Root || !rows[2].Current || rows[0].Current {
		t.Fatalf("%+v", rows)
	}
	if r := invoke(t, s, ClientAPI, "list_orders", map[string]any{"session_id": "emu42", "status": "FILLED"}); len(r.Result.(map[string]any)["orders"].([]OrderRow)) != 0 {
		t.Fatal("state filter")
	}
}

// ------------------------------------------------------------ safety

func TestExternalSessionNeedsConfirmation(t *testing.T) {
	s := newTestService(t, "")
	r := invoke(t, s, ClientAPI, "list_sessions", nil)
	for _, row := range r.Result.(map[string]any)["sessions"].([]SessionInfo) {
		if row.External != (row.SessionID == "venue") {
			t.Fatalf("%s external=%v", row.SessionID, row.External)
		}
	}
	if !strings.Contains(r.Summary, "venue (FIX.4.2 AGENT -> BROKER) DISCONNECTED [external]") {
		t.Fatal(r.Summary)
	}
	order := map[string]any{"session_id": "venue", "symbol": "AAPL", "side": "buy", "qty": 100, "ord_type": "mkt"}
	e := mustErr(t, invoke(t, s, ClientMCPStdio, "send_order", order), "confirmation_required", "ask the human", "confirm_external: true")
	if e.Status != 403 || !strings.Contains(e.Detail, "10.20.30.40") {
		t.Fatal(e.Detail)
	}
	mustErr(t, invoke(t, s, ClientAPI, "cancel_order", map[string]any{"session_id": "venue", "cl_ord_id": "last"}), "confirmation_required", "ask the human")
	mustErr(t, invoke(t, s, ClientAPI, "replace_order", map[string]any{"session_id": "venue", "cl_ord_id": "last", "qty": 5}), "confirmation_required", "ask the human")
	mustErr(t, invoke(t, s, ClientAPI, "start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "generic", "session_id": "venue"}),
		"confirmation_required", "ask the human")
	// Confirmed, the call goes on (and stops at the next gate: not connected).
	order["confirm_external"] = true
	mustErr(t, invoke(t, s, ClientAPI, "send_order", order), "not_connected")
	// Loopback names are not external.
	for host, ext := range map[string]bool{"127.0.0.1": false, "localhost": false, "::1": false, "127.0.0.2": false, "10.0.0.1": true, "fix.example.com": true} {
		if (config.Session{Host: host}).External() != ext {
			t.Errorf("%s external=%v", host, !ext)
		}
	}
}

func TestAttestationNeedsUserConfirmed(t *testing.T) {
	s := newTestService(t, "")
	// A finished run on disk, as the runner writes it.
	runID := "20260930-120000.000"
	dir := filepath.Join(s.certsDir(), runID)
	res := &cert.RunResult{Suite: "t", RunID: runID, Session: "emu42", RequiredInSuite: []string{"m1", "9.1", "a1"}, Cases: []*cert.CaseResult{
		{ID: "m1", Mode: cert.ModeManual, Required: true, Status: cert.StatusPending, Attestable: true},
		{ID: "9.1", Mode: cert.ModeAuto, Required: true, Review: cert.ReviewRequiredCases},
		{ID: "a1", Mode: cert.ModeAuto, Required: true, Status: cert.StatusPass},
	}}
	cert.Finalize(res)
	res.Exit = cert.ExitCode(res, 0)
	if err := cert.WriteResults(dir, res); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"run_id": runID, "case_id": "m1", "status": "pass", "by": "Dan", "note": "confirmed"}
	mustErr(t, invoke(t, s, ClientMCPStdio, "attest_cert_case", args), "confirmation_required", "do not attest on your own", "user_confirmed: true")
	args["user_confirmed"] = false
	mustErr(t, invoke(t, s, ClientMCPStdio, "attest_cert_case", args), "confirmation_required")
	args["user_confirmed"] = true
	args["case_id"] = "a1"
	mustErr(t, invoke(t, s, ClientMCPStdio, "attest_cert_case", args), "not_attestable", "attestable cases in this run: m1")
	args["case_id"] = "m1"
	r := invoke(t, s, ClientMCPStdio, "attest_cert_case", args)
	if r.Error != nil || !strings.Contains(r.Summary, "case m1 attested pass by Dan -> PASS") || !strings.Contains(r.Summary, "exit 0") {
		t.Fatalf("%+v %v", r.Summary, r.Error)
	}
	// Recomputed and rewritten: results.json, summary, the audit trail.
	data, _ := os.ReadFile(filepath.Join(dir, "results.json"))
	var back cert.RunResult
	json.Unmarshal(data, &back)
	if back.Exit != 0 || back.Cases[1].Status != cert.StatusPass || back.Cases[0].Attestation.By != "Dan" {
		t.Fatalf("%+v", back.Cases[1])
	}
	if trail, _ := os.ReadFile(filepath.Join(dir, "attestations.jsonl")); !strings.Contains(string(trail), `"via":"mcp-stdio"`) {
		t.Fatalf("%s", trail)
	}
	// Results read back from disk (a CLI run or an earlier service's).
	rr := invoke(t, s, ClientAPI, "cert_run_results", map[string]any{"run_id": runID, "only": "all"})
	if rr.Error != nil || !strings.Contains(rr.Summary, "exit 0") {
		t.Fatalf("%+v", rr)
	}
}

func TestRestartMarksInFlightRunsError(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Parse([]byte(fmt.Sprintf(testConfig, dir, "")))
	runDir := filepath.Join(cfg.Storage.CertsDir, "20260930-110000.000")
	writeJSON(filepath.Join(runDir, "run.json"), RunInfo{RunID: "20260930-110000.000", State: RunRunning, SessionID: "emu42", Dir: runDir})
	doneDir := filepath.Join(cfg.Storage.CertsDir, "20260930-100000.000")
	writeJSON(filepath.Join(doneDir, "run.json"), RunInfo{RunID: "20260930-100000.000", State: RunFinished, SessionID: "emu42", Dir: doneDir})
	s, err := New(Options{Config: cfg, Root: "../.."})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(0)
	r := invoke(t, s, ClientAPI, "cert_run_status", map[string]any{"run_id": "20260930-110000.000"})
	if r.Error != nil || !strings.Contains(r.Summary, "error") || !strings.Contains(r.Summary, "ERROR: service restarted") {
		t.Fatalf("%+v", r)
	}
	r = invoke(t, s, ClientAPI, "cert_run_status", map[string]any{"run_id": "20260930-100000.000"})
	if r.Error != nil || !strings.Contains(r.Summary, "finished") {
		t.Fatalf("%+v", r)
	}
}

// ------------------------------------------------------------ config gates

func TestConfigGatesTools(t *testing.T) {
	names := func(s *Service) string {
		var out []string
		for _, t := range s.EnabledTools() {
			out = append(out, t.Name)
		}
		return strings.Join(out, ",")
	}
	s := newTestService(t, emulatorBlock)
	if len(s.EnabledTools()) != 20 {
		t.Fatalf("all on: %s", names(s))
	}
	s = newTestService(t, "") // no control API: no emulator tools
	if strings.Contains(names(s), "emulator_") || len(s.EnabledTools()) != 16 {
		t.Fatalf("no control api: %s", names(s))
	}
	s = newTestService(t, emulatorBlock+"\nmcp: { allow_orders: false, allow_emulator_tools: false }")
	got := names(s)
	for _, gone := range []string{"send_order", "cancel_order", "replace_order", "start_cert_run", "emulator_"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%s still registered: %s", gone, got)
		}
	}
	// An MCP client calling a disabled tool anyway is refused, with why.
	mustErr(t, invoke(t, s, ClientMCPStdio, "send_order", map[string]any{"session_id": "emu42", "symbol": "A", "side": "buy", "qty": 1, "ord_type": "mkt"}),
		"tool_disabled", "mcp.allow_orders is false")
	mustErr(t, invoke(t, s, ClientMCPHTTP, "emulator_hold_order", map[string]any{"order_id": "X"}), "tool_disabled", "mcp.allow_emulator_tools")
}

// ------------------------------------------------------------ HTTP

func TestHTTPAPIShapesAndGuard(t *testing.T) {
	s := newTestService(t, "")
	h := s.Handler(HandlerOptions{})
	do := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:8190"+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	w := do("POST", "/api/v1/list_sessions", "{}", nil)
	var ok struct {
		Result  map[string]any `json:"result"`
		Summary string         `json:"summary"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ok) != nil || ok.Summary == "" || ok.Result["sessions"] == nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = do("POST", "/api/v1/send_order", `{"session_id":"emu42"}`, nil)
	var e APIError
	if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &e) != nil || e.Code != "invalid_arguments" || e.Hint == "" || e.Detail == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"error":`) || !strings.Contains(w.Body.String(), `"hint":`) {
		t.Fatal(w.Body.String())
	}
	if w = do("GET", "/api/v1/health", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":"0.4.0"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = do("GET", "/api/v1/tools", "", nil); w.Code != 200 || strings.Count(w.Body.String(), `"name"`) != 20 {
		t.Fatalf("%s", w.Body)
	}
	// Not JSON: refused (a browser form post cannot drive the agent).
	req := httptest.NewRequest("POST", "http://127.0.0.1:8190/api/v1/list_sessions", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnsupportedMediaType {
		t.Fatal(rw.Code)
	}
	// DNS rebinding and cross-origin pages are refused.
	req = httptest.NewRequest("POST", "http://evil.example:8190/api/v1/list_sessions", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != 403 || !strings.Contains(rw.Body.String(), "forbidden_host") {
		t.Fatal(rw.Code, rw.Body)
	}
	if w = do("POST", "/api/v1/list_sessions", "{}", map[string]string{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = do("POST", "/api/v1/list_sessions", "{}", map[string]string{"Origin": "http://localhost:3000"}); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestEveryCallIsLogged(t *testing.T) {
	s := newTestService(t, "")
	invoke(t, s, ClientMCPStdio, "list_sessions", nil)
	invoke(t, s, ClientAPI, "session_status", map[string]any{"session_id": "nope"})
	logPath := s.Log.Path()
	evPath := s.EvidencePath()
	s.Shutdown(0)
	engine, _ := os.ReadFile(logPath)
	if !strings.Contains(string(engine), "MCP  list_sessions {} -> 3 session(s)") ||
		!strings.Contains(string(engine), "API  session_status session_id=nope -> ERROR unknown_session") {
		t.Fatalf("%s", engine)
	}
	ev, _ := os.ReadFile(evPath)
	if !strings.Contains(string(ev), `"detail":"tool call: client=mcp-stdio list_sessions {} -> 3 session(s)`) ||
		!strings.Contains(string(ev), `"session":"nope"`) {
		t.Fatalf("%s", ev)
	}
}
