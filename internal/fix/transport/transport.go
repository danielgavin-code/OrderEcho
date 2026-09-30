// Package transport is the thin TCP initiator around the pure session core.
//
// It owns the socket, the decoder buffer, the timer tick and reconnects; the
// session owns every decision. All calls into the session happen under one
// mutex, so the reader, the ticker and external commands (Logout,
// TestRequest, the test-only injections) never race.
//
// Every message in or out, and every discarded frame, goes to the FIX log and
// the evidence file; every Evidence action goes to the evidence file and the
// engine log at the level the session chose.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/evidence"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/logs"
)

const (
	defaultTick  = 200 * time.Millisecond
	writeTimeout = 5 * time.Second
	readChunk    = 64 * 1024
)

// ErrNotConnected is returned by commands while there is no live connection.
var ErrNotConnected = errors.New("not connected")

// Options configure an Initiator.
type Options struct {
	Addr              string
	Session           *session.Session
	Clock             clock.Clock
	Evidence          *evidence.Writer
	FixLog            *logs.FixLog
	EngineLog         *logs.EngineLog
	Reconnect         bool
	ReconnectInterval time.Duration
	DialTimeout       time.Duration
	Tick              time.Duration
	// OnEvidence, if set, is called (under the session lock) for every
	// Evidence action, after it has been recorded. Keep it quick.
	OnEvidence func(session.Evidence)
	// OnWire, if set, sees every framed message as it crosses the wire:
	// inbound before the session handles it ("in"), outbound as written
	// ("out"). Called under the session lock. The cert runner uses it to see
	// messages the session deliberately ignores (PossDup replays).
	OnWire func(direction string, msg *codec.Message)
}

// Snapshot is a consistent view of the session for callers outside the loop.
type Snapshot struct {
	Connected        bool
	State            session.State
	NextOut          int
	NextIn           int
	HeartBtInt       int
	PendingTestReqID string
	Logons           int
	Connects         int
	Outcome          session.Outcome
}

// Result is how Run ended.
type Result struct {
	Connects int
	Logons   int
	// Outcome is the last connection's outcome.
	Outcome session.Outcome
	// DialErr is set when the last attempt could not connect at all.
	DialErr error
	// Stopped is set when Run ended because the caller asked it to (Stop,
	// Logout or context cancellation).
	Stopped bool
}

// Initiator connects out, runs one session over TCP, and reconnects if asked.
type Initiator struct {
	opts Options
	id   string

	mu       sync.Mutex
	sess     *session.Session
	conn     net.Conn
	closing  bool
	stopping bool // no reconnect after the current connection
	connects int
}

// New builds an Initiator.
func New(opts Options) *Initiator {
	if opts.Tick <= 0 {
		opts.Tick = defaultTick
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.ReconnectInterval <= 0 {
		opts.ReconnectInterval = 5 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = clock.SystemClock{}
	}
	return &Initiator{opts: opts, id: opts.Session.Config().SessionID, sess: opts.Session}
}

// ------------------------------------------------------------- logging

func (in *Initiator) info(msg string)  { in.opts.EngineLog.Info(in.id, msg) }
func (in *Initiator) warn(msg string)  { in.opts.EngineLog.Warning(in.id, msg) }
func (in *Initiator) debug(msg string) { in.opts.EngineLog.Debug(in.id, msg) }

// --------------------------------------------------------------- run

// Run connects and keeps the session going until it ends for good: a clean
// logout, a refused logon, Stop/ctx, or any drop when reconnect is off.
func (in *Initiator) Run(ctx context.Context) Result {
	var res Result
	for {
		if ctx.Err() != nil || in.isStopping() {
			res.Stopped = true
			break
		}
		in.info(fmt.Sprintf("Connecting to %s", in.opts.Addr))
		dialer := net.Dialer{Timeout: in.opts.DialTimeout}
		conn, err := dialer.DialContext(ctx, "tcp", in.opts.Addr)
		if err != nil {
			res.DialErr = err
			in.warn(fmt.Sprintf("Connect to %s failed: %v", in.opts.Addr, err))
			in.opts.Evidence.Event(in.id, "connect failed", err.Error(), false)
			if !in.opts.Reconnect || ctx.Err() != nil {
				break
			}
			if !in.sleep(ctx, in.opts.ReconnectInterval) {
				res.Stopped = true
				break
			}
			continue
		}
		res.DialErr = nil
		outcome := in.runConnection(ctx, conn)
		res.Outcome = outcome

		if ctx.Err() != nil || in.isStopping() {
			res.Stopped = true
			break
		}
		if outcome.Refused || outcome.CleanLogout {
			break
		}
		if !in.opts.Reconnect {
			break
		}
		in.info(fmt.Sprintf("Reconnecting in %s", in.opts.ReconnectInterval))
		if !in.sleep(ctx, in.opts.ReconnectInterval) {
			res.Stopped = true
			break
		}
	}
	in.mu.Lock()
	res.Connects = in.connects
	res.Logons = in.sess.Logons()
	in.mu.Unlock()
	return res
}

func (in *Initiator) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return !in.isStopping()
	}
}

func (in *Initiator) isStopping() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.stopping
}

func (in *Initiator) runConnection(ctx context.Context, conn net.Conn) session.Outcome {
	in.mu.Lock()
	in.conn = conn
	in.closing = false
	in.connects++
	in.info(fmt.Sprintf("Connected to %s (local %s)", conn.RemoteAddr(), conn.LocalAddr()))
	in.runActionsLogged(in.sess.OnConnect)
	in.mu.Unlock()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // timer tick
		defer wg.Done()
		t := time.NewTicker(in.opts.Tick)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				in.mu.Lock()
				if in.conn != nil && !in.closing {
					in.runActionsLogged(in.sess.OnTimer)
				}
				in.mu.Unlock()
			}
		}
	}()
	go func() { // hard stop on context cancellation
		defer wg.Done()
		select {
		case <-done:
		case <-ctx.Done():
			in.mu.Lock()
			in.closeLocked("context cancelled")
			in.mu.Unlock()
		}
	}()

	var dec codec.Decoder
	buf := make([]byte, readChunk)
	peerClosed := false
	var readErr error
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			in.mu.Lock()
			for _, item := range dec.Decode(buf[:n]) {
				if in.closing {
					break
				}
				in.handleItem(item)
			}
			stop := in.closing
			in.mu.Unlock()
			if stop {
				break
			}
		}
		if err != nil {
			in.mu.Lock()
			peerClosed = !in.closing
			in.mu.Unlock()
			readErr = err
			break
		}
	}
	close(done)
	wg.Wait()

	in.mu.Lock()
	defer in.mu.Unlock()
	if peerClosed {
		reason := "connection closed by counterparty"
		if readErr != nil && !errors.Is(readErr, net.ErrClosed) && !errors.Is(readErr, io.EOF) {
			reason = fmt.Sprintf("connection lost: %v", readErr)
		}
		in.warn(reason)
		in.opts.Evidence.Event(in.id, "connection dropped", reason, false)
	}
	in.closeLocked("")
	before := in.sess.State()
	for _, a := range in.sess.OnDisconnect() {
		if _, isDisc := a.(session.Disconnect); !isDisc {
			in.run(a)
		}
	}
	in.logState(before)
	in.info(fmt.Sprintf("Connection closed, peer=%s", conn.RemoteAddr()))
	in.conn = nil
	return in.sess.Outcome()
}

// closeLocked closes the socket once; the reader then exits.
func (in *Initiator) closeLocked(reason string) {
	if in.conn == nil || in.closing {
		return
	}
	in.closing = true
	if reason != "" {
		in.info("Disconnecting: " + reason)
		in.opts.Evidence.Event(in.id, "disconnecting", reason, false)
	}
	in.conn.Close()
}

func (in *Initiator) handleItem(item codec.Item) {
	switch it := item.(type) {
	case *codec.DiscardedFrame:
		in.warn("Discarded frame: " + it.Reason)
		in.opts.FixLog.Discarded(it)
		in.opts.Evidence.Discarded(in.id, it)
		in.runActionsLogged(func() []session.Action { return in.sess.OnDiscarded(it) })
	case *codec.Message:
		if in.opts.OnWire != nil {
			in.opts.OnWire("in", it)
		}
		in.opts.FixLog.Inbound(it)
		in.opts.Evidence.Message(evidence.KindIn, in.id, 0, it, "", false)
		in.runActionsLogged(func() []session.Action { return in.sess.OnMessage(it) })
	}
}

// runActionsLogged calls fn (a session input) and executes what it returns,
// logging any state transition. Caller holds mu.
func (in *Initiator) runActionsLogged(fn func() []session.Action) {
	before := in.sess.State()
	for _, a := range fn() {
		in.run(a)
	}
	in.logState(before)
}

func (in *Initiator) logState(before session.State) {
	if after := in.sess.State(); after != before {
		in.info(fmt.Sprintf("State %s -> %s", before, after))
	}
}

func (in *Initiator) run(a session.Action) {
	in.debug(fmt.Sprintf("Action: %s", describe(a)))
	switch act := a.(type) {
	case session.Send:
		in.write(act)
	case session.Evidence:
		in.opts.Evidence.EventWithOrder(in.id, act.Event, act.Detail, act.Injected, act.Order)
		text := act.Event
		if act.Detail != "" {
			text = act.Event + ": " + act.Detail
		}
		if act.Event == "resend summary" || act.Event == "order report" {
			text = act.Detail // already a finished, column-aligned line
		}
		in.opts.EngineLog.Log(logs.Level(act.Level), in.id, text)
		if in.opts.OnEvidence != nil {
			in.opts.OnEvidence(act)
		}
	case session.Disconnect:
		in.closeLocked(act.Reason)
	}
}

func describe(a session.Action) string {
	switch act := a.(type) {
	case session.Send:
		return fmt.Sprintf("Send(35=%s seq=%d injected=%v %s)", act.MsgType, act.Seq, act.Injected, act.Detail)
	case session.Evidence:
		return fmt.Sprintf("Evidence(%s: %s)", act.Event, act.Detail)
	case session.Disconnect:
		return fmt.Sprintf("Disconnect(%s)", act.Reason)
	}
	return fmt.Sprintf("%T", a)
}

func (in *Initiator) write(s session.Send) {
	if in.conn != nil && !in.closing {
		in.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := in.conn.Write(s.Raw); err != nil {
			in.warn(fmt.Sprintf("Write of 35=%s seq=%d failed: %v", s.MsgType, s.Seq, err))
		}
	} else {
		in.warn(fmt.Sprintf("No connection: 35=%s seq=%d not written", s.MsgType, s.Seq))
	}
	comment := s.Detail
	if s.Injected && s.Detail != "" {
		comment = "injected: " + s.Detail
	}
	if in.opts.OnWire != nil && s.Msg != nil {
		in.opts.OnWire("out", s.Msg)
	}
	in.opts.FixLog.Outbound(s.Seq, s.MsgType, s.Raw, comment)
	in.opts.Evidence.Message(evidence.KindOut, in.id, s.Seq, s.Msg, comment, s.Injected)
	if s.MsgType == session.MsgResendRequest {
		in.info(fmt.Sprintf("ResendRequest sent: 7=%s 16=%s", s.Msg.Value(7), s.Msg.Value(16)))
	}
}

// ------------------------------------------------------------ commands

// Snapshot returns the current session view.
func (in *Initiator) Snapshot() Snapshot {
	in.mu.Lock()
	defer in.mu.Unlock()
	return Snapshot{
		Connected:        in.conn != nil && !in.closing,
		State:            in.sess.State(),
		NextOut:          in.sess.NextOut(),
		NextIn:           in.sess.NextIn(),
		HeartBtInt:       in.sess.HeartBtInt(),
		PendingTestReqID: in.sess.PendingTestReqID(),
		Logons:           in.sess.Logons(),
		Connects:         in.connects,
		Outcome:          in.sess.Outcome(),
	}
}

func (in *Initiator) live() error {
	if in.conn == nil || in.closing {
		return ErrNotConnected
	}
	return nil
}

// Logout starts a clean Logout (or abandons a pending Logon) and disables
// reconnects. It is safe to call from any goroutine.
func (in *Initiator) Logout(text string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.stopping = true
	if err := in.live(); err != nil {
		return err
	}
	in.runActionsLogged(func() []session.Action { return in.sess.InitiateLogout(text) })
	return nil
}

// Stop disables reconnects and drops the connection at once, without Logout.
func (in *Initiator) Stop(reason string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.stopping = true
	in.closeLocked(reason)
}

// TestRequest sends a TestRequest now and returns its TestReqID.
func (in *Initiator) TestRequest() (string, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.live(); err != nil {
		return "", err
	}
	var id string
	in.runActionsLogged(func() []session.Action {
		var acts []session.Action
		id, acts = in.sess.SendTestRequest()
		return acts
	})
	if id == "" {
		return "", fmt.Errorf("session is %s, not ACTIVE", in.sess.State())
	}
	return id, nil
}

// SkipOutboundSeq is the test-only gap injection (see session.SkipOutboundSeq).
func (in *Initiator) SkipOutboundSeq(n int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.live(); err != nil {
		return err
	}
	in.runActionsLogged(func() []session.Action { return in.sess.SkipOutboundSeq(n) })
	return nil
}

// SendRaw is the test-only arbitrary send (see session.SendRaw).
func (in *Initiator) SendRaw(fields []codec.Field) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.live(); err != nil {
		return err
	}
	actions, err := in.sess.SendRaw(fields)
	if err != nil {
		return err
	}
	before := in.sess.State()
	for _, a := range actions {
		in.run(a)
	}
	in.logState(before)
	return nil
}

// Exec runs fn against the session under the session lock and executes the
// actions it returns. It fails if there is no live connection.
func (in *Initiator) Exec(fn func(s *session.Session) ([]session.Action, error)) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.live(); err != nil {
		return err
	}
	before := in.sess.State()
	actions, err := fn(in.sess)
	for _, a := range actions {
		in.run(a)
	}
	in.logState(before)
	return err
}

// Locked runs fn under the session lock (for reading state the session's
// App shares with the reader goroutine).
func (in *Initiator) Locked(fn func()) {
	in.mu.Lock()
	defer in.mu.Unlock()
	fn()
}

// Rearm clears the "stop" flag Logout/Stop set, so Run can be called again
// on the same session (the cert runner reconnects between cases).
func (in *Initiator) Rearm() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.stopping = false
}
