package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/service"
)

const cfgText = `sessions:
  - { id: emu42, fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: ORDERECHO, host: 127.0.0.1, port: 9, heartbeat_sec: 30 }
storage: { seqnum_dir: %[1]s/seq, msgstore_dir: %[1]s/store, evidence_dir: %[1]s/ev, certs_dir: %[1]s/certs }
logging: { log_dir: %[1]s/logs, console: false }
emulator: { control_api: "http://127.0.0.1:1", sessions: { emu42: agent42 } }
%[2]s
`

func newService(t *testing.T, extra string) *service.Service {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(fmt.Sprintf(cfgText, dir, extra)))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Path = filepath.Join(dir, "orderecho.yaml")
	s, err := service.New(service.Options{Config: cfg, Root: "../.."})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(0) })
	return s
}

func connect(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func text(r *mcp.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestToolsOverMCPWithAnnotations(t *testing.T) {
	s := newService(t, "")
	cs := connect(t, ForService(s, service.ClientMCPHTTP))
	ctx := context.Background()
	if ins := cs.InitializeResult().Instructions; !strings.Contains(ins, "Ask the human before attest_cert_case") {
		t.Fatalf("instructions: %q", ins)
	}
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 21 { // A5: + cert_run_report
		t.Fatalf("%d tools", len(list.Tools))
	}
	readOnly := map[string]bool{"list_sessions": true, "session_status": true, "list_orders": true, "order_timeline": true,
		"recent_messages": true, "list_cert_suites": true, "list_cert_targets": true, "cert_run_status": true, "cert_run_results": true,
		"cert_run_report": true}
	sideEffects := []string{"send_order", "cancel_order", "replace_order", "attest_cert_case", "emulator_fill_order",
		"emulator_cancel_order", "emulator_hold_order", "emulator_inject_next", "start_cert_run", "connect_session", "disconnect_session"}
	for _, tl := range list.Tools {
		a := tl.Annotations
		if a == nil || a.Title == "" {
			t.Fatalf("%s: no annotations", tl.Name)
		}
		if a.ReadOnlyHint != readOnly[tl.Name] {
			t.Errorf("%s readOnlyHint=%v", tl.Name, a.ReadOnlyHint)
		}
		if !a.ReadOnlyHint && a.DestructiveHint == nil {
			t.Errorf("%s: side-effect tool without destructiveHint", tl.Name)
		}
		schema, _ := json.Marshal(tl.InputSchema)
		if !strings.Contains(string(schema), `"type":"object"`) {
			t.Errorf("%s schema %s", tl.Name, schema)
		}
		if strings.HasPrefix(tl.Name, "emulator_") && !strings.Contains(tl.Description, "EMULATOR") {
			t.Errorf("%s does not say it acts on the emulator", tl.Name)
		}
	}
	for _, n := range sideEffects {
		if readOnly[n] {
			t.Fatal(n)
		}
	}
	byName := map[string]*mcp.Tool{}
	for _, tl := range list.Tools {
		byName[tl.Name] = tl
	}
	if !strings.Contains(byName["attest_cert_case"].Description, "ALWAYS ask the human first") ||
		!strings.Contains(byName["send_order"].Description, "confirm_external: true only after they approve") {
		t.Fatal("safety wording missing from descriptions")
	}
	if !*byName["cancel_order"].Annotations.DestructiveHint || *byName["send_order"].Annotations.DestructiveHint {
		t.Fatal("destructive hints")
	}

	// A call: structured content and a plain-text summary.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_sessions", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("%v %v", err, res)
	}
	if !strings.HasPrefix(text(res), "1 session(s): emu42") {
		t.Fatalf("%q", text(res))
	}
	sc, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(sc), `"session_id":"emu42"`) {
		t.Fatalf("%s", sc)
	}
	// An error: IsError with the hint, and {error, detail, hint} structured.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "attest_cert_case", Arguments: map[string]any{
		"run_id": "x", "case_id": "1.1", "status": "pass", "by": "me", "note": "n"}})
	if err != nil || !res.IsError || !strings.Contains(text(res), "ERROR confirmation_required") || !strings.Contains(text(res), "Hint: do not attest on your own") {
		t.Fatalf("%v %q", err, text(res))
	}
	sc, _ = json.Marshal(res.StructuredContent)
	if !strings.Contains(string(sc), `"error":"confirmation_required"`) || !strings.Contains(string(sc), `"hint":`) {
		t.Fatalf("%s", sc)
	}
	// Bad arguments come back as a tool error the model can read and fix.
	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "send_order", Arguments: map[string]any{"session_id": "emu42"}})
	if !res.IsError || !strings.Contains(text(res), "Hint: send_order takes required session_id, symbol, side, qty, ord_type") {
		t.Fatalf("%q", text(res))
	}
}

func TestConfigSwitchesToolsOff(t *testing.T) {
	s := newService(t, "mcp: { allow_orders: false, allow_emulator_tools: false }")
	cs := connect(t, ForService(s, service.ClientMCPHTTP))
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Tools {
		if tl.Name == "send_order" || tl.Name == "cancel_order" || tl.Name == "replace_order" || tl.Name == "start_cert_run" || strings.HasPrefix(tl.Name, "emulator_") {
			t.Fatalf("%s registered", tl.Name)
		}
	}
	if len(list.Tools) != 13 { // A5: + cert_run_report
		t.Fatalf("%d tools", len(list.Tools))
	}
}

// The stdio server forwards to the service's HTTP API: same results.
func TestForwardingOverTheHTTPAPI(t *testing.T) {
	s := newService(t, "")
	api := httptest.NewServer(s.Handler(service.HandlerOptions{}))
	defer api.Close()
	client := service.NewClient(api.URL, service.ClientMCPStdio)
	cs := connect(t, New(s.EnabledTools(), client))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "session_status", Arguments: map[string]any{"session_id": "emu42"}})
	if err != nil || res.IsError || !strings.HasPrefix(text(res), "emu42 DISCONNECTED") {
		t.Fatalf("%v %q", err, text(res))
	}
	res, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "session_status", Arguments: map[string]any{"session_id": "zz"}})
	if !res.IsError || !strings.Contains(text(res), "ERROR unknown_session") || !strings.Contains(text(res), "Hint: configured sessions: emu42") {
		t.Fatalf("%q", text(res))
	}
	// A service that is gone: a clear error telling the model what to do.
	dead := service.NewClient("http://127.0.0.1:1", service.ClientMCPStdio)
	_, _, aerr := dead.Call(context.Background(), "list_sessions", nil)
	if aerr == nil || aerr.Code != "service_unreachable" || !strings.Contains(aerr.Hint, "orderecho serve") {
		t.Fatalf("%+v", aerr)
	}
}

func TestHTTPTransportRequiresBearerToken(t *testing.T) {
	s := newService(t, "")
	h := s.Handler(service.HandlerOptions{MCP: HTTPHandler(s, "s3cret-token")})
	srv := httptest.NewServer(h)
	defer srv.Close()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	post := func(auth string) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(init))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	for _, auth := range []string{"", "Bearer wrong", "Basic czNjcmV0LXRva2Vu", "Bearer s3cret-toke"} {
		if resp := post(auth); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth %q: %d", auth, resp.StatusCode)
		}
	}
	if resp := post("Bearer s3cret-token"); resp.StatusCode != http.StatusOK {
		t.Fatalf("with token: %d", resp.StatusCode)
	}
	// A real client with the token works end to end.
	hc := &http.Client{Transport: bearer{"s3cret-token"}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_cert_suites", Arguments: map[string]any{}})
	if err != nil || res.IsError || !strings.Contains(text(res), "order-entry-fix42") {
		t.Fatalf("%v %q", err, text(res))
	}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
