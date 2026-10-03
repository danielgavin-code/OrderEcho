package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

func sortStrings(s []string) { sort.Strings(s) }

// numText turns a qty/price argument (JSON number or string) into its text.
func numText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case json.Number:
		return x.String()
	case string:
		return strings.TrimSpace(x)
	case float64:
		return fmt.Sprint(x)
	}
	return fmt.Sprint(v)
}

// orderGate is what every order tool checks first: the session exists, is
// not in a cert run, is connected, and an external one was confirmed.
func (s *Service) orderGate(id string, confirmExternal bool, tool string) (*sessState, *conn, *APIError) {
	st, e := s.session(id)
	if e != nil {
		return nil, nil, e
	}
	if st.cfg.External() && !confirmExternal {
		return nil, nil, apiErr(403, "confirmation_required",
			fmt.Sprintf("ask the human whether to send this to the real counterparty at %s; only if they explicitly approve, call %s again with confirm_external: true", st.cfg.Addr(), tool),
			"session %s is external (%s is not this machine): %s needs confirm_external: true", id, st.cfg.Host, tool)
	}
	if run := st.busy(); run != "" {
		return nil, nil, busyErr(id, run)
	}
	cur := st.current()
	if cur == nil || !cur.connected() {
		return nil, nil, apiErr(409, "not_connected", fmt.Sprintf("call connect_session {session_id: %q} first", id), "session %s is not connected", id)
	}
	return st, cur, nil
}

func waitDefaults(waitFor string, timeout float64) (string, time.Duration) {
	if waitFor == "" {
		waitFor = "ack"
	}
	d := time.Duration(timeout * float64(time.Second))
	if d <= 0 {
		d = 10 * time.Second
		if waitFor == "terminal" {
			d = 30 * time.Second
		}
	}
	return waitFor, d
}

// wait polls until cond holds, the timeout passes or the caller goes away.
func wait(ctx context.Context, a *agent.Agent, d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		ok := false
		a.With(func(*order.Manager) { ok = cond() })
		if ok {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// OrderResult is what the order tools return.
type OrderResult struct {
	SessionID string `json:"session_id"`
	// ClOrdID is the order's first ClOrdID (the D); later F/G get their own.
	ClOrdID        string          `json:"cl_ord_id"`
	RequestClOrdID string          `json:"request_cl_ord_id,omitempty"` // the F/G this call sent
	CurrentClOrdID string          `json:"current_cl_ord_id"`
	OrderID        string          `json:"order_id"`
	Symbol         string          `json:"symbol"`
	Side           string          `json:"side"`
	OrderQty       string          `json:"order_qty"`
	OrdType        string          `json:"ord_type"`
	Price          string          `json:"price,omitempty"`
	State          string          `json:"state"`
	Terminal       bool            `json:"terminal"`
	WaitFor        string          `json:"wait_for,omitempty"`
	TimedOut       bool            `json:"timed_out"`
	WaitedMS       int64           `json:"waited_ms"`
	Reports        []string        `json:"reports"`
	Shadow         map[string]any  `json:"shadow"`
	Checks         CheckStatus     `json:"checks"`
	Requests       []order.Request `json:"-"`
}

// CheckStatus is the live verdict of the 11 checks.
type CheckStatus struct {
	Verdict string          `json:"verdict"`
	Line    string          `json:"line"`
	Results []checks.Result `json:"results"`
}

var sideWords = map[string]string{"1": "BUY", "2": "SELL", "5": "SHORT"}
var typeWords = map[string]string{profile.OrdTypeMarket: "MKT", profile.OrdTypeLimit: "LMT"}

func orderResult(sessionID string, a *agent.Agent, o *order.Order) OrderResult {
	var r OrderResult
	a.With(func(*order.Manager) {
		r = OrderResult{SessionID: sessionID, ClOrdID: o.Root, CurrentClOrdID: o.Current, OrderID: o.OrderID, Symbol: o.Symbol,
			Side: sideWords[o.Side], OrderQty: o.OrderQty.RatString(), OrdType: typeWords[o.OrdType], Price: o.Price, State: o.State,
			Terminal: o.Terminal(), Reports: []string{}, Shadow: o.Snapshot(),
			Checks: CheckStatus{Verdict: o.Verdict, Line: order.CheckLine(o), Results: append([]checks.Result(nil), o.Checks...)}}
		for _, rq := range o.Requests {
			for _, ans := range rq.Answers {
				r.Reports = append(r.Reports, ans)
			}
		}
	})
	if r.Checks.Results == nil {
		r.Checks.Results = []checks.Result{}
	}
	return r
}

func (r OrderResult) oneLine() string {
	s := fmt.Sprintf("%s %s %s %s", r.Side, r.OrderQty, r.Symbol, r.OrdType)
	if r.Price != "" {
		s += " " + r.Price
	}
	s += fmt.Sprintf(" (ClOrdID %s", r.ClOrdID)
	if r.OrderID != "" {
		s += ", OrderID " + r.OrderID
	}
	s += fmt.Sprintf(") is %s", r.State)
	cum, _ := r.Shadow["cum_qty_expected"].(string)
	avg, _ := r.Shadow["avg_px_reported"].(string)
	if cum != "" && cum != "0" {
		s += fmt.Sprintf(", cum %s", cum)
		if avg != "" {
			s += " avg " + avg
		}
	}
	s += "; " + r.Checks.Line
	if r.TimedOut {
		s += fmt.Sprintf(" — still %s after waiting %.1fs for %s", r.State, float64(r.WaitedMS)/1000, r.WaitFor)
	}
	return s
}

func hSendOrder(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in SendOrderArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	spec := order.Spec{Symbol: strings.TrimSpace(in.Symbol), Qty: numText(in.Qty), Side: in.Side, OrdType: in.OrdType,
		Price: numText(in.Price), TIF: in.TIF}
	if _, _, _, err := spec.Validate(); err != nil {
		return nil, "", apiErr(400, "invalid_order", "fix the argument and call send_order again (mkt takes no price; lmt needs a positive price; qty is a positive whole number)", "%v", err)
	}
	_, cur, e := s.orderGate(in.SessionID, in.ConfirmExternal, "send_order")
	if e != nil {
		return nil, "", e
	}
	a := cur.a
	start := time.Now()
	o, err := a.NewOrder(spec)
	if err != nil {
		return nil, "", apiErr(502, "send_failed", "check session_status; reconnect with connect_session if the session dropped", "NewOrderSingle not sent: %v", err)
	}
	waitFor, d := waitDefaults(in.WaitFor, in.TimeoutSec)
	timedOut := false
	switch waitFor {
	case "ack":
		timedOut = !wait(ctx, a, d, func() bool { return o.State != order.Sent || len(o.Requests) > 0 && o.Requests[0].Answered() })
	case "terminal":
		timedOut = !wait(ctx, a, d, func() bool { return o.Terminal() })
	}
	r := orderResult(in.SessionID, a, o)
	r.WaitFor, r.TimedOut, r.WaitedMS = waitFor, timedOut, time.Since(start).Milliseconds()
	return r, "sent: " + r.oneLine(), nil
}

// requestAnswered is true once the request with ClOrdID id has an answer.
func requestAnswered(o *order.Order, id string) bool {
	for _, rq := range o.Requests {
		if rq.ClOrdID == id && rq.Answered() {
			return true
		}
	}
	return false
}

func (s *Service) amend(ctx context.Context, sessionID, ref string, confirm bool, tool, waitFor string, timeout float64,
	send func(a *agent.Agent) (*order.Order, string, error)) (any, string, *APIError) {
	st, cur, e := s.orderGate(sessionID, confirm, tool)
	if e != nil {
		return nil, "", e
	}
	owner, o, e := st.findOrder(ref)
	if e != nil {
		return nil, "", e
	}
	if owner != cur {
		return nil, "", apiErr(409, "order_on_old_connection",
			"only orders sent since the latest connect_session can be canceled or replaced; the counterparty may still have it (for the emulator, emulator_cancel_order can cancel it from its side)",
			"order %s was sent on an earlier connection of %s", o.Root, sessionID)
	}
	start := time.Now()
	_, id, err := send(cur.a)
	if err != nil {
		code, hint := "request_refused", "look at the order with order_timeline; a FILLED, CANCELED or REJECTED order cannot be canceled or replaced"
		if strings.Contains(err.Error(), "not connected") {
			code, hint = "send_failed", "reconnect with connect_session"
		}
		return nil, "", apiErr(409, code, hint, "%s not sent: %v", tool, err)
	}
	waitFor, d := waitDefaults(waitFor, timeout)
	timedOut := false
	switch waitFor {
	case "ack":
		timedOut = !wait(ctx, cur.a, d, func() bool { return requestAnswered(o, id) })
	case "terminal":
		timedOut = !wait(ctx, cur.a, d, func() bool { return o.Terminal() })
	}
	r := orderResult(sessionID, cur.a, o)
	r.RequestClOrdID = id
	r.WaitFor, r.TimedOut, r.WaitedMS = waitFor, timedOut, time.Since(start).Milliseconds()
	var answer string
	cur.a.With(func(*order.Manager) {
		for _, rq := range o.Requests {
			if rq.ClOrdID == id && len(rq.Answers) > 0 {
				answer = rq.Answers[len(rq.Answers)-1]
			}
		}
	})
	verb := map[string]string{"cancel_order": "cancel", "replace_order": "replace"}[tool]
	summary := fmt.Sprintf("%s %s sent for %s", verb, id, o.Root)
	if answer != "" {
		summary += "; answer: " + answer
	}
	return r, summary + "; now " + r.oneLine(), nil
}

func hCancelOrder(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in CancelArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	return s.amend(ctx, in.SessionID, in.ClOrdID, in.ConfirmExternal, "cancel_order", in.WaitFor, in.TimeoutSec,
		func(a *agent.Agent) (*order.Order, string, error) { return a.Cancel(in.ClOrdID) })
}

func hReplaceOrder(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in ReplaceArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	qty, px := numText(in.Qty), numText(in.Price)
	if qty == "" && px == "" {
		return nil, "", apiErr(400, "invalid_arguments", "give qty (the new total quantity), price (the new limit price), or both", "replace_order changes nothing")
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	if qty == "" {
		if _, o, e := st.findOrder(in.ClOrdID); e == nil {
			if cur := st.current(); cur != nil {
				cur.a.With(func(*order.Manager) { qty = o.OrderQty.RatString() })
			}
		}
	}
	return s.amend(ctx, in.SessionID, in.ClOrdID, in.ConfirmExternal, "replace_order", in.WaitFor, in.TimeoutSec,
		func(a *agent.Agent) (*order.Order, string, error) { return a.Replace(in.ClOrdID, qty, px) })
}

// OrderRow is one list_orders row.
type OrderRow struct {
	ClOrdID        string `json:"cl_ord_id"`
	CurrentClOrdID string `json:"current_cl_ord_id"`
	OrderID        string `json:"order_id"`
	Symbol         string `json:"symbol"`
	Side           string `json:"side"`
	OrderQty       string `json:"order_qty"`
	OrdType        string `json:"ord_type"`
	Price          string `json:"price,omitempty"`
	State          string `json:"state"`
	CumQty         string `json:"cum_qty"`
	LeavesQty      string `json:"leaves_qty"`
	AvgPx          string `json:"avg_px,omitempty"`
	Verdict        string `json:"verdict"`
	Connection     string `json:"connection"` // the agent run id of the connection it was sent on
	Current        bool   `json:"on_current_connection"`
}

func (s *Service) orderRows(st *sessState, status string) []OrderRow {
	rows := []OrderRow{}
	conns := st.allConns()
	for i, c := range conns {
		c.a.With(func(m *order.Manager) {
			for _, o := range m.Orders() {
				switch {
				case status == "" || status == "all":
				case status == "open":
					if o.Terminal() {
						continue
					}
				case status == "terminal":
					if !o.Terminal() {
						continue
					}
				default:
					if !strings.EqualFold(o.State, status) {
						continue
					}
				}
				snap := o.Snapshot()
				rows = append(rows, OrderRow{ClOrdID: o.Root, CurrentClOrdID: o.Current, OrderID: o.OrderID, Symbol: o.Symbol,
					Side: sideWords[o.Side], OrderQty: o.OrderQty.RatString(), OrdType: typeWords[o.OrdType], Price: o.Price,
					State: o.State, CumQty: fmt.Sprint(snap["cum_qty_expected"]), LeavesQty: fmt.Sprint(snap["leaves_qty_expected"]),
					AvgPx: o.AvgReported, Verdict: o.Verdict, Connection: c.a.RunID, Current: i == len(conns)-1})
			}
		})
	}
	return rows
}

func hListOrders(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in ListOrdersArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	rows := s.orderRows(st, in.Status)
	var parts []string
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s %s %s %s %s", r.ClOrdID, r.Side, r.OrderQty, r.Symbol, r.State))
	}
	filter := in.Status
	if filter == "" {
		filter = "all"
	}
	summary := fmt.Sprintf("%d order(s) on %s (%s)", len(rows), in.SessionID, filter)
	if len(parts) > 0 {
		if len(parts) > 10 {
			parts = append(parts[len(parts)-10:], "…")
		}
		summary += ": " + strings.Join(parts, "; ")
	}
	return map[string]any{"session_id": in.SessionID, "status": filter, "orders": rows}, summary, nil
}

// TimelineStep is one message of an order's chain.
type TimelineStep struct {
	Time          string `json:"time"`
	Direction     string `json:"direction"`
	MsgType       string `json:"msg_type"`
	MsgTypeName   string `json:"msg_type_name"`
	ClOrdID       string `json:"cl_ord_id,omitempty"`
	OrigClOrdID   string `json:"orig_cl_ord_id,omitempty"`
	OrderID       string `json:"order_id,omitempty"`
	ExecType      string `json:"exec_type,omitempty"`
	ExecTypeName  string `json:"exec_type_name,omitempty"`
	OrdStatus     string `json:"ord_status,omitempty"`
	OrdStatusName string `json:"ord_status_name,omitempty"`
	OrderQty      string `json:"order_qty,omitempty"`
	Price         string `json:"price,omitempty"`
	LastQty       string `json:"last_qty,omitempty"`
	LastPx        string `json:"last_px,omitempty"`
	CumQty        string `json:"cum_qty,omitempty"`
	LeavesQty     string `json:"leaves_qty,omitempty"`
	AvgPx         string `json:"avg_px,omitempty"`
	Text          string `json:"text,omitempty"`
	Replay        bool   `json:"replay,omitempty"`
	Injected      bool   `json:"injected,omitempty"`
}

func hOrderTimeline(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in TimelineArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	ref := in.ClOrdID
	if ref == "" {
		ref = in.OrderID
	}
	owner, o, e := st.findOrder(ref)
	if e != nil {
		return nil, "", e
	}
	chain := owner.a.Chain(o)
	p := profileFor(st.cfg.FixVersion)
	var steps []TimelineStep
	var arrows []string
	for _, stp := range chain.Steps {
		m := stp.Message
		ts := ""
		if m.TS != nil {
			ts = m.TS.UTC().Format("2006-01-02T15:04:05.000Z")
		}
		t := TimelineStep{Time: ts, Direction: m.Direction, MsgType: m.MsgType(), MsgTypeName: p.MsgTypeName(m.MsgType()),
			ClOrdID: m.Value(11), OrigClOrdID: m.Value(41), OrderID: m.Value(37), ExecType: m.Value(150), OrdStatus: m.Value(39),
			OrderQty: m.Value(38), Price: m.Value(44), LastQty: m.Value(32), LastPx: m.Value(31), CumQty: m.Value(14),
			LeavesQty: m.Value(151), AvgPx: m.Value(6), Text: m.Value(58), Replay: stp.Replay, Injected: m.Injected}
		if t.ExecType != "" {
			t.ExecTypeName = p.ExecTypeName(t.ExecType)
		}
		if t.OrdStatus != "" {
			t.OrdStatusName = p.OrdStatusName(t.OrdStatus)
		}
		steps = append(steps, t)
		a := t.MsgType
		if t.ExecTypeName != "" {
			a += "(" + t.ExecTypeName + ")"
		}
		arrows = append(arrows, a)
	}
	var shadow map[string]any
	owner.a.With(func(*order.Manager) { shadow = o.Snapshot() })
	var bad []string
	for _, r := range chain.Checks {
		if r.Status != checks.PASS {
			bad = append(bad, r.Status+" "+r.Name+": "+r.Explanation)
		}
	}
	out := map[string]any{"session_id": in.SessionID, "seed": chain.Seed, "cl_ord_ids": chain.SortedIDs(), "order_ids": chain.SortedOrderIDs(),
		"steps": steps, "checks": chain.Checks, "verdict": chain.Verdict(), "shadow": shadow}
	summary := fmt.Sprintf("order %s: %s; %d/%d checks PASS, verdict %s", o.Root, strings.Join(arrows, " -> "),
		len(chain.Checks)-len(bad), len(chain.Checks), chain.Verdict())
	if len(bad) > 0 {
		summary += " (" + strings.Join(bad, "; ") + ")"
	}
	return out, summary, nil
}
