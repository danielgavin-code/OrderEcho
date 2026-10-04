package service

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
)

// The tool catalogue: one entry per §5 tool. The HTTP API (POST
// /api/v1/<name>) and both MCP transports serve exactly these, through the
// same Dispatch: same validation, same handlers, same result shapes.
//
// Descriptions are written for an LLM reader: what the tool does, when to
// use it, what comes back, and the mistakes to avoid.

// Group decides whether config lets a tool be registered for MCP.
const (
	GroupCore     = "core"
	GroupOrders   = "orders"   // mcp.allow_orders
	GroupEmulator = "emulator" // mcp.allow_emulator_tools + emulator.control_api
	// GroupAPIOnly operations are served on /api/v1 (the GUI uses them) but
	// never registered as MCP tools (A5 adds no MCP tool but cert_run_report).
	GroupAPIOnly = "api-only"
)

// Tool is one catalogue entry.
type Tool struct {
	Name        string
	Title       string
	Description string
	Group       string
	ReadOnly    bool
	Destructive bool // meaningful only when !ReadOnly
	OpenWorld   bool // talks to the counterparty (or its emulator)
	Idempotent  bool
	input       reflect.Type
	Schema      *jsonschema.Schema
	resolved    *jsonschema.Resolved
	handle      handlerFunc
}

// ------------------------------------------------------------ inputs

// NoArgs is the input of tools that take none.
type NoArgs struct{}

// SessionArgs names one configured session.
type SessionArgs struct {
	SessionID string `json:"session_id" jsonschema:"a configured session id from list_sessions, e.g. emu42"`
}

// ConnectArgs is connect_session's input.
type ConnectArgs struct {
	SessionID string `json:"session_id" jsonschema:"a configured session id from list_sessions, e.g. emu42"`
	Reset     bool   `json:"reset,omitempty" jsonschema:"reset sequence numbers to 1 on this logon (141=Y); only needed when the counterparty asks for it or seqnums are out of sync"`
}

// SendOrderArgs is send_order's input.
type SendOrderArgs struct {
	SessionID       string  `json:"session_id" jsonschema:"the connected session to send on"`
	Symbol          string  `json:"symbol" jsonschema:"instrument symbol, e.g. AAPL"`
	Side            string  `json:"side" jsonschema:"buy, sell or short"`
	Qty             any     `json:"qty" jsonschema:"order quantity, a positive whole number (100 or \"100\")"`
	OrdType         string  `json:"ord_type" jsonschema:"mkt (market, no price) or lmt (limit, needs price)"`
	Price           any     `json:"price,omitempty" jsonschema:"limit price as a decimal string or number, e.g. \"10.50\"; only for lmt, never for mkt"`
	TIF             string  `json:"tif,omitempty" jsonschema:"time in force: day, gtc, opg, ioc, fok or gtx; omit to send no TimeInForce (the counterparty's default, usually Day)"`
	WaitFor         string  `json:"wait_for,omitempty" jsonschema:"none: return right after sending; ack (default): wait for the first answer (New or Reject); terminal: wait until FILLED, CANCELED or REJECTED"`
	TimeoutSec      float64 `json:"timeout_sec,omitempty" jsonschema:"how long to wait for wait_for (default 10 for ack, 30 for terminal; max 300)"`
	ConfirmExternal bool    `json:"confirm_external,omitempty" jsonschema:"required true for a session tagged external (a real counterparty off this machine); set it only after the human explicitly approved this order"`
}

// CancelArgs is cancel_order's input.
type CancelArgs struct {
	SessionID       string  `json:"session_id" jsonschema:"the connected session the order was sent on"`
	ClOrdID         string  `json:"cl_ord_id" jsonschema:"any ClOrdID of the order (the one send_order returned is fine), its OrderID, or \"last\" for the most recent order on this session"`
	WaitFor         string  `json:"wait_for,omitempty" jsonschema:"none, ack (default: wait for the cancel's answer) or terminal"`
	TimeoutSec      float64 `json:"timeout_sec,omitempty" jsonschema:"how long to wait (default 10; max 300)"`
	ConfirmExternal bool    `json:"confirm_external,omitempty" jsonschema:"required true on an external session, only after the human approved it"`
}

// ReplaceArgs is replace_order's input.
type ReplaceArgs struct {
	SessionID       string  `json:"session_id" jsonschema:"the connected session the order was sent on"`
	ClOrdID         string  `json:"cl_ord_id" jsonschema:"any ClOrdID of the order, its OrderID, or \"last\""`
	Qty             any     `json:"qty,omitempty" jsonschema:"new total order quantity (not the change); omit to keep the current quantity"`
	Price           any     `json:"price,omitempty" jsonschema:"new limit price (limit orders only); omit to keep the current price"`
	WaitFor         string  `json:"wait_for,omitempty" jsonschema:"none, ack (default: wait for the replace's answer) or terminal"`
	TimeoutSec      float64 `json:"timeout_sec,omitempty" jsonschema:"how long to wait (default 10; max 300)"`
	ConfirmExternal bool    `json:"confirm_external,omitempty" jsonschema:"required true on an external session, only after the human approved it"`
}

// ListOrdersArgs is list_orders's input.
type ListOrdersArgs struct {
	SessionID string `json:"session_id" jsonschema:"a configured session id"`
	Status    string `json:"status,omitempty" jsonschema:"open (not yet FILLED/CANCELED/REJECTED), terminal, all (default), or one state: SENT, NEW, PARTIALLY_FILLED, FILLED, CANCELED, REJECTED, PENDING_CANCEL, PENDING_REPLACE"`
}

// TimelineArgs is order_timeline's input.
type TimelineArgs struct {
	SessionID string `json:"session_id" jsonschema:"a configured session id"`
	ClOrdID   string `json:"cl_ord_id,omitempty" jsonschema:"any ClOrdID of the chain, or \"last\"; give this or order_id (neither means last)"`
	OrderID   string `json:"order_id,omitempty" jsonschema:"the counterparty's OrderID (tag 37) of the chain"`
}

// MessagesArgs is recent_messages's input.
type MessagesArgs struct {
	SessionID string   `json:"session_id" jsonschema:"a configured session id"`
	Limit     int      `json:"limit,omitempty" jsonschema:"how many of the most recent messages (default 20, max 200)"`
	Direction string   `json:"direction,omitempty" jsonschema:"in (from the counterparty), out (ours) or both (default)"`
	MsgTypes  []string `json:"msg_types,omitempty" jsonschema:"only these MsgTypes, by code or name, e.g. [\"8\", \"OrderCancelReject\"]; admin messages (Heartbeat, Logon...) are included unless you filter"`
}

// StartRunArgs is start_cert_run's input.
type StartRunArgs struct {
	Suite           string   `json:"suite" jsonschema:"suite name or file from list_cert_suites, e.g. order-entry-fix42 (it must match the session's FIX version)"`
	Target          string   `json:"target" jsonschema:"target name or file from list_cert_targets, e.g. emulator"`
	SessionID       string   `json:"session_id" jsonschema:"the session to certify; the run takes it over (logs it out and back on with a sequence reset) and leaves it disconnected"`
	Cases           []string `json:"cases,omitempty" jsonschema:"only these case ids, e.g. [\"4.1\", \"4.3\"]"`
	Section         string   `json:"section,omitempty" jsonschema:"only this section id, e.g. ORD; with cases, the run takes both"`
	ConfirmExternal bool     `json:"confirm_external,omitempty" jsonschema:"required true on an external session (the run sends real orders), only after the human approved it"`
}

// RunArgs names a cert run.
type RunArgs struct {
	RunID string `json:"run_id" jsonschema:"the run_id start_cert_run returned"`
}

// ResultsArgs is cert_run_results's input.
type ResultsArgs struct {
	RunID string `json:"run_id" jsonschema:"the run_id start_cert_run returned"`
	Only  string `json:"only,omitempty" jsonschema:"which cases to list: failed (FAIL/ERROR), pending (PENDING/BLOCKED/NOT_RUN), all; default: failed and pending"`
}

// AttestArgs is attest_cert_case's input.
type AttestArgs struct {
	RunID         string `json:"run_id" jsonschema:"the finished run"`
	CaseID        string `json:"case_id" jsonschema:"an attestable case id (cert_run_results lists them)"`
	Status        string `json:"status" jsonschema:"what the human confirmed: pass, fail or na"`
	By            string `json:"by" jsonschema:"the name of the human who confirmed it (never your own name)"`
	Note          string `json:"note" jsonschema:"what the human said or checked, in their words"`
	UserConfirmed bool   `json:"user_confirmed,omitempty" jsonschema:"must be true, and only after the human explicitly confirmed this exact status in the conversation"`
}

// EmuFillArgs is emulator_fill_order's input.
type EmuFillArgs struct {
	OrderID string `json:"order_id" jsonschema:"the emulator's OrderID (tag 37) from send_order or list_orders, not the ClOrdID"`
	Qty     any    `json:"qty" jsonschema:"quantity to fill now (a positive whole number, at most the order's leaves)"`
	Price   any    `json:"price,omitempty" jsonschema:"fill price, e.g. \"10.25\"; omit to let the emulator price it"`
}

// EmuOrderArgs names an emulator order.
type EmuOrderArgs struct {
	OrderID string `json:"order_id" jsonschema:"the emulator's OrderID (tag 37) from send_order or list_orders, not the ClOrdID"`
}

// EmuSeqGapArgs is emulator_inject_seq_gap's input (API only).
type EmuSeqGapArgs struct {
	Session string `json:"session" jsonschema:"our session id (e.g. emu42) or the emulator's own id for it"`
	Skip    int    `json:"skip" jsonschema:"how many outbound sequence numbers the emulator skips (1-100)"`
}

// EmuInjectArgs is emulator_inject_next's input.
type EmuInjectArgs struct {
	Session string            `json:"session" jsonschema:"our session id (e.g. emu42) or the emulator's own id for it (e.g. agent42)"`
	MsgType string            `json:"msg_type,omitempty" jsonschema:"only alter the next message of this MsgType (e.g. 8); omit for the very next message"`
	Set     map[string]string `json:"set,omitempty" jsonschema:"tags to set or overwrite, tag number -> value, e.g. {\"58\": \"hello\"}"`
	Remove  []any             `json:"remove,omitempty" jsonschema:"tag numbers to remove, e.g. [151]"`
}

// ------------------------------------------------------------ catalogue

const (
	orderResultDoc = "Returns the ClOrdID, the OrderID once acknowledged, the order state (SENT, NEW, PARTIALLY_FILLED, FILLED, CANCELED, REJECTED, PENDING_*), every report received so far, the shadow state the agent keeps (expected CumQty/LeavesQty/AvgPx from the fills), and the live verdict of the 11 order checks (PASS/WARN/FAIL). The verdict comes from the agent's code; report it as given."
	askExternal    = "If list_sessions tags the session external (a real counterparty off this machine), you must ask the human first and pass confirm_external: true only after they approve."
)

func catalogue() []*Tool {
	tools := []*Tool{
		{Name: "list_sessions", Title: "List FIX sessions", Group: GroupCore, ReadOnly: true, input: typeOf[NoArgs](),
			Description: "Lists every FIX session configured in this OrderEcho agent: id, FIX version, route (SenderCompID -> TargetCompID), address, connection state, and whether it is external (a counterparty off this machine) or backed by the emulator. Start here to find a session_id. Sessions start DISCONNECTED; use connect_session before sending orders."},
		{Name: "connect_session", Title: "Connect a FIX session", Group: GroupCore, OpenWorld: true, Idempotent: true, input: typeOf[ConnectArgs](),
			Description: "Opens the TCP connection and logs on (35=A) for a configured session, and keeps it up between tool calls. Returns once the Logon is answered (or with the reason it failed: refused, no answer, cannot connect). Calling it on a session that is already connected changes nothing. Do not use reset unless asked; the configured sessions already reset on logon when the counterparty expects it."},
		{Name: "disconnect_session", Title: "Disconnect a FIX session", Group: GroupCore, OpenWorld: true, Destructive: true, Idempotent: true, input: typeOf[SessionArgs](),
			Description: "Logs out cleanly (35=5 both ways) and closes the connection. Orders stay listable afterwards, but you cannot cancel or replace them until you connect again (and then only orders sent on the new connection). Returns whether the logout was clean."},
		{Name: "session_status", Title: "Session status", Group: GroupCore, ReadOnly: true, input: typeOf[SessionArgs](),
			Description: "Detailed state of one session: connection state, sequence numbers (next out / next in), heartbeat interval and when a message last went each way, open orders, and a one-line summary of the last few messages. Use it when something looks stuck, or before sending after a pause."},
		{Name: "send_order", Title: "Send a new order", Group: GroupOrders, OpenWorld: true, input: typeOf[SendOrderArgs](),
			Description: "Sends a NewOrderSingle (35=D) on a connected session and, by default, waits for its first answer. Use wait_for=terminal to wait until it is FILLED/CANCELED/REJECTED (a limit order far from the market may simply rest: then you get timed_out=true and the current state, which is not an error). " + orderResultDoc + " Common mistakes: a price on a market order, a missing price on a limit order, qty as a decimal, sending before connect_session. " + askExternal},
		{Name: "cancel_order", Title: "Cancel an order", Group: GroupOrders, OpenWorld: true, Destructive: true, input: typeOf[CancelArgs](),
			Description: "Sends an OrderCancelRequest (35=F) for a working order, referring to it by any of its ClOrdIDs, its OrderID, or \"last\". Waits for the answer: a Canceled report (state CANCELED) or an OrderCancelReject (35=9, e.g. too late to cancel). " + orderResultDoc + " A terminal order (FILLED/CANCELED/REJECTED) cannot be canceled. " + askExternal},
		{Name: "replace_order", Title: "Cancel/replace an order", Group: GroupOrders, OpenWorld: true, Destructive: true, input: typeOf[ReplaceArgs](),
			Description: "Sends an OrderCancelReplaceRequest (35=G) changing a working order's total quantity and/or limit price; give at least one of qty or price. qty is the new total, not an increment. Waits for the answer (Replaced report or 35=9 reject). " + orderResultDoc + " " + askExternal},
		{Name: "list_orders", Title: "List orders", Group: GroupCore, ReadOnly: true, input: typeOf[ListOrdersArgs](),
			Description: "Lists the orders sent on a session (all its connections since the service started), newest last: ClOrdID, OrderID, symbol, side, quantity, type, price, state, cumulative/leaves quantity, average price and the checks verdict. Filter with status=open to see what is still working."},
		{Name: "order_timeline", Title: "Order timeline and checks", Group: GroupCore, ReadOnly: true, input: typeOf[TimelineArgs](),
			Description: "The full chain of one order: every message both ways (D, F, G, execution reports, rejects) with decoded ExecType/OrdStatus, quantities and prices, then the 11 order checks, each with PASS/WARN/FAIL and a plain-English explanation, and the overall verdict. Use it to explain what happened to an order or why a check warned or failed. The checks are the authority; do not re-judge them."},
		{Name: "recent_messages", Title: "Recent FIX messages", Group: GroupCore, ReadOnly: true, input: typeOf[MessagesArgs](),
			Description: "The most recent FIX messages on a session (both directions by default), decoded: message type names, every field with its tag name and, for enumerations, the value's meaning. Use it to look at the raw exchange (heartbeats, rejects, logon details) when order_timeline is not enough."},
		{Name: "list_cert_suites", Title: "List certification suites", Group: GroupCore, ReadOnly: true, input: typeOf[NoArgs](),
			Description: "Lists the certification suites (executable checklists) available: name, FIX version, title, sections and case counts by mode (auto: the agent verifies; assisted: needs the counterparty to act; manual: needs a human). Pick the suite whose FIX version matches the session."},
		{Name: "list_cert_targets", Title: "List certification targets", Group: GroupCore, ReadOnly: true, input: typeOf[NoArgs](),
			Description: "Lists the certification targets (counterparty adapters): name, whether it has a control API (then assisted cases run automatically), and how many cases it marks not applicable. Use target \"emulator\" for the OrderEcho emulator, \"generic\" for any other venue."},
		{Name: "start_cert_run", Title: "Start a certification run", Group: GroupOrders, OpenWorld: true, input: typeOf[StartRunArgs](),
			Description: "Starts a certification run in the background and returns its run_id at once; the run can take minutes (a whole suite several). The run takes the session over: it logs it out if connected, logs on with a sequence reset, runs the cases (sending real orders), and leaves the session disconnected. Poll cert_run_status every 10-30 seconds until state is finished, then call cert_run_results. Order tools on that session are refused while it runs. Pass/fail is decided by the runner's code, never by you. " + askExternal},
		{Name: "cert_run_status", Title: "Certification run progress", Group: GroupCore, ReadOnly: true, input: typeOf[RunArgs](),
			Description: "Progress of a certification run: state (running, finished, error), cases done / total, the case running now, and the counts by status so far (PASS, FAIL, BLOCKED, PENDING, N/A, ERROR). When state is finished or error, call cert_run_results."},
		{Name: "cert_run_results", Title: "Certification run results", Group: GroupCore, ReadOnly: true, input: typeOf[ResultsArgs](),
			Description: "Results of a finished certification run: counts by status (all cases and required cases), the exit code and what it means, the failing and pending cases with their reasons, which cases a human may attest, the drafted deviations section, and where the evidence is on disk (results.json, summary.txt, deviations.md, one folder per case). Quote reasons as given; do not upgrade a FAIL or PENDING."},
		{Name: "cert_run_report", Title: "Certification report", Group: GroupCore, ReadOnly: true, input: typeOf[RunArgs](),
			Description: "The human-readable certification report of a finished run: a single HTML file with the verdict (CERTIFIED, NOT CERTIFIED or INCOMPLETE), counts, every case with its steps, checks, attestations and FIX evidence, the deviations, and an integrity appendix of SHA-256 digests. Returns the file's path, the URL to open it in a browser (served by the agent on this machine), the verdict with its reason, and the counts. Use it when the human wants something to read, print or hand to the counterparty or compliance. The report is written automatically when a run ends and again after every attestation; quote its verdict as given."},
		{Name: "attest_cert_case", Title: "Record a human attestation", Group: GroupCore, Destructive: true, input: typeOf[AttestArgs](),
			Description: "Records a human's attestation for one manual or assisted case of a finished run (e.g. environment, credentials, sign-off, the deviations review), then recomputes the run's summary and exit code. ALWAYS ask the human first: show them the case and what it asks, get their explicit answer (pass, fail or na) and their name, and only then call this with user_confirmed: true. Never attest on your own judgement, never invent the name, and never set user_confirmed without their explicit confirmation; calls without it are rejected. Cases decided by code (auto, or assisted cases whose control steps ran) cannot be attested."},
		{Name: "emulator_fill_order", Title: "Emulator: fill an order", Group: GroupEmulator, OpenWorld: true, input: typeOf[EmuFillArgs](),
			Description: "Acts on the counterparty EMULATOR (the OrderEcho test venue), not on a real venue: makes the emulator fill part of one of its working orders at a price you choose, which sends us an execution report. Use it to drive partial fills and average-price scenarios on orders that rest (e.g. the hold symbol ZWZZT). order_id is the emulator's OrderID (tag 37), not our ClOrdID. Returns the emulator's answer and our side's updated view of the order."},
		{Name: "emulator_cancel_order", Title: "Emulator: cancel an order", Group: GroupEmulator, OpenWorld: true, Destructive: true, input: typeOf[EmuOrderArgs](),
			Description: "Acts on the counterparty EMULATOR, not on a real venue: the emulator cancels one of its working orders on its own initiative (an unsolicited cancel), which sends us a Canceled execution report. To cancel an order yourself, use cancel_order instead. order_id is the emulator's OrderID (tag 37)."},
		{Name: "emulator_hold_order", Title: "Emulator: hold an order", Group: GroupEmulator, OpenWorld: true, Idempotent: true, input: typeOf[EmuOrderArgs](),
			Description: "Acts on the counterparty EMULATOR, not on a real venue: stops the emulator's scheduled reports for one order and leaves it working, so you can then fill, cancel or replace it step by step. order_id is the emulator's OrderID (tag 37)."},
		{Name: "emulator_inject_seq_gap", Title: "Emulator: skip outbound seqnums", Group: GroupAPIOnly, OpenWorld: true, Destructive: true, input: typeOf[EmuSeqGapArgs](),
			Description: "Acts on the counterparty EMULATOR, not on a real venue: the emulator skips outbound sequence numbers on one session, so the agent must detect the gap and send a ResendRequest. Served on the HTTP API for the GUI's emulator panel; not an MCP tool."},
		{Name: "emulator_inject_next", Title: "Emulator: alter its next message", Group: GroupEmulator, OpenWorld: true, Destructive: true, input: typeOf[EmuInjectArgs](),
			Description: "Acts on the counterparty EMULATOR, not on a real venue: makes the emulator alter the next message it sends us on one session (optionally only the next of a MsgType), setting or removing tags, to test how the agent and its checks react to a bad counterparty message. The alteration applies once."},
	}
	for _, t := range tools {
		t.handle = handlers[t.Name]
		if t.handle == nil {
			panic("service: no handler for tool " + t.Name)
		}
	}
	return tools
}

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// property tweaks the inferred schemas cannot express from Go types.
var enums = map[string][]any{
	"side": {"buy", "sell", "short"}, "ord_type": {"mkt", "lmt"}, "tif": {"day", "gtc", "opg", "ioc", "fok", "gtx"},
	"wait_for": {"none", "ack", "terminal"}, "direction": {"in", "out", "both"}, "only": {"failed", "pending", "all"},
}

func buildSchema(t *Tool) error {
	s, err := jsonschema.ForType(t.input, &jsonschema.ForOptions{})
	if err != nil {
		return err
	}
	if s.Properties == nil {
		s.Properties = map[string]*jsonschema.Schema{}
	}
	for name, p := range s.Properties {
		if e, ok := enums[name]; ok {
			p.Enum = e
		}
		switch name {
		case "qty":
			p.Types, p.Type = []string{"integer", "string"}, ""
		case "price":
			p.Types, p.Type = []string{"number", "string"}, ""
		case "remove":
			p.Items = &jsonschema.Schema{Types: []string{"integer", "string"}}
		case "limit":
			p.Minimum, p.Maximum = f64(1), f64(200)
		case "timeout_sec":
			p.Minimum, p.Maximum = f64(0), f64(300)
		}
	}
	switch t.Name {
	case "attest_cert_case":
		s.Properties["status"].Enum = []any{"pass", "fail", "na"}
	case "emulator_inject_seq_gap":
		s.Properties["skip"].Minimum, s.Properties["skip"].Maximum = f64(1), f64(100)
	case "list_orders":
		s.Properties["status"].Enum = []any{"open", "terminal", "all", "SENT", "NEW", "PARTIALLY_FILLED", "FILLED", "CANCELED", "REJECTED", "PENDING_CANCEL", "PENDING_REPLACE"}
	}
	r, err := s.Resolve(nil)
	if err != nil {
		return err
	}
	t.Schema, t.resolved = s, r
	return nil
}

func f64(v float64) *float64 { return &v }

var catalogList = func() []*Tool {
	list := catalogue()
	for _, t := range list {
		if err := buildSchema(t); err != nil {
			panic(fmt.Sprintf("service: schema for %s: %v", t.Name, err))
		}
	}
	return list
}()

var catalog = func() map[string]*Tool {
	m := map[string]*Tool{}
	for _, t := range catalogList {
		m[t.Name] = t
	}
	return m
}()

// Catalog returns every tool, in catalogue order.
func Catalog() []*Tool { return append([]*Tool(nil), catalogList...) }

// Lookup returns a tool by name.
func Lookup(name string) (*Tool, bool) { t, ok := catalog[name]; return t, ok }

// Names lists every tool name, sorted.
func Names() []string {
	var out []string
	for _, t := range catalogList {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}
