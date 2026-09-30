package cert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/fix/transport"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

// ------------------------------------------------------------ history

// Entry is one message that crossed the wire.
type Entry struct {
	Index int
	Dir   string // in | out
	Msg   *codec.Message
	TS    time.Time
}

// History is every framed message in and out, in wire order, including the
// ones the session deliberately ignores (PossDup replays).
type History struct {
	mu      sync.Mutex
	clock   clock.Clock
	entries []Entry
}

// NewHistory returns an empty history stamped by clk.
func NewHistory(clk clock.Clock) *History { return &History{clock: clk} }

// Add records a message.
func (h *History) Add(dir string, msg *codec.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, Entry{Index: len(h.entries), Dir: dir, Msg: msg, TS: h.clock.Now()})
}

// Since returns the entries from index i on.
func (h *History) Since(i int) []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i >= len(h.entries) {
		return nil
	}
	return append([]Entry(nil), h.entries[i:]...)
}

// Len is the number of entries.
func (h *History) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}

// ------------------------------------------------------------ driver

// SessionInfo is what suites may refer to as built-in vars.
type SessionInfo struct {
	SessionID    string
	FixVersion   string
	SenderCompID string
	TargetCompID string
	HeartbeatSec int
}

// OrderInfo is a runner's view of one order (managed or raw).
type OrderInfo struct {
	Root     string
	Current  string
	ClOrdIDs []string
	OrderID  string
	Seqs     []int // MsgSeqNums of its requests
	Terminal bool
	State    string
}

// Offsets are positions in the run's FIX log and evidence file, used to cut
// a case's slice out of them.
type Offsets struct {
	FixPath string
	FixOff  int64
	EvPath  string
	EvOff   int64
}

// Driver is everything the runner does to a session. The live driver runs a
// real agent; unit tests use a fake with a FakeClock.
type Driver interface {
	Info() SessionInfo
	Connected() bool
	Connect(reset bool) error
	Logout() error
	Abrupt() error
	// Dropped returns a non-empty reason (once) if the session went down
	// without the runner asking.
	Dropped() string
	TestRequest() (string, error)
	SkipOutboundSeq(n int) error
	ResendRequest(begin, end int) error
	ResendStored(seq int) error
	NewOrder(spec order.Spec) (string, error)
	Cancel(root string, allowTerminal bool) (string, error)
	Replace(root, qty, price string) (string, error)
	SendRaw(fields []codec.Field) (int, error)
	Order(root string) (OrderInfo, bool)
	Chain(clOrdID string) *checks.Chain
	History() *History
	NextIn() int
	NewID() string
	Mark(event, detail string)
	Offsets() Offsets
}

// ------------------------------------------------------------ live

// Live drives a real agent over TCP.
type Live struct {
	A       *agent.Agent
	hist    *History
	ctx     context.Context
	mu      sync.Mutex
	running bool
	runCh   chan transport.Result
	last    *transport.Result
	intent  bool   // the runner itself took the session down
	dropped string // unexpected drop not yet reported
	idN     int
}

// NewLive wires a live driver. The agent must be built with OnWire set to
// the returned history's Add (see NewLiveAgent).
func NewLive(ctx context.Context, a *agent.Agent, hist *History) *Live {
	return &Live{A: a, hist: hist, ctx: ctx}
}

// Info implements Driver.
func (l *Live) Info() SessionInfo {
	sc := l.A.Opt.Session
	return SessionInfo{SessionID: sc.ID, FixVersion: sc.FixVersion, SenderCompID: sc.SenderCompID,
		TargetCompID: sc.TargetCompID, HeartbeatSec: sc.HeartbeatSec}
}

func (l *Live) poll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running {
		return
	}
	select {
	case r := <-l.runCh:
		l.running = false
		l.last = &r
		if !l.intent {
			l.dropped = agent.Explain(r, l.A.Opt.Session)
		}
	default:
	}
}

// Connected implements Driver.
func (l *Live) Connected() bool {
	l.poll()
	l.mu.Lock()
	running := l.running
	l.mu.Unlock()
	return running && l.A.Init.Snapshot().State == session.Active
}

// Dropped implements Driver.
func (l *Live) Dropped() string {
	l.poll()
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.dropped
	l.dropped = ""
	return d
}

// LastResult is the last Run result (nil while connected).
func (l *Live) LastResult() *transport.Result {
	l.poll()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

// Connect implements Driver: start a connection and wait for its Logon.
func (l *Live) Connect(reset bool) error {
	l.poll()
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return errors.New("already connected")
	}
	l.intent = false
	l.running = true
	l.runCh = make(chan transport.Result, 1)
	ch := l.runCh
	l.mu.Unlock()
	before := l.A.Init.Snapshot().Logons
	l.A.Init.Rearm()
	if reset {
		l.A.Session.RequestReset()
	}
	go func() { ch <- l.A.Run(l.ctx) }()
	deadline := time.Now().Add(l.A.Opt.Session.LogonTimeout() + 5*time.Second)
	for time.Now().Before(deadline) {
		snap := l.A.Init.Snapshot()
		if snap.Logons > before && snap.State == session.Active {
			return nil
		}
		l.mu.Lock()
		select {
		case r := <-ch:
			l.running = false
			l.last = &r
			l.mu.Unlock()
			return fmt.Errorf("logon failed: %s", agent.Explain(r, l.A.Opt.Session))
		default:
		}
		l.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	l.A.Init.Stop("logon wait timed out")
	return fmt.Errorf("no logon within %s", l.A.Opt.Session.LogonTimeout()+5*time.Second)
}

func (l *Live) waitDown(timeout time.Duration) (*transport.Result, error) {
	l.mu.Lock()
	ch, running := l.runCh, l.running
	l.mu.Unlock()
	if !running {
		return l.last, nil
	}
	select {
	case r := <-ch:
		l.mu.Lock()
		l.running = false
		l.last = &r
		l.mu.Unlock()
		return &r, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("session did not end within %s", timeout)
	}
}

// Logout implements Driver: a clean Logout both ways, or an error.
func (l *Live) Logout() error {
	l.mu.Lock()
	l.intent = true
	l.mu.Unlock()
	if err := l.A.Init.Logout("OrderEcho cert runner: logout step"); err != nil {
		return err
	}
	r, err := l.waitDown(l.A.Opt.Session.LogoutTimeout() + 5*time.Second)
	if err != nil {
		return err
	}
	if r == nil || !r.Outcome.CleanLogout {
		return fmt.Errorf("logout was not clean: %s", agent.Explain(*r, l.A.Opt.Session))
	}
	return nil
}

// Abrupt implements Driver: drop the socket without a Logout.
func (l *Live) Abrupt() error {
	l.mu.Lock()
	l.intent = true
	l.mu.Unlock()
	l.A.Init.Stop("cert runner: disconnect_abrupt step")
	_, err := l.waitDown(10 * time.Second)
	return err
}

// Close logs out (if connected) at the end of a run.
func (l *Live) Close() (*transport.Result, error) {
	if l.Connected() {
		l.mu.Lock()
		l.intent = true
		l.mu.Unlock()
		if err := l.A.Init.Logout("OrderEcho cert runner: run complete"); err == nil {
			return l.waitDown(l.A.Opt.Session.LogoutTimeout() + 5*time.Second)
		}
	}
	l.A.Init.Stop("cert runner: run complete")
	return l.waitDown(10 * time.Second)
}

// TestRequest implements Driver.
func (l *Live) TestRequest() (string, error) { return l.A.Init.TestRequest() }

// SkipOutboundSeq implements Driver.
func (l *Live) SkipOutboundSeq(n int) error { return l.A.Init.SkipOutboundSeq(n) }

// ResendRequest implements Driver.
func (l *Live) ResendRequest(begin, end int) error {
	return l.A.Init.Exec(func(s *session.Session) ([]session.Action, error) { return s.SendResendRequest(begin, end) })
}

// ResendStored implements Driver.
func (l *Live) ResendStored(seq int) error {
	return l.A.Init.Exec(func(s *session.Session) ([]session.Action, error) { return s.ResendStored(seq) })
}

// NewOrder implements Driver.
func (l *Live) NewOrder(spec order.Spec) (string, error) {
	o, err := l.A.NewOrder(spec)
	if err != nil {
		return "", err
	}
	return o.Root, nil
}

// Cancel implements Driver.
func (l *Live) Cancel(root string, allowTerminal bool) (string, error) {
	var id string
	var err error
	if allowTerminal {
		_, id, err = l.A.CancelAny(root)
	} else {
		_, id, err = l.A.Cancel(root)
	}
	return id, err
}

// Replace implements Driver.
func (l *Live) Replace(root, qty, price string) (string, error) {
	_, id, err := l.A.Replace(root, qty, price)
	return id, err
}

// SendRaw implements Driver: arbitrary fields through the normal send path
// (recorded as injected), returning the MsgSeqNum used.
func (l *Live) SendRaw(fields []codec.Field) (int, error) {
	seq := 0
	err := l.A.Init.Exec(func(s *session.Session) ([]session.Action, error) {
		acts, err := s.SendRaw(fields)
		for _, a := range acts {
			if snd, ok := a.(session.Send); ok {
				seq = snd.Seq
			}
		}
		return acts, err
	})
	return seq, err
}

// Order implements Driver.
func (l *Live) Order(root string) (OrderInfo, bool) {
	var info OrderInfo
	found := false
	l.A.With(func(m *order.Manager) {
		o, err := m.Find(root)
		if err != nil {
			return
		}
		found = true
		info = OrderInfo{Root: o.Root, Current: o.Current, ClOrdIDs: append([]string(nil), o.ClOrdIDs...),
			OrderID: o.OrderID, Terminal: o.Terminal(), State: o.State}
		for _, r := range o.Requests {
			if r.Seq > 0 {
				info.Seqs = append(info.Seqs, r.Seq)
			}
		}
	})
	return info, found
}

// Chain implements Driver: offline semantics over everything seen.
func (l *Live) Chain(clOrdID string) *checks.Chain {
	var c *checks.Chain
	l.A.With(func(m *order.Manager) { c = checks.BuildChain(m.History(), clOrdID, "") })
	return c
}

// History implements Driver.
func (l *Live) History() *History { return l.hist }

// NextIn implements Driver.
func (l *Live) NextIn() int { return l.A.Init.Snapshot().NextIn }

// NewID implements Driver: a ClOrdID for raw messages, unique in the run.
func (l *Live) NewID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.idN++
	return fmt.Sprintf("%s-%s-R%d", l.A.Opt.Session.ClOrdIDPrefix, l.A.RunID, l.idN)
}

// Mark implements Driver: an evidence event (and engine log line).
func (l *Live) Mark(event, detail string) {
	l.A.Evidence.Event(l.A.Opt.Session.ID, event, detail, false)
	l.A.EngineLog.Info(l.A.Opt.Session.ID, event+": "+detail)
}

// Offsets implements Driver.
func (l *Live) Offsets() Offsets {
	o := Offsets{FixPath: l.A.FixLog.PathFor(time.Now()), EvPath: l.A.Evidence.Path}
	if st, err := os.Stat(o.FixPath); err == nil {
		o.FixOff = st.Size()
	}
	if st, err := os.Stat(o.EvPath); err == nil {
		o.EvOff = st.Size()
	}
	return o
}
