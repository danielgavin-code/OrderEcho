// Package service is the long-running OrderEcho agent service (A4): it owns
// every configured FIX session (connected on demand, several at once), their
// order managers, the certification runs and their attestations. Its JSON
// HTTP API and the MCP server are thin front doors onto Dispatch, so a tool
// call is the same call whichever door it came through.
//
// The principle is unchanged from the CLI: the caller (a human or an LLM)
// decides what to do; the agent's code decides what happened and whether
// it passed.
package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/evidence"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/fixview"
	"github.com/danielgavin-code/OrderEcho/internal/logs"
	"github.com/danielgavin-code/OrderEcho/internal/order"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

// Clients, as Dispatch is told who called.
const (
	ClientAPI      = "api"
	ClientMCPStdio = "mcp-stdio"
	ClientMCPHTTP  = "mcp-http"
	ClientGUI      = "gui"
)

// IsMCP reports whether client is one of the MCP front doors.
func IsMCP(client string) bool { return strings.HasPrefix(client, "mcp") }

// APIError is every refused call: what went wrong and what to do next.
type APIError struct {
	Code   string `json:"error"`
	Detail string `json:"detail"`
	Hint   string `json:"hint"`
	Status int    `json:"-"` // HTTP status
}

func (e *APIError) Error() string { return e.Code + ": " + e.Detail }

func apiErr(status int, code, hint, format string, args ...any) *APIError {
	return &APIError{Code: code, Detail: fmt.Sprintf(format, args...), Hint: hint, Status: status}
}

// Response is one tool call's outcome: a result object and a short
// plain-text summary, or an error.
type Response struct {
	Result  any       `json:"result,omitempty"`
	Summary string    `json:"summary,omitempty"`
	Error   *APIError `json:"-"`
}

type handlerFunc func(ctx context.Context, s *Service, call *call) (any, string, *APIError)

// call is one tool invocation being handled.
type call struct {
	tool   *Tool
	client string
	raw    json.RawMessage
}

// decode fills v (a pointer to the tool's input struct), keeping numbers as
// their literal text.
func (c *call) decode(v any) *APIError {
	dec := json.NewDecoder(bytes.NewReader(c.raw))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return apiErr(400, "invalid_arguments", "send a JSON object matching the tool's input schema", "%s: %v", c.tool.Name, err)
	}
	return nil
}

// Options configure a Service.
type Options struct {
	Config *config.Config
	// Root is where certs/ (suites and targets) are found; default ".".
	Root string
	// Console receives the service's engine lines (serve in a terminal).
	Console io.Writer
	// HTTP is the client for the emulator's control API.
	HTTP *http.Client
}

// Service owns sessions and runs.
type Service struct {
	cfg     *config.Config
	root    string
	Log     *logs.EngineLog
	ev      *evidence.Writer
	http    *http.Client
	ctx     context.Context
	cancel  context.CancelFunc
	started time.Time

	events *hub
	csrf   string // per-process CSRF token for state-changing requests (A5 §6)

	mu       sync.Mutex
	sessions map[string]*sessState
	order    []string
	runs     map[string]*runState
	lastID   string
	stopping bool
}

// New builds the service. Sessions start disconnected; runs found on disk
// that were in progress when a previous service died are marked ERROR.
func New(opt Options) (*Service, error) {
	cfg := opt.Config
	if opt.Root == "" {
		opt.Root = "."
	}
	if opt.HTTP == nil {
		opt.HTTP = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	}
	clk := clock.SystemClock{}
	level, _ := logs.ParseLevel(cfg.Logging.EngineLevel)
	s := &Service{cfg: cfg, root: opt.Root, http: opt.HTTP, started: time.Now(),
		sessions: map[string]*sessState{}, runs: map[string]*runState{}}
	s.Log = logs.NewEngineLog(cfg.Logging.LogDir, clk, level, opt.Console)
	s.ev = evidence.Open(cfg.Storage.EvidenceDir, "service-"+evidence.MakeRunID(time.Now()), clk)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.events = newHub()
	s.csrf = randomToken()
	for _, sc := range cfg.Sessions {
		s.sessions[sc.ID] = &sessState{cfg: sc, ring: newRing(ringSize)}
		s.order = append(s.order, sc.ID)
	}
	s.recoverRuns()
	go s.watchSessions(s.ctx)
	return s, nil
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// CSRFToken is the token the GUI page carries (and API clients fetch).
func (s *Service) CSRFToken() string { return s.csrf }

// Config is the service's configuration.
func (s *Service) Config() *config.Config { return s.cfg }

// EvidencePath is the service's own evidence file (tool calls, lifecycle).
func (s *Service) EvidencePath() string { return s.ev.Path }

// newRunID is a run id (YYYYMMDD-HHMMSS.mmm) never handed out before by
// this service, so evidence files and ClOrdIDs never collide.
func (s *Service) newRunID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := time.Now()
	id := evidence.MakeRunID(t)
	for id <= s.lastID {
		t = t.Add(time.Millisecond)
		id = evidence.MakeRunID(t)
	}
	s.lastID = id
	return id
}

// ------------------------------------------------------------ enabled tools

// EmulatorToolsAvailable: a session's counterparty has a control API.
func (s *Service) EmulatorToolsAvailable() bool {
	return s.cfg.Emulator.ControlAPI != "" && len(s.cfg.Emulator.Sessions) > 0
}

// Enabled reports whether a tool is offered to MCP clients under this
// config (mcp.allow_orders, mcp.allow_emulator_tools, a control API).
func (s *Service) Enabled(t *Tool) bool {
	switch t.Group {
	case GroupOrders:
		return s.cfg.MCP.AllowOrders
	case GroupEmulator:
		return s.cfg.MCP.AllowEmulatorTools && s.EmulatorToolsAvailable()
	case GroupAPIOnly:
		return false
	}
	return true
}

// EnabledTools lists the tools MCP clients get, in catalogue order.
func (s *Service) EnabledTools() []*Tool {
	var out []*Tool
	for _, t := range Catalog() {
		if s.Enabled(t) {
			out = append(out, t)
		}
	}
	return out
}

// ------------------------------------------------------------ dispatch

// Dispatch validates and runs one tool call. Every call is logged in the
// engine log ("MCP  <tool> <args> -> <result>") and as an evidence event.
func (s *Service) Dispatch(ctx context.Context, client, name string, raw json.RawMessage) Response {
	resp := s.dispatch(ctx, client, name, raw)
	prefix := "API "
	if IsMCP(client) {
		prefix = "MCP "
	} else if client == ClientGUI {
		prefix = "GUI "
	}
	outcome := resp.Summary
	if resp.Error != nil {
		outcome = "ERROR " + resp.Error.Code + ": " + resp.Error.Detail
	}
	line := fmt.Sprintf("%s %s %s -> %s", prefix, name, argsSummary(raw), oneLine(outcome, 400))
	if resp.Error != nil {
		s.Log.Warning("engine", line)
	} else {
		s.Log.Info("engine", line)
	}
	sess := sessionOf(raw)
	if sess == "" {
		sess = "service"
	}
	s.ev.Event(sess, "tool call", fmt.Sprintf("client=%s %s %s -> %s", client, name, argsSummary(raw), oneLine(outcome, 1000)), false)
	return resp
}

func (s *Service) dispatch(ctx context.Context, client, name string, raw json.RawMessage) Response {
	t, ok := Lookup(name)
	if !ok {
		return Response{Error: apiErr(404, "unknown_tool", "tools: "+strings.Join(Names(), ", "), "no tool %q", name)}
	}
	if IsMCP(client) && !s.Enabled(t) {
		why := "mcp.allow_orders is false in the agent config"
		if t.Group == GroupAPIOnly {
			why = "it is an HTTP API operation (the GUI's), not an MCP tool"
		}
		if t.Group == GroupEmulator {
			why = "emulator tools need emulator.control_api in the agent config and mcp.allow_emulator_tools: true"
		}
		return Response{Error: apiErr(403, "tool_disabled", "this tool is switched off for MCP clients: "+why, "%s is not enabled", name)}
	}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = json.RawMessage("{}")
	}
	var inst any
	if err := json.Unmarshal(raw, &inst); err != nil {
		return Response{Error: apiErr(400, "invalid_arguments", "send a JSON object matching the tool's input schema", "%s: arguments are not JSON: %v", name, err)}
	}
	if err := t.resolved.Validate(inst); err != nil {
		return Response{Error: apiErr(400, "invalid_arguments", schemaHint(t), "%s: %v", name, err)}
	}
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping && !t.ReadOnly {
		return Response{Error: apiErr(503, "service_stopping", "the agent service is shutting down; start it again (orderecho serve) and retry", "service is stopping")}
	}
	result, summary, aerr := t.handle(ctx, s, &call{tool: t, client: client, raw: raw})
	if aerr != nil {
		if aerr.Status == 0 {
			aerr.Status = 400
		}
		return Response{Error: aerr}
	}
	return Response{Result: result, Summary: summary}
}

// schemaHint names the tool's required and optional arguments.
func schemaHint(t *Tool) string {
	var opt []string
	req := map[string]bool{}
	for _, r := range t.Schema.Required {
		req[r] = true
	}
	for name := range t.Schema.Properties {
		if !req[name] {
			opt = append(opt, name)
		}
	}
	sort.Strings(opt)
	h := fmt.Sprintf("%s takes", t.Name)
	if len(t.Schema.Required) > 0 {
		h += " required " + strings.Join(t.Schema.Required, ", ")
	} else {
		h += " no required arguments"
	}
	if len(opt) > 0 {
		h += "; optional " + strings.Join(opt, ", ")
	}
	names := make([]string, 0, len(t.Schema.Properties))
	for name := range t.Schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := t.Schema.Properties[name]
		if len(p.Enum) > 0 {
			var vals []string
			for _, v := range p.Enum {
				vals = append(vals, fmt.Sprint(v))
			}
			h += fmt.Sprintf("; %s is one of %s", name, strings.Join(vals, "|"))
		}
	}
	return h
}

func sessionOf(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"session_id", "session"} {
		if v, ok := m[k].(string); ok {
			return v
		}
	}
	return ""
}

// argsSummary renders arguments as sorted k=v pairs.
func argsSummary(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := m[k]
		var txt string
		switch x := v.(type) {
		case string:
			txt = x
		default:
			b, _ := json.Marshal(x)
			txt = string(b)
		}
		parts = append(parts, k+"="+oneLine(txt, 80))
	}
	return strings.Join(parts, " ")
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// ------------------------------------------------------------ sessions

const ringSize = 2000

// sessState is one configured session and its connections.
type sessState struct {
	cfg config.Session
	// op serializes what changes the connection: connect, disconnect and a
	// cert run claiming the session (never held while waiting on a run).
	op sync.Mutex
	mu sync.Mutex
	// conns: one per connect_session (a fresh agent each time, so seqnums
	// and the message store are re-read from disk); the last is current.
	// Earlier ones are closed but their orders stay listable.
	conns []*conn
	run   string // the cert run that owns the session, if any
	ring  *ring
}

// conn is one agent (one connect_session) of a session.
type conn struct {
	a         *agent.Agent
	live      *cert.Live
	since     time.Time
	closedF   atomic.Bool
	closeOnce sync.Once
	mu        sync.Mutex // lastIn/lastOut
	lastIn    time.Time
	lastOut   time.Time
}

func (c *conn) closed() bool    { return c.closedF.Load() }
func (c *conn) connected() bool { return !c.closed() && c.live.Connected() }

func (s *Service) session(id string) (*sessState, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[id]
	if !ok {
		return nil, apiErr(404, "unknown_session", "configured sessions: "+strings.Join(s.order, ", ")+" (call list_sessions)", "no session %q", id)
	}
	return st, nil
}

// current returns the session's latest connection (nil before the first).
func (st *sessState) current() *conn {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.conns) == 0 {
		return nil
	}
	return st.conns[len(st.conns)-1]
}

func (st *sessState) allConns() []*conn {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]*conn(nil), st.conns...)
}

func (st *sessState) busy() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.run
}

func busyErr(id, run string) *APIError {
	return apiErr(409, "session_busy", fmt.Sprintf("poll cert_run_status {run_id: %q} until it is finished; the run leaves the session disconnected, then connect_session again", run),
		"session %s is being used by cert run %s", id, run)
}

// connect opens a new connection (a fresh agent) and waits for its Logon.
func (s *Service) connect(st *sessState, reset bool) (*conn, *APIError) {
	st.op.Lock()
	defer st.op.Unlock()
	if c := st.current(); c != nil && c.connected() {
		return c, nil
	}
	if run := st.busy(); run != "" {
		return nil, busyErr(st.cfg.ID, run)
	}
	if c := st.current(); c != nil && !c.closed() {
		c.close()
	}
	sc := st.cfg
	hist := cert.NewHistory(clock.SystemClock{})
	c := &conn{since: time.Now()}
	var injected *codec.Message
	a, err := agent.New(agent.Options{Config: s.cfg, Session: sc, Clock: clock.SystemClock{}, RunID: s.newRunID(),
		OnWireInjected: func(m *codec.Message) { injected = m },
		OnWire: func(dir string, m *codec.Message) {
			hist.Add(dir, m)
			st.ring.add(dir, m)
			c.mu.Lock()
			if dir == "in" {
				c.lastIn = time.Now()
			} else {
				c.lastOut = time.Now()
			}
			c.mu.Unlock()
			s.publishMessage(sc, dir, m, m == injected)
		},
		OnEvidence: func(e session.Evidence) { s.publishEvidence(sc.ID, e) }})
	if err != nil {
		return nil, apiErr(500, "agent_error", "check the storage and log directories in the agent config", "cannot build the agent for %s: %v", sc.ID, err)
	}
	c.a = a
	c.live = cert.NewLive(s.ctx, a, hist)
	st.mu.Lock()
	st.conns = append(st.conns, c)
	st.mu.Unlock()
	a.EngineLog.Info("engine", fmt.Sprintf("Startup: version=%s build=%s config=%s session=%s %s %s->%s@%s evidence=%s (agent service)",
		version.Version, version.Build, s.cfg.Path, sc.ID, sc.FixVersion, sc.SenderCompID, sc.TargetCompID, sc.Addr(), a.Evidence.Path))
	if err := c.live.Connect(reset); err != nil {
		c.close()
		hint := fmt.Sprintf("check that the counterparty is listening on %s and expects %s -> %s (%s), then call connect_session again", sc.Addr(), sc.SenderCompID, sc.TargetCompID, sc.FixVersion)
		if emu, ok := s.cfg.EmulatorSession(sc.ID); ok {
			hint += fmt.Sprintf("; for the emulator, is it running (control API %s, session %s)?", s.cfg.Emulator.ControlAPI, emu)
		}
		return nil, apiErr(502, "logon_failed", hint, "%s: %v", sc.ID, err)
	}
	return c, nil
}

// close logs out if needed and closes the agent's files.
func (c *conn) close() {
	c.closeOnce.Do(func() {
		if c.live.Connected() {
			if err := c.live.LogoutWith("OrderEcho agent service: session closed"); err != nil {
				c.a.Init.Stop("closing")
			}
		} else {
			c.a.Init.Stop("closing")
		}
		c.closedF.Store(true)
		c.a.Close()
	})
}

// findOrder resolves "last", a ClOrdID or an OrderID across the session's
// connections, newest first.
func (st *sessState) findOrder(ref string) (*conn, *order.Order, *APIError) {
	conns := st.allConns()
	if ref == "" || strings.EqualFold(ref, "last") {
		for i := len(conns) - 1; i >= 0; i-- {
			var o *order.Order
			conns[i].a.With(func(m *order.Manager) { o = m.Last() })
			if o != nil {
				return conns[i], o, nil
			}
		}
		return nil, nil, apiErr(404, "no_orders", "send one with send_order first", "no order has been sent on %s yet", st.cfg.ID)
	}
	for i := len(conns) - 1; i >= 0; i-- {
		var o *order.Order
		conns[i].a.With(func(m *order.Manager) { o, _ = m.Find(ref) })
		if o != nil {
			return conns[i], o, nil
		}
	}
	return nil, nil, apiErr(404, "unknown_order", "use list_orders to see this session's ClOrdIDs and OrderIDs, or \"last\"", "no order %q on %s", ref, st.cfg.ID)
}

// ------------------------------------------------------------ lifecycle

// Shutdown logs out every session cleanly, marks in-flight runs ERROR and
// closes everything.
func (s *Service) Shutdown(timeout time.Duration) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	var active []*runState
	for _, r := range s.runs {
		if r.state() == RunRunning {
			active = append(active, r)
		}
	}
	s.mu.Unlock()
	s.Log.Info("engine", fmt.Sprintf("Shutdown: %d active cert run(s), logging out every session", len(active)))
	for _, r := range active {
		r.abort("service stopped while the run was in progress")
	}
	deadline := time.Now().Add(timeout)
	for _, r := range active {
		select {
		case <-r.done:
		case <-time.After(time.Until(deadline)):
		}
	}
	var wg sync.WaitGroup
	for _, id := range s.order {
		st := s.sessions[id]
		for _, c := range st.allConns() {
			if c.closed() {
				continue
			}
			wg.Add(1)
			go func(c *conn) { defer wg.Done(); c.close() }(c)
		}
	}
	wg.Wait()
	s.cancel()
	s.ev.Event("service", "shutdown", "agent service stopped", false)
	s.Log.Info("engine", "Shutdown complete")
	s.ev.Close()
	s.Log.Close()
}

// Stopping reports whether Shutdown has begun.
func (s *Service) Stopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopping
}

// Started is when the service started.
func (s *Service) Started() time.Time { return s.started }

func (s *Service) certsDir() string { return s.cfg.Storage.CertsDir }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var errNotFound = errors.New("not found")

// ------------------------------------------------------------ live events

// MessageEvent is the data of a "message" event.
type MessageEvent struct {
	SessionID string `json:"session_id"`
	DecodedMessage
	Line    string   `json:"line"`
	ClOrdID string   `json:"cl_ord_id,omitempty"`
	Flags   []string `json:"flags"`
}

func (s *Service) publishMessage(sc config.Session, dir string, m *codec.Message, injected bool) {
	p := profileFor(sc.FixVersion)
	ev := MessageEvent{SessionID: sc.ID, DecodedMessage: decodeMessage(wireMsg{TS: time.Now(), Dir: dir, Msg: m}, p),
		Line: fixview.Line(p, m.MsgType(), m.Get), ClOrdID: m.Value(11), Flags: fixview.Flags(m.Get)}
	if injected {
		ev.Flags = append(ev.Flags, "INJECTED")
	}
	if ev.Flags == nil {
		ev.Flags = []string{}
	}
	s.events.publish(EvMessage, ev)
}

// publishEvidence turns the session's own evidence into live events: order
// reports (with the order's shadow state and checks) and discarded frames.
func (s *Service) publishEvidence(sessionID string, e session.Evidence) {
	switch {
	case e.Event == "order report":
		s.events.publish(EvOrder, map[string]any{"session_id": sessionID, "line": e.Detail, "order": e.Order})
	case strings.HasPrefix(e.Event, "check "):
		s.events.publish(EvOrder, map[string]any{"session_id": sessionID, "line": e.Detail, "check_change": strings.TrimPrefix(e.Event, "check ")})
	case e.Event == "frame discarded":
		s.events.publish(EvMessage, MessageEvent{SessionID: sessionID, DecodedMessage: DecodedMessage{TS: stamp(time.Now()), Direction: "disc",
			MsgTypeName: "discarded frame", Summary: e.Detail, Fields: []DecodedField{}}, Line: "discarded frame: " + e.Detail, Flags: []string{"BAD-FRAMING"}})
	}
}
