// Package order is the PURE order manager: the agent's shadow state of every
// order it sends, built from what we sent and what the counterparty reported,
// judged after every report by the eleven checks.
//
// It plugs into the session as its App: the session tells it about every
// outbound application message (new, replayed or injected via SendRaw) and
// every inbound one, plus session Rejects. It never touches the network or
// the wall clock; time comes from the injected clock.
package order

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
)

// Order states.
const (
	Sent            = "SENT"
	New             = "NEW"
	PartiallyFilled = "PARTIALLY_FILLED"
	Filled          = "FILLED"
	Canceled        = "CANCELED"
	Rejected        = "REJECTED"
	PendingCancel   = "PENDING_CANCEL"
	PendingReplace  = "PENDING_REPLACE"
	Replaced        = "REPLACED" // appears in History only
)

var statusState = map[string]string{
	"0": New, "1": PartiallyFilled, "2": Filled, "4": Canceled, "8": Rejected,
	"6": PendingCancel, "E": PendingReplace, "A": Sent,
	// Rare terminal statuses the emulator never sends: the order is over.
	"C": Canceled, "3": Canceled,
}

// IsTerminal reports whether a state is final.
func IsTerminal(state string) bool {
	return state == Filled || state == Canceled || state == Rejected
}

// ------------------------------------------------------------ ClOrdIDs

// IDs generates ClOrdIDs <prefix>-<run_id>-<n>. One generator per process
// keeps them unique across sessions.
type IDs struct {
	mu     sync.Mutex
	Prefix string
	RunID  string
	n      int
}

// Next returns the next ClOrdID.
func (g *IDs) Next() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	prefix := g.Prefix
	if prefix == "" {
		prefix = "OE"
	}
	return fmt.Sprintf("%s-%s-%d", prefix, g.RunID, g.n)
}

// ------------------------------------------------------------ inputs

// Spec is a new order as the user asked for it.
type Spec struct {
	Symbol  string
	Qty     string
	Side    string // buy | sell | short
	OrdType string // mkt | lmt
	Price   string // lmt only
	TIF     string // "" or day | gtc | opg | ioc | fok | gtx
	Account string // overrides the configured account for this order
}

var sides = map[string]string{"buy": "1", "sell": "2", "short": "5"}
var sideNames = map[string]string{"1": "BUY", "2": "SELL", "5": "SHORT"}
var ordTypes = map[string]string{"mkt": profile.OrdTypeMarket, "lmt": profile.OrdTypeLimit}
var tifs = map[string]string{"day": "0", "gtc": "1", "opg": "2", "ioc": "3", "fok": "4", "gtx": "5"}

func positiveWhole(s string) (*big.Rat, bool) {
	d, ok := checks.ParseDec(s)
	if !ok || d.R.Sign() <= 0 || !d.R.IsInt() {
		return nil, false
	}
	return d.R, true
}

func positiveDec(s string) (*big.Rat, bool) {
	d, ok := checks.ParseDec(s)
	if !ok || d.R.Sign() <= 0 {
		return nil, false
	}
	return d.R, true
}

// Validate checks a Spec client-side and returns the FIX values. SendRaw
// remains the way to send deliberately bad messages.
func (s Spec) Validate() (side, ordType, tif string, err error) {
	var ok bool
	if strings.TrimSpace(s.Symbol) == "" {
		return "", "", "", fmt.Errorf("symbol is required")
	}
	if _, ok = positiveWhole(s.Qty); !ok {
		return "", "", "", fmt.Errorf("quantity must be a positive whole number, got %q", s.Qty)
	}
	if side, ok = sides[strings.ToLower(s.Side)]; !ok {
		return "", "", "", fmt.Errorf("side must be buy, sell or short, got %q", s.Side)
	}
	if ordType, ok = ordTypes[strings.ToLower(s.OrdType)]; !ok {
		return "", "", "", fmt.Errorf("order type must be mkt or lmt, got %q", s.OrdType)
	}
	if ordType == profile.OrdTypeLimit {
		if _, ok = positiveDec(s.Price); !ok {
			return "", "", "", fmt.Errorf("a limit order needs a positive price, got %q", s.Price)
		}
	} else if s.Price != "" {
		return "", "", "", fmt.Errorf("a market order takes no price")
	}
	if s.TIF != "" {
		if tif, ok = tifs[strings.ToLower(s.TIF)]; !ok {
			return "", "", "", fmt.Errorf("time in force must be day, gtc, opg, ioc, fok or gtx, got %q", s.TIF)
		}
	}
	return side, ordType, tif, nil
}

// ------------------------------------------------------------ state

// Request is one D/F/G we sent (normally or via SendRaw).
type Request struct {
	MsgType     string
	ClOrdID     string
	OrigClOrdID string
	Seq         int
	Injected    bool
	Answers     []string // one line per response attached
	Rejected    bool     // answered by a reject (ER 39=8, 35=9, 35=3, 35=j)
	sent        bool
}

// Answered reports whether any response has been attached.
func (r *Request) Answered() bool { return len(r.Answers) > 0 }

// Order is the shadow state of one order chain.
type Order struct {
	Root     string   // the D's ClOrdID
	ClOrdIDs []string // every ClOrdID of the chain, in the order we used them
	// Current is the ClOrdID a new F/G refers to: the most recent request of
	// the chain not known to have been rejected.
	Current string
	// Accepted is the most recent ClOrdID the counterparty acknowledged.
	Accepted string
	OrderID  string
	Symbol   string
	Side     string
	OrdType  string
	Price    string
	OrderQty *big.Rat
	TIF      string
	State    string
	History  []string // state transitions, including REPLACED
	Requests []*Request

	// What the counterparty last reported.
	CumReported, LeavesReported, AvgReported string
	// Our own expectation from the fills: Cum = sum(LastQty),
	// Avg = sum(LastQty*LastPx)/sum(LastQty), Leaves = OrderQty - Cum while
	// working (exact decimals).
	CumExpected *big.Rat
	Notional    *big.Rat
	Reports     int
	Fills       int
	LastReport  string

	Checks  []checks.Result
	Verdict string

	seenExec map[string]bool
}

// AvgExpected is the fills' weighted mean (nil before any fill).
func (o *Order) AvgExpected() *big.Rat {
	if o.CumExpected.Sign() == 0 {
		return nil
	}
	return new(big.Rat).Quo(o.Notional, o.CumExpected)
}

// LeavesExpected is OrderQty - Cum while working, 0 once terminal.
func (o *Order) LeavesExpected() *big.Rat {
	if IsTerminal(o.State) {
		return new(big.Rat)
	}
	l := new(big.Rat).Sub(o.OrderQty, o.CumExpected)
	if l.Sign() < 0 {
		return new(big.Rat)
	}
	return l
}

// Terminal reports whether the order is over.
func (o *Order) Terminal() bool { return IsTerminal(o.State) }

func ratStr(r *big.Rat, scale int) string {
	if r == nil {
		return ""
	}
	if r.IsInt() && scale == 0 {
		return r.Num().String()
	}
	return checks.FormatRat(r, scale)
}

// Snapshot is the order as the evidence "order" field records it.
func (o *Order) Snapshot() map[string]any {
	avg := ""
	if a := o.AvgExpected(); a != nil {
		avg = checks.FormatRat(a, 4)
	}
	statuses := map[string]string{}
	for _, c := range o.Checks {
		statuses[c.Name] = c.Status
	}
	var reqs []map[string]any
	for _, r := range o.Requests {
		reqs = append(reqs, map[string]any{"msg_type": r.MsgType, "cl_ord_id": r.ClOrdID,
			"orig_cl_ord_id": r.OrigClOrdID, "seq": r.Seq, "injected": r.Injected,
			"answers": r.Answers, "rejected": r.Rejected})
	}
	return map[string]any{
		"cl_ord_id": o.Current, "root_cl_ord_id": o.Root, "cl_ord_ids": o.ClOrdIDs,
		"order_id": o.OrderID, "symbol": o.Symbol, "side": o.Side, "ord_type": o.OrdType,
		"price": o.Price, "order_qty": ratStr(o.OrderQty, 0), "state": o.State, "history": o.History,
		"cum_qty_reported": o.CumReported, "leaves_qty_reported": o.LeavesReported, "avg_px_reported": o.AvgReported,
		"cum_qty_expected": ratStr(o.CumExpected, 0), "leaves_qty_expected": ratStr(o.LeavesExpected(), 0),
		"avg_px_expected": avg, "reports": o.Reports, "fills": o.Fills,
		"requests": reqs, "checks": statuses, "verdict": o.Verdict,
	}
}

// ------------------------------------------------------------ manager

// Options configure a Manager.
type Options struct {
	Session  string // session id, used as the message "session" for checks
	Profile  *profile.Profile
	IDs      *IDs
	Clock    clock.Clock
	Order    profile.OrderOptions
	Account  string
	SenderID string // our SenderCompID
	// AnswerGrace: live requests_answered does not WARN for requests younger
	// than this (A3 3.3). Zero means no grace.
	AnswerGrace time.Duration
}

// Manager is the pure order manager. Not safe for concurrent use; the
// transport serializes calls with its session lock.
type Manager struct {
	opt       Options
	orders    []*Order
	byClOrdID map[string]*Order
	requests  map[string]*Request // by ClOrdID of the request
	bySeq     map[int]*Request
	pending   map[string]*Request // prepared, not yet seen on the wire
	history   []*checks.Message
	index     int
	last      *Order
}

// NewManager returns a manager.
func NewManager(opt Options) *Manager {
	if opt.Clock == nil {
		opt.Clock = clock.SystemClock{}
	}
	return &Manager{opt: opt, byClOrdID: map[string]*Order{}, requests: map[string]*Request{},
		bySeq: map[int]*Request{}, pending: map[string]*Request{}}
}

// Orders returns every order, oldest first.
func (m *Manager) Orders() []*Order { return m.orders }

// Last is the most recently sent order (the REPL's "last").
func (m *Manager) Last() *Order { return m.last }

// History is every order-relevant message seen, both directions.
func (m *Manager) History() []*checks.Message { return m.history }

// Find resolves "last", any ClOrdID of a chain, or an OrderID.
func (m *Manager) Find(ref string) (*Order, error) {
	if ref == "" || strings.EqualFold(ref, "last") {
		if m.last == nil {
			return nil, fmt.Errorf("no order sent yet")
		}
		return m.last, nil
	}
	if o, ok := m.byClOrdID[ref]; ok {
		return o, nil
	}
	for _, o := range m.orders {
		if o.OrderID == ref {
			return o, nil
		}
	}
	return nil, fmt.Errorf("unknown order %q", ref)
}

func (m *Manager) now() string { return codec.FormatTime(m.opt.Clock.Now()) }

// NewOrder validates spec, allocates a ClOrdID, registers the order and
// returns the D body to send. If the send fails, call Forget.
func (m *Manager) NewOrder(spec Spec) (*Order, []codec.Field, error) {
	side, ordType, tif, err := spec.Validate()
	if err != nil {
		return nil, nil, err
	}
	qty, _ := positiveWhole(spec.Qty)
	id := m.opt.IDs.Next()
	o := &Order{Root: id, ClOrdIDs: []string{id}, Current: id, Symbol: spec.Symbol, Side: side,
		OrdType: ordType, Price: spec.Price, OrderQty: qty, TIF: tif, State: Sent,
		History: []string{Sent}, CumExpected: new(big.Rat), Notional: new(big.Rat), Verdict: checks.PASS,
		seenExec: map[string]bool{}}
	if ordType != profile.OrdTypeLimit {
		o.Price = ""
	}
	body := m.opt.Profile.RenderNewOrder(profile.NewOrder{
		ClOrdID: id, Symbol: spec.Symbol, Side: side, OrdType: ordType, OrderQty: spec.Qty,
		Price: o.Price, TimeInForce: tif, Account: firstNonEmpty(spec.Account, m.opt.Account), TransactTime: m.now(),
	}, m.opt.Order)
	req := &Request{MsgType: "D", ClOrdID: id}
	o.Requests = append(o.Requests, req)
	m.orders = append(m.orders, o)
	m.byClOrdID[id] = o
	m.requests[id] = req
	m.pending[id] = req
	m.last = o
	return o, body, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Cancel prepares an F for the order ref resolves to.
func (m *Manager) Cancel(ref string) (*Order, []codec.Field, error) { return m.CancelOpt(ref, false) }

// CancelOpt is Cancel; allowTerminal skips the client-side "already over"
// refusal, to provoke a too-late cancel reject deliberately.
func (m *Manager) CancelOpt(ref string, allowTerminal bool) (*Order, []codec.Field, error) {
	o, err := m.Find(ref)
	if err != nil {
		return nil, nil, err
	}
	if o.Terminal() && !allowTerminal {
		return nil, nil, fmt.Errorf("order %s is %s", o.Current, o.State)
	}
	id := m.opt.IDs.Next()
	body := m.opt.Profile.RenderCancel(profile.CancelRequest{
		OrigClOrdID: o.Current, ClOrdID: id, OrderID: o.OrderID, Symbol: o.Symbol, Side: o.Side,
		TransactTime: m.now(), OrderQty: ratStr(o.OrderQty, 0),
	})
	m.chainRequest(o, &Request{MsgType: "F", ClOrdID: id, OrigClOrdID: o.Current})
	return o, body, nil
}

// Replace prepares a G changing quantity (and price, for a limit order).
func (m *Manager) Replace(ref, qty, price string) (*Order, []codec.Field, error) {
	o, err := m.Find(ref)
	if err != nil {
		return nil, nil, err
	}
	if o.Terminal() {
		return nil, nil, fmt.Errorf("order %s is %s", o.Current, o.State)
	}
	if _, ok := positiveWhole(qty); !ok {
		return nil, nil, fmt.Errorf("quantity must be a positive whole number, got %q", qty)
	}
	if o.OrdType == profile.OrdTypeLimit {
		if price == "" {
			price = o.Price
		}
		if _, ok := positiveDec(price); !ok {
			return nil, nil, fmt.Errorf("price must be positive, got %q", price)
		}
	} else if price != "" {
		return nil, nil, fmt.Errorf("a market order takes no price")
	}
	id := m.opt.IDs.Next()
	body := m.opt.Profile.RenderReplace(profile.ReplaceRequest{
		OrigClOrdID: o.Current, ClOrdID: id, OrderID: o.OrderID, Symbol: o.Symbol, Side: o.Side,
		TransactTime: m.now(), OrderQty: qty, OrdType: o.OrdType, Price: price,
	}, m.opt.Order)
	m.chainRequest(o, &Request{MsgType: "G", ClOrdID: id, OrigClOrdID: o.Current})
	return o, body, nil
}

func (m *Manager) chainRequest(o *Order, r *Request) {
	o.Requests = append(o.Requests, r)
	o.ClOrdIDs = append(o.ClOrdIDs, r.ClOrdID)
	o.Current = r.ClOrdID
	m.byClOrdID[r.ClOrdID] = o
	m.requests[r.ClOrdID] = r
	m.pending[r.ClOrdID] = r
}

// Forget undoes a prepared request that could not be sent.
func (m *Manager) Forget(o *Order, clOrdID string) {
	delete(m.pending, clOrdID)
	delete(m.requests, clOrdID)
	delete(m.byClOrdID, clOrdID)
	if o == nil {
		return
	}
	for i, r := range o.Requests {
		if r.ClOrdID == clOrdID {
			o.Requests = append(o.Requests[:i], o.Requests[i+1:]...)
			break
		}
	}
	for i, id := range o.ClOrdIDs {
		if id == clOrdID {
			o.ClOrdIDs = append(o.ClOrdIDs[:i], o.ClOrdIDs[i+1:]...)
			break
		}
	}
	if len(o.Requests) == 0 {
		delete(m.byClOrdID, o.Root)
		for i, x := range m.orders {
			if x == o {
				m.orders = append(m.orders[:i], m.orders[i+1:]...)
				break
			}
		}
		if m.last == o {
			m.last = nil
			if n := len(m.orders); n > 0 {
				m.last = m.orders[n-1]
			}
		}
		return
	}
	o.Current = m.currentOf(o)
}

// currentOf is the latest request of the chain not known to be rejected.
func (m *Manager) currentOf(o *Order) string {
	for i := len(o.Requests) - 1; i >= 0; i-- {
		if !o.Requests[i].Rejected {
			return o.Requests[i].ClOrdID
		}
	}
	return o.Root
}

func (m *Manager) record(msg *codec.Message, direction string, injected bool) *checks.Message {
	m.index++
	cm := checks.FromCodec(msg, direction, m.opt.Session, m.opt.Clock.Now(), m.index, injected)
	m.history = append(m.history, cm)
	return cm
}

// ------------------------------------------------------ session hooks

// OnAppSent records an outbound application message (session.SentObserver).
func (m *Manager) OnAppSent(send session.Send) {
	mt := send.MsgType
	if mt != "D" && mt != "F" && mt != "G" {
		m.record(send.Msg, checks.DirOut, send.Injected)
		return
	}
	m.record(send.Msg, checks.DirOut, send.Injected)
	if send.Msg.Value(43) == "Y" {
		return // a replay of a request we already track
	}
	id := send.Msg.Value(11)
	if req, ok := m.pending[id]; ok && req.MsgType == mt && !send.Injected {
		delete(m.pending, id)
		req.Seq, req.sent = send.Seq, true
		m.bySeq[send.Seq] = req
		return
	}
	// Sent some other way (SendRaw): track it, linked to a known order when
	// its ClOrdID or OrigClOrdID is ours.
	req := &Request{MsgType: mt, ClOrdID: id, OrigClOrdID: send.Msg.Value(41), Seq: send.Seq, Injected: send.Injected, sent: true}
	m.bySeq[send.Seq] = req
	if _, dup := m.requests[id]; !dup {
		m.requests[id] = req
	}
	if o := m.byClOrdID[id]; o != nil {
		o.Requests = append(o.Requests, req)
	} else if o := m.byClOrdID[req.OrigClOrdID]; o != nil && req.OrigClOrdID != "" {
		o.Requests = append(o.Requests, req)
		o.ClOrdIDs = append(o.ClOrdIDs, id)
		m.byClOrdID[id] = o
	}
}

// OnAppMessage handles inbound application messages (session.App).
func (m *Manager) OnAppMessage(msg *codec.Message) []session.Action {
	switch msg.MsgType() {
	case "8":
		return m.onExecutionReport(msg)
	case "9":
		return m.onCancelReject(msg)
	case "j":
		m.record(msg, checks.DirIn, false)
		return m.onRefReject(msg, "business reject")
	}
	return nil
}

// OnSessionReject handles inbound 35=3 (session.RejectObserver).
func (m *Manager) OnSessionReject(msg *codec.Message) []session.Action {
	m.record(msg, checks.DirIn, false)
	return m.onRefReject(msg, "session reject")
}

func (m *Manager) onRefReject(msg *codec.Message, what string) []session.Action {
	ref, ok := checks.PyInt(msg.Value(45))
	if !ok {
		return nil
	}
	req, ok := m.bySeq[ref]
	if !ok {
		return nil
	}
	line := fmt.Sprintf("%s 35=%s 45=%d 373=%s 380=%s 58=%s", what, msg.MsgType(), ref, msg.Value(373), msg.Value(380), msg.Value(58))
	req.Answers = append(req.Answers, line)
	req.Rejected = true
	o := m.byClOrdID[req.ClOrdID]
	if o == nil {
		return []session.Action{session.Evidence{Event: "order request rejected",
			Detail: fmt.Sprintf("35=%s seq=%d 11=%s: %s", req.MsgType, req.Seq, req.ClOrdID, line), Level: session.Warning}}
	}
	if req.MsgType == "D" && req.ClOrdID == o.Root && o.State == Sent {
		m.setState(o, Rejected)
	}
	o.Current = m.currentOf(o)
	return m.afterReport(o, fmt.Sprintf("<< %s for 35=%s seq=%d 11=%s: %s", strings.ToUpper(what), req.MsgType, req.Seq, req.ClOrdID, msg.Value(58)))
}

func (m *Manager) setState(o *Order, st string) {
	if o.State != st {
		o.State = st
		o.History = append(o.History, st)
	}
}

func (m *Manager) onExecutionReport(msg *codec.Message) []session.Action {
	cm := m.record(msg, checks.DirIn, false)
	id := msg.Value(11)
	o := m.byClOrdID[id]
	if o == nil {
		o = m.byClOrdID[msg.Value(41)]
	}
	if o == nil {
		return []session.Action{session.Evidence{Event: "unsolicited or unknown order",
			Detail: fmt.Sprintf("ExecutionReport for 11=%s 41=%s 37=%s we never sent; logged and checkable offline", id, msg.Value(41), msg.Value(37)),
			Level:  session.Warning}}
	}
	execID := msg.Value(17)
	replay := cm.PossDup() && execID != "" && o.seenExec[execID]
	if replay {
		return m.afterReport(o, "<< "+m.describe(msg)+"  [replay]")
	}
	if execID != "" {
		o.seenExec[execID] = true
	}
	orderID := msg.Value(37)
	req := m.requests[id]
	status := msg.Value(39)

	// A report on a different OrderID for a ClOrdID we sent twice is the
	// answer to the duplicate, not news about the order we know.
	if o.OrderID != "" && orderID != "" && orderID != o.OrderID && status == "8" {
		if dup := m.latestDuplicate(o, id); dup != nil {
			dup.Answers = append(dup.Answers, m.describe(msg))
			dup.Rejected = true
			actions := []session.Action{session.Evidence{Event: "duplicate request rejected",
				Detail: fmt.Sprintf("35=%s seq=%d 11=%s answered on OrderID %s (ours is %s): 103=%s %s",
					dup.MsgType, dup.Seq, id, orderID, o.OrderID, msg.Value(103), msg.Value(58)), Level: session.Warning}}
			return append(actions, m.afterReport(o, "<< "+m.describe(msg)+"  [answers duplicate request]")...)
		}
	}

	o.Reports++
	if req != nil {
		req.Answers = append(req.Answers, m.describe(msg))
		if status == "8" {
			req.Rejected = true
		}
	}
	if o.OrderID == "" && orderID != "" && !strings.EqualFold(orderID, "NONE") {
		o.OrderID = orderID
	}
	et := msg.Value(150)
	if checks.FillExecTypes[et] {
		q, okq := checks.ParseDec(msg.Value(32))
		px, okp := checks.ParseDec(msg.Value(31))
		if okq && okp && q.R.Sign() > 0 {
			o.Fills++
			o.CumExpected.Add(o.CumExpected, q.R)
			o.Notional.Add(o.Notional, new(big.Rat).Mul(q.R, px.R))
		}
	}
	if et == "5" { // Replaced
		o.History = append(o.History, fmt.Sprintf("%s %s->%s", Replaced, msg.Value(41), id))
		if q, ok := positiveWhole(msg.Value(38)); ok {
			o.OrderQty = q
		}
		if px := msg.Value(44); px != "" {
			o.Price = px
		}
	}
	if status != "8" || id == o.Root {
		o.Accepted = id
	}
	if st, ok := statusState[status]; ok {
		if et == "8" && id != o.Root {
			// A rejected replace/cancel request does not end the order.
		} else {
			m.setState(o, st)
		}
	}
	o.CumReported, o.LeavesReported, o.AvgReported = msg.Value(14), msg.Value(151), msg.Value(6)
	o.Current = m.currentOf(o)
	return m.afterReport(o, "<< "+m.describe(msg))
}

func (m *Manager) latestDuplicate(o *Order, id string) *Request {
	var found []*Request
	for _, r := range o.Requests {
		if r.ClOrdID == id {
			found = append(found, r)
		}
	}
	for i := len(found) - 1; i >= 1; i-- {
		if !found[i].Answered() {
			return found[i]
		}
	}
	return nil
}

func (m *Manager) onCancelReject(msg *codec.Message) []session.Action {
	m.record(msg, checks.DirIn, false)
	id := msg.Value(11)
	req := m.requests[id]
	o := m.byClOrdID[id]
	if o == nil {
		o = m.byClOrdID[msg.Value(41)]
	}
	if req != nil {
		req.Answers = append(req.Answers, m.describe(msg))
		req.Rejected = true
	}
	if o == nil {
		if req != nil {
			return []session.Action{session.Evidence{Event: "order request rejected",
				Detail: fmt.Sprintf("35=%s seq=%d 11=%s: %s", req.MsgType, req.Seq, id, m.describe(msg)), Level: session.Warning}}
		}
		return []session.Action{session.Evidence{Event: "unsolicited or unknown order",
			Detail: fmt.Sprintf("OrderCancelReject for 11=%s 41=%s we never sent; logged and checkable offline", id, msg.Value(41)),
			Level:  session.Warning}}
	}
	o.Current = m.currentOf(o)
	return m.afterReport(o, "<< "+m.describe(msg))
}

// describe is one human line for a report.
func (m *Manager) describe(msg *codec.Message) string {
	p := m.opt.Profile
	switch msg.MsgType() {
	case "8":
		s := fmt.Sprintf("ER 11=%s 37=%s 150=%s(%s) 39=%s(%s)", msg.Value(11), msg.Value(37),
			msg.Value(150), p.ExecTypeName(msg.Value(150)), msg.Value(39), p.OrdStatusName(msg.Value(39)))
		if q := msg.Value(32); q != "" && q != "0" {
			s += fmt.Sprintf(" last=%s@%s", q, msg.Value(31))
		}
		s += fmt.Sprintf(" cum=%s leaves=%s avg=%s", msg.Value(14), msg.Value(151), msg.Value(6))
		if r := msg.Value(103); r != "" {
			s += " 103=" + r
		}
		if t := msg.Value(58); t != "" {
			s += fmt.Sprintf(" %q", t)
		}
		return s
	case "9":
		return fmt.Sprintf("CXLREJ 11=%s 41=%s 37=%s 39=%s 434=%s 102=%s %q", msg.Value(11), msg.Value(41),
			msg.Value(37), msg.Value(39), msg.Value(434), msg.Value(102), msg.Value(58))
	}
	return "35=" + msg.MsgType()
}

// Chain builds the order's chain from everything seen so far.
func (m *Manager) Chain(o *Order) *checks.Chain {
	return checks.BuildChain(m.history, o.Root, "")
}

// afterReport runs the checks on the chain so far, logs every check whose
// status changed (WARN/FAIL at once, a recovery to PASS as INFO), and
// records the shadow snapshot.
func (m *Manager) afterReport(o *Order, line string) []session.Action {
	chain := m.Chain(o)
	if m.opt.AnswerGrace > 0 {
		chain.Checks = checks.WithAnswerGrace(chain, chain.Checks, m.opt.Clock.Now(), m.opt.AnswerGrace)
	}
	prev := map[string]string{}
	for _, c := range o.Checks {
		prev[c.Name] = c.Status
	}
	o.Checks = chain.Checks
	o.Verdict = chain.Verdict()
	o.LastReport = line
	var actions []session.Action
	for _, c := range chain.Checks {
		before, seen := prev[c.Name]
		if (!seen && c.Status == checks.PASS) || before == c.Status {
			continue
		}
		lvl := session.Info
		switch c.Status {
		case checks.WARN:
			lvl = session.Warning
		case checks.FAIL:
			lvl = session.Error
		}
		actions = append(actions, session.Evidence{Event: "check " + c.Name,
			Detail: fmt.Sprintf("%s %s: %s (order %s)", c.Status, c.Name, c.Explanation, o.Root), Level: lvl})
	}
	report := session.Evidence{Event: "order report",
		Detail: fmt.Sprintf("%s  -> %s cum=%s leaves=%s  checks: %s", line, o.State,
			ratStr(o.CumExpected, 0), ratStr(o.LeavesExpected(), 0), o.Verdict),
		Level: session.Info, Order: o.Snapshot()}
	return append([]session.Action{report}, actions...)
}

// CheckLine is the one-line check summary printed under each report.
func CheckLine(o *Order) string {
	var bad []string
	for _, c := range o.Checks {
		if c.Status != checks.PASS {
			bad = append(bad, c.Status+" "+c.Name)
		}
	}
	if len(bad) == 0 {
		return fmt.Sprintf("checks: %s (%d/11 PASS)", o.Verdict, len(o.Checks))
	}
	return fmt.Sprintf("checks: %s (%s)", o.Verdict, strings.Join(bad, "; "))
}

// Status lines for the REPL's "status".
func (m *Manager) Status() []string {
	var out []string
	out = append(out, fmt.Sprintf("%-28s %-22s %-6s %-5s %-4s %8s %8s %8s %10s %s",
		"CLORDID", "ORDERID", "SYMBOL", "SIDE", "TYPE", "QTY", "CUM", "LEAVES", "AVG", "STATE / CHECKS"))
	for _, o := range m.orders {
		avg := o.AvgReported
		if avg == "" {
			avg = "-"
		}
		typ := "MKT"
		if o.OrdType == profile.OrdTypeLimit {
			typ = "LMT"
		}
		out = append(out, fmt.Sprintf("%-28s %-22s %-6s %-5s %-4s %8s %8s %8s %10s %s / %s",
			o.Current, dash(o.OrderID), o.Symbol, sideNames[o.Side], typ, ratStr(o.OrderQty, 0),
			ratStr(o.CumExpected, 0), ratStr(o.LeavesExpected(), 0), avg, o.State, o.Verdict))
	}
	return out
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// SortedStates is a helper for tests.
func SortedStates(os []*Order) []string {
	var out []string
	for _, o := range os {
		out = append(out, o.State)
	}
	sort.Strings(out)
	return out
}

var _ = strconv.Itoa
