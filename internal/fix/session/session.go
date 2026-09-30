// Package session is the PURE FIX session core for the initiator side.
//
// No sockets, no time.Now(), no randomness: inputs in, actions out. The
// transport turns Actions into bytes on the wire, evidence records and log
// lines, and calls back in with whatever arrives. The session owns sequence
// numbers and persists every change through the injected SeqStore; every
// outbound message it creates is recorded in the injected MessageStore so a
// ResendRequest can be answered with the real thing.
//
// The rules mirror the Python emulator's session core (its Cook 1 section 7
// and Cook 4 section 6), adapted to the initiator: we send the Logon and
// validate the reply rather than the other way round.
package session

import (
	"fmt"
	"strconv"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/store"
)

// Tags used by the session layer.
const (
	TagBeginSeqNo          = 7
	TagBeginString         = 8
	TagEndSeqNo            = 16
	TagMsgSeqNum           = 34
	TagMsgType             = 35
	TagNewSeqNo            = 36
	TagPossDupFlag         = 43
	TagRefSeqNum           = 45
	TagSenderCompID        = 49
	TagSendingTime         = 52
	TagTargetCompID        = 56
	TagText                = 58
	TagEncryptMethod       = 98
	TagHeartBtInt          = 108
	TagTestReqID           = 112
	TagOrigSendingTime     = 122
	TagGapFillFlag         = 123
	TagResetSeqNumFlag     = 141
	TagRefTagID            = 371
	TagRefMsgType          = 372
	TagSessionRejectReason = 373
)

// Session-level MsgTypes.
const (
	MsgHeartbeat     = "0"
	MsgTestRequest   = "1"
	MsgResendRequest = "2"
	MsgReject        = "3"
	MsgSequenceReset = "4"
	MsgLogout        = "5"
	MsgLogon         = "A"
)

// SessionRejectReason (373) values we send.
const (
	RejectRequiredTagMissing  = "1"
	RejectValueIncorrect      = "5"
	RejectIncorrectDataFormat = "6"
	RejectCompIDProblem       = "9"
)

var requiredHeaderTags = []int{TagMsgType, TagMsgSeqNum, TagSenderCompID, TagTargetCompID, TagSendingTime}

// headerTags are supplied by the session on every send; SendRaw may not set
// them.
var headerTags = map[int]bool{8: true, 9: true, 10: true, 34: true, 49: true, 52: true, 56: true}

// ------------------------------------------------------------------ states

// State is the session state.
type State string

// Session states. The initiator's path is
// DISCONNECTED -> LOGON_SENT -> ACTIVE -> LOGOUT_SENT -> DISCONNECTED.
const (
	Disconnected State = "DISCONNECTED"
	LogonSent    State = "LOGON_SENT"
	Active       State = "ACTIVE"
	LogoutSent   State = "LOGOUT_SENT"
)

// ------------------------------------------------------------------ actions

// Level is the engine-log level an Evidence action deserves.
type Level int

// Levels, lowest first.
const (
	Debug Level = iota
	Info
	Warning
	Error
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Warning:
		return "WARNING"
	case Error:
		return "ERROR"
	}
	return "INFO"
}

// Action is what the session asks the transport to do.
type Action interface{ isAction() }

// Send puts Raw on the wire. The session has already encoded it (with the
// injected clock), assigned Seq and stored it when appropriate.
type Send struct {
	Seq      int
	MsgType  string
	Raw      []byte
	Msg      *codec.Message // Raw decoded, for evidence fields
	Injected bool
	// Detail describes a replay or an injection; it becomes the evidence
	// detail and the FIX log comment.
	Detail string
}

// Disconnect closes the connection.
type Disconnect struct {
	Reason string
}

// Evidence is a non-message event worth recording.
type Evidence struct {
	Event    string
	Detail   string
	Level    Level
	Injected bool
	// Order is an order snapshot for order events (evidence "order" field).
	Order any
}

func (Send) isAction()       {}
func (Disconnect) isAction() {}
func (Evidence) isAction()   {}

// App receives inbound application messages; what it returns (evidence) is
// executed like any other session action.
type App interface {
	OnAppMessage(msg *codec.Message) []Action
}

// RejectObserver is an optional App extension told about inbound session
// Rejects (35=3), which may refer to one of its requests.
type RejectObserver interface {
	OnSessionReject(msg *codec.Message) []Action
}

// SentObserver is an optional App extension told about every outbound
// application message (new, replayed or injected) as it is created.
type SentObserver interface {
	OnAppSent(send Send)
}

// NoopApp ignores application messages.
type NoopApp struct{}

// OnAppMessage does nothing.
func (NoopApp) OnAppMessage(*codec.Message) []Action { return nil }

// Heartbeat mismatch policies.
const (
	HeartbeatMismatchWarn   = "warn"
	HeartbeatMismatchRefuse = "refuse"
)

// MaxHeld bounds the out-of-order queue per session.
const MaxHeld = 1000

// ------------------------------------------------------------------ config

// Config is what one session needs to know.
type Config struct {
	SessionID         string
	Profile           *profile.Profile
	SenderCompID      string
	TargetCompID      string
	HeartbeatSec      int
	ResetOnLogon      bool
	LogonTimeout      time.Duration
	LogoutTimeout     time.Duration
	HeartbeatGracePct float64
	// HeartbeatMismatch is "warn" (default: keep our interval) or "refuse".
	HeartbeatMismatch string
}

// Outcome summarizes how the current (or last) connection went, for the CLI.
type Outcome struct {
	LoggedOn bool // a Logon reply was accepted on this connection
	// Refused is set when the counterparty answered our Logon with a Logout.
	Refused     bool
	RefusalText string
	// CleanLogout is set when a Logout exchange completed in either direction.
	CleanLogout     bool
	LogoutByUs      bool
	LogoutText      string // 58 of the counterparty's Logout, if any
	DisconnectCause string // the reason on the last Disconnect we produced
}

// ------------------------------------------------------------------ session

// Session is the pure session core. It is not safe for concurrent use; the
// transport calls it from a single goroutine.
type Session struct {
	cfg      Config
	seqStore store.SeqStore
	msgStore store.MessageStore
	clock    clock.Clock
	app      App

	state      State
	nextOut    int
	nextIn     int
	heartBtInt int

	lastSent     time.Time
	lastReceived time.Time

	testReqCounter   int
	pendingTestReqID string
	pendingTestReqAt time.Time

	resendOutstanding bool
	resendGapHigh     int // 0 = unknown; the seq that revealed the gap
	resendEnd         int // EndSeqNo of the outstanding ResendRequest

	// held is the out-of-order queue: messages above next_in, by seq, kept
	// while a ResendRequest is outstanding. processed records seqs handled
	// in sequence since the queue last emptied (so a held original of a
	// message we already processed as a replay is dropped, not repeated).
	held      map[int]*codec.Message
	processed map[int]bool
	// consumed marks held seqs already processed (a Logon reply that arrived
	// above next_in): when the gap closes they only advance next_in.
	consumed map[int]bool

	logonSentAt  time.Time
	logoutSentAt time.Time

	// resetNextLogon makes the next Logon carry 141=Y (after resetting both
	// numbers locally). It starts as ResetOnLogon and is cleared once a Logon
	// reply is accepted, so automatic reconnects continue the sequence.
	resetNextLogon bool
	sentResetFlag  bool // the Logon on this connection carried 141=Y

	outcome   Outcome
	logons    int
	lastError error
}

// New builds a session, loading sequence numbers from seqStore. msgStore may
// be nil (then ResendRequests are answered with one gap fill); app may be nil.
func New(cfg Config, seqStore store.SeqStore, msgStore store.MessageStore, clk clock.Clock, app App) (*Session, error) {
	if cfg.Profile == nil {
		return nil, fmt.Errorf("session %s: no FIX profile", cfg.SessionID)
	}
	if cfg.HeartbeatSec <= 0 {
		return nil, fmt.Errorf("session %s: heartbeat_sec must be positive", cfg.SessionID)
	}
	if app == nil {
		app = NoopApp{}
	}
	out, in, err := seqStore.Load()
	if err != nil {
		return nil, fmt.Errorf("session %s: loading seqnums: %w", cfg.SessionID, err)
	}
	now := clk.Now()
	return &Session{
		cfg:            cfg,
		seqStore:       seqStore,
		msgStore:       msgStore,
		clock:          clk,
		app:            app,
		state:          Disconnected,
		nextOut:        out,
		nextIn:         in,
		lastSent:       now,
		lastReceived:   now,
		resetNextLogon: cfg.ResetOnLogon,
		held:           map[int]*codec.Message{},
		processed:      map[int]bool{},
		consumed:       map[int]bool{},
	}, nil
}

// ------------------------------------------------------------ accessors

// State is the current state.
func (s *Session) State() State { return s.state }

// NextOut is the next outbound MsgSeqNum.
func (s *Session) NextOut() int { return s.nextOut }

// NextIn is the next expected inbound MsgSeqNum.
func (s *Session) NextIn() int { return s.nextIn }

// HeartBtInt is the agreed heartbeat interval (0 before logon).
func (s *Session) HeartBtInt() int { return s.heartBtInt }

// PendingTestReqID is the outstanding TestReqID, or "".
func (s *Session) PendingTestReqID() string { return s.pendingTestReqID }

// ResendOutstanding reports whether we are waiting on our ResendRequest.
func (s *Session) ResendOutstanding() bool { return s.resendOutstanding }

// Outcome reports how the current connection went.
func (s *Session) Outcome() Outcome { return s.outcome }

// Logons counts accepted Logon replies over the session's lifetime.
func (s *Session) Logons() int { return s.logons }

// ResetPending reports whether the next Logon will carry 141=Y.
func (s *Session) ResetPending() bool { return s.resetNextLogon }

// Config returns the session config.
func (s *Session) Config() Config { return s.cfg }

// RequestReset makes the next Logon reset sequence numbers (141=Y).
func (s *Session) RequestReset() { s.resetNextLogon = true }

// ------------------------------------------------------------- helpers

func (s *Session) persist() []Action {
	if err := s.seqStore.Save(s.nextOut, s.nextIn); err != nil {
		return []Action{Evidence{Event: "seqnum persist failed", Detail: err.Error(), Level: Error}}
	}
	return nil
}

func (s *Session) header(seq int, possDup bool, origSendingTime string) codec.Header {
	return codec.Header{
		SenderCompID:    s.cfg.SenderCompID,
		TargetCompID:    s.cfg.TargetCompID,
		MsgSeqNum:       seq,
		SendingTime:     codec.FormatTime(s.clock.Now()),
		PossDup:         possDup,
		OrigSendingTime: origSendingTime,
	}
}

func (s *Session) finishSend(seq int, msgType string, raw []byte, injected bool, detail string) Send {
	msg, err := codec.DecodeOne(raw)
	if err != nil {
		msg = &codec.Message{Raw: raw}
	}
	s.lastSent = s.clock.Now()
	send := Send{Seq: seq, MsgType: msgType, Raw: raw, Msg: msg, Injected: injected, Detail: detail}
	if !s.cfg.Profile.IsAdmin(msgType) {
		if obs, ok := s.app.(SentObserver); ok {
			obs.OnAppSent(send)
		}
	}
	return send
}

// send encodes a new message on the next outbound seq, persists the numbers
// and stores it for replay.
func (s *Session) send(msgType string, body []codec.Field) []Action {
	return s.sendWith(msgType, body, false, "")
}

func (s *Session) sendWith(msgType string, body []codec.Field, injected bool, detail string) []Action {
	seq := s.nextOut
	s.nextOut++
	actions := s.persist()
	raw := codec.Encode(s.cfg.Profile.BeginString, msgType, s.header(seq, false, ""), body)
	if s.msgStore != nil {
		err := s.msgStore.Append(store.Record{
			Seq:        seq,
			MsgType:    msgType,
			FixVersion: s.cfg.Profile.Name,
			Raw:        string(raw),
			SentTS:     s.clock.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			Injected:   injected,
		})
		if err != nil {
			actions = append(actions, Evidence{Event: "message store write failed", Detail: err.Error(), Level: Error})
		}
	}
	return append(actions, s.finishSend(seq, msgType, raw, injected, detail))
}

// gapFill builds a SequenceReset-GapFill on an old seq; it consumes nothing
// and is never stored (it reuses a number that belongs to another message).
func (s *Session) gapFill(beginSeq, newSeqNo int) Send {
	now := codec.FormatTime(s.clock.Now())
	raw := codec.Encode(s.cfg.Profile.BeginString, MsgSequenceReset, s.header(beginSeq, true, now), []codec.Field{
		codec.F(TagGapFillFlag, "Y"),
		codec.F(TagNewSeqNo, strconv.Itoa(newSeqNo)),
	})
	return s.finishSend(beginSeq, MsgSequenceReset, raw, false, "")
}

func (s *Session) logoutAndDisconnect(text string) []Action {
	actions := s.send(MsgLogout, []codec.Field{codec.F(TagText, text)})
	s.state = LogoutSent
	s.logoutSentAt = s.clock.Now()
	return append(actions, s.disconnect(text))
}

func (s *Session) disconnect(reason string) Disconnect {
	s.outcome.DisconnectCause = reason
	return Disconnect{Reason: reason}
}

func (s *Session) reject(refSeq int, reason, text string) []Action {
	body := []codec.Field{codec.F(TagRefSeqNum, strconv.Itoa(refSeq))}
	if reason != "" {
		body = append(body, codec.F(TagSessionRejectReason, reason))
	}
	if text != "" {
		body = append(body, codec.F(TagText, text))
	}
	return s.send(MsgReject, body)
}

func (s *Session) clearResendIfCovered() {
	if !s.resendOutstanding {
		return
	}
	if s.resendEnd > 0 && s.nextIn > s.resendEnd {
		// The requested (closed) range has been delivered; anything still held
		// beyond a further gap needs a new request (see drain).
		s.resendOutstanding = false
		s.resendGapHigh = 0
		s.resendEnd = 0
		return
	}
	if len(s.held) > 0 {
		return
	}
	if s.resendGapHigh == 0 || s.nextIn > s.resendGapHigh {
		s.resendOutstanding = false
		s.resendGapHigh = 0
	}
}

func (s *Session) resetSeqNums(why string) []Action {
	s.nextOut, s.nextIn = 1, 1
	actions := s.persist()
	actions = append(actions, Evidence{Event: "seqnums reset", Detail: why, Level: Info})
	if s.msgStore != nil {
		archived, err := s.msgStore.Archive(s.clock.Now())
		switch {
		case err != nil:
			actions = append(actions, Evidence{Event: "store archive failed", Detail: err.Error(), Level: Error})
		case archived != "":
			actions = append(actions, Evidence{Event: "store archived",
				Detail: fmt.Sprintf("outbound message store archived to %s (%s)", archived, why), Level: Info})
		}
	}
	return actions
}

func atoi(v string, ok bool) (int, bool) {
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ----------------------------------------------------- connection events

// OnConnect is called when the TCP connection is up: we send our Logon.
func (s *Session) OnConnect() []Action {
	now := s.clock.Now()
	s.state = LogonSent
	s.lastSent, s.lastReceived = now, now
	s.pendingTestReqID, s.pendingTestReqAt = "", time.Time{}
	s.resendOutstanding, s.resendGapHigh, s.resendEnd = false, 0, 0
	s.logoutSentAt = time.Time{}
	s.heartBtInt = 0
	s.outcome = Outcome{}
	s.held, s.processed, s.consumed = map[int]*codec.Message{}, map[int]bool{}, map[int]bool{}

	actions := []Action{Evidence{Event: "connected", Detail: "sending Logon", Level: Info}}
	s.sentResetFlag = s.resetNextLogon
	if s.sentResetFlag {
		actions = append(actions, s.resetSeqNums("Logon will carry 141=Y")...)
	}
	body := []codec.Field{
		codec.F(TagEncryptMethod, "0"),
		codec.F(TagHeartBtInt, strconv.Itoa(s.cfg.HeartbeatSec)),
	}
	if s.sentResetFlag {
		body = append(body, codec.F(TagResetSeqNumFlag, "Y"))
	}
	actions = append(actions, s.send(MsgLogon, body)...)
	s.logonSentAt = s.clock.Now()
	return actions
}

// OnDisconnect is called when the connection is gone, whoever closed it.
func (s *Session) OnDisconnect() []Action {
	s.state = Disconnected
	s.heartBtInt = 0
	s.pendingTestReqID, s.pendingTestReqAt = "", time.Time{}
	s.logoutSentAt = time.Time{}
	actions := s.persist()
	if n := len(s.held); n > 0 {
		actions = append(actions, Evidence{Event: "held messages dropped on disconnect",
			Detail: fmt.Sprintf("%d message(s) were still waiting behind a gap", n), Level: Warning})
	}
	s.held, s.processed, s.consumed = map[int]*codec.Message{}, map[int]bool{}, map[int]bool{}
	return append(actions, Evidence{Event: "disconnected",
		Detail: fmt.Sprintf("next_out=%d next_in=%d", s.nextOut, s.nextIn), Level: Info})
}

// OnDiscarded records a frame that failed BodyLength/CheckSum: discard, never
// reject.
func (s *Session) OnDiscarded(frame *codec.DiscardedFrame) []Action {
	return []Action{Evidence{Event: "frame discarded", Detail: frame.Reason, Level: Warning}}
}

// ------------------------------------------------------------- inbound

// OnMessage handles one inbound, framing-valid message.
func (s *Session) OnMessage(msg *codec.Message) []Action {
	s.lastReceived = s.clock.Now()
	switch s.state {
	case Disconnected:
		return []Action{Evidence{Event: "message while disconnected", Detail: "msg_type=" + msg.MsgType(), Level: Warning}}
	case LogonSent:
		return s.onLogonReply(msg)
	}

	// A counterparty on the wrong FIX version gets a Logout and the door;
	// there is no shared dialect to reject in.
	if bs := msg.Value(TagBeginString); bs != s.cfg.Profile.BeginString {
		actions := []Action{Evidence{Event: "begin string mismatch",
			Detail: fmt.Sprintf("received %s, expected %s", bs, s.cfg.Profile.BeginString), Level: Warning}}
		return append(actions, s.logoutAndDisconnect("Incorrect BeginString, expected "+s.cfg.Profile.BeginString)...)
	}
	return s.onActive(msg)
}

// ---------------------------------------------------------- logon phase

func (s *Session) onLogonReply(msg *codec.Message) []Action {
	msgType := msg.MsgType()

	// A Logout instead of a Logon is a refusal. Its text is the most useful
	// thing the counterparty will ever tell us about why, so it is recorded
	// whatever version or sequence number it arrived with.
	if msgType == MsgLogout {
		text := msg.Value(TagText)
		s.outcome.Refused = true
		s.outcome.RefusalText = text
		s.outcome.LogoutText = text
		detail := "counterparty answered Logon with Logout"
		if text != "" {
			detail += ": " + text
		}
		// Answer their Logout with ours before hanging up (A2 3.5).
		actions := []Action{Evidence{Event: "logon refused", Detail: detail, Level: Warning}}
		actions = append(actions, s.send(MsgLogout, []codec.Field{codec.F(TagText, "Logout acknowledged")})...)
		return append(actions, s.disconnect("Logon refused by counterparty: "+orNone(text)))
	}
	if msgType != MsgLogon {
		return []Action{
			Evidence{Event: "first message not Logon", Detail: "msg_type=" + msgType, Level: Warning},
			s.disconnect("First message not Logon"),
		}
	}

	if bs := msg.Value(TagBeginString); bs != s.cfg.Profile.BeginString {
		actions := []Action{Evidence{Event: "begin string mismatch",
			Detail: fmt.Sprintf("Logon reply has %s, expected %s", bs, s.cfg.Profile.BeginString), Level: Warning}}
		return append(actions, s.logoutAndDisconnect("Incorrect BeginString, expected "+s.cfg.Profile.BeginString)...)
	}

	sender, target := msg.Value(TagSenderCompID), msg.Value(TagTargetCompID)
	if sender != s.cfg.TargetCompID || target != s.cfg.SenderCompID {
		return s.logoutAndDisconnect(fmt.Sprintf(
			"CompID problem: expecting 49=%s 56=%s, received 49=%s 56=%s",
			s.cfg.TargetCompID, s.cfg.SenderCompID, sender, target))
	}

	hb, ok := atoi(msg.Get(TagHeartBtInt))
	if !ok || hb <= 0 {
		return s.logoutAndDisconnect("HeartBtInt must be a positive integer, received " + msg.Value(TagHeartBtInt))
	}
	var actions []Action
	if hb != s.cfg.HeartbeatSec {
		if s.cfg.HeartbeatMismatch == HeartbeatMismatchRefuse {
			return s.logoutAndDisconnect(fmt.Sprintf("HeartBtInt mismatch, sent %d received %d", s.cfg.HeartbeatSec, hb))
		}
		actions = append(actions, Evidence{Event: "heartbeat mismatch",
			Detail: fmt.Sprintf("sent HeartBtInt=%d, counterparty replied %d; keeping our %d", s.cfg.HeartbeatSec, hb, s.cfg.HeartbeatSec),
			Level:  Warning})
		hb = s.cfg.HeartbeatSec
	}

	seq, ok := msg.SeqNum()
	if !ok {
		return s.logoutAndDisconnect("Required tag missing: MsgSeqNum (34)")
	}

	if msg.Value(TagResetSeqNumFlag) == "Y" && !s.sentResetFlag {
		// They reset without being asked: their numbering restarts at 1.
		s.nextIn = 1
		actions = append(actions, s.persist()...)
		actions = append(actions, Evidence{Event: "counterparty reset seqnums",
			Detail: "Logon reply carried 141=Y although our Logon did not", Level: Warning})
	}

	expected := s.nextIn
	gap := false
	switch {
	case seq == expected:
		s.nextIn = seq + 1
		actions = append(actions, s.persist()...)
	case seq > expected:
		gap = true
		actions = append(actions, Evidence{Event: "seq gap detected",
			Detail: fmt.Sprintf("Logon MsgSeqNum %d, expecting %d", seq, expected), Level: Warning})
	default:
		return append(actions, s.logoutAndDisconnect(
			fmt.Sprintf("MsgSeqNum too low, expecting %d but received %d", expected, seq))...)
	}

	s.heartBtInt = hb
	s.state = Active
	s.resetNextLogon = false
	s.logons++
	s.outcome.LoggedOn = true
	actions = append(actions, Evidence{Event: "logon accepted",
		Detail: fmt.Sprintf("HeartBtInt=%d, next_in=%d next_out=%d", hb, s.nextIn, s.nextOut), Level: Info})
	if gap {
		// The Logon itself is processed; its seq is consumed once the range
		// before it has been filled.
		s.held[seq] = msg
		s.consumed[seq] = true
		actions = append(actions, s.requestResend(expected, seq)...)
	}
	return actions
}

func orNone(text string) string {
	if text == "" {
		return "(no text)"
	}
	return text
}

// requestResend asks for exactly the missing range: 7=begin, 16=gapHigh-1
// where gapHigh is the seq that revealed the gap (A3 3.1: closed range).
// The revealing message and anything after it are held and processed in
// order once the range is filled.
func (s *Session) requestResend(begin, gapHigh int) []Action {
	if s.resendOutstanding {
		return nil
	}
	end := gapHigh - 1
	if end < begin {
		end = begin
	}
	s.resendOutstanding = true
	s.resendGapHigh = gapHigh
	s.resendEnd = end
	actions := []Action{Evidence{Event: "resend request sent",
		Detail: fmt.Sprintf("7=%d 16=%d", begin, end), Level: Info}}
	return append(actions, s.send(MsgResendRequest, []codec.Field{
		codec.F(TagBeginSeqNo, strconv.Itoa(begin)),
		codec.F(TagEndSeqNo, strconv.Itoa(end)),
	})...)
}

// --------------------------------------------------------- active phase

func (s *Session) onActive(msg *codec.Message) []Action {
	msgType := msg.MsgType()
	seq, ok := msg.SeqNum()
	if !ok {
		return s.logoutAndDisconnect("Required tag missing: MsgSeqNum (34)")
	}

	sender, hasSender := msg.Get(TagSenderCompID)
	target, hasTarget := msg.Get(TagTargetCompID)
	if (hasSender && sender != s.cfg.TargetCompID) || (hasTarget && target != s.cfg.SenderCompID) {
		actions := s.reject(seq, RejectCompIDProblem, fmt.Sprintf(
			"CompID problem: expecting 49=%s 56=%s, received 49=%s 56=%s",
			s.cfg.TargetCompID, s.cfg.SenderCompID, sender, target))
		return append(actions, s.logoutAndDisconnect("CompID problem")...)
	}

	var actions []Action
	if msgType == MsgSequenceReset && msg.Value(TagGapFillFlag) != "Y" {
		// SequenceReset in Reset mode skips the sequence check entirely.
		actions = s.sequenceReset(msg, seq, false)
	} else {
		acts, stop := s.sequenceCheck(msg, seq)
		actions = acts
		if !stop {
			actions = append(actions, s.process(msg, seq)...)
		}
	}
	if hasDisconnect(actions) {
		return actions
	}
	return append(actions, s.drain()...)
}

func hasDisconnect(actions []Action) bool {
	for _, a := range actions {
		if _, ok := a.(Disconnect); ok {
			return true
		}
	}
	return false
}

// process handles a message whose sequence number has been consumed.
func (s *Session) process(msg *codec.Message, seq int) []Action {
	if len(s.held) > 0 || s.resendOutstanding {
		s.processed[seq] = true
	}
	return s.processContent(msg, seq)
}

// processContent validates the header and dispatches, without touching
// sequence numbers.
func (s *Session) processContent(msg *codec.Message, seq int) []Action {
	var missing []string
	for _, tag := range requiredHeaderTags {
		if !msg.Has(tag) {
			missing = append(missing, strconv.Itoa(tag))
		}
	}
	if len(missing) > 0 {
		return s.reject(seq, RejectRequiredTagMissing, "Required tag missing: "+joinComma(missing))
	}
	return s.dispatch(msg, msg.MsgType(), seq)
}

// drain releases held messages, in seq order, once the range before them
// has been filled. A held message whose seq has already been covered (by a
// gap fill or a resend) is dropped: with a closed-range ResendRequest the
// counterparty only fills what was missing, so that never loses anything it
// meant us to see. If held messages remain behind a further gap once the
// outstanding range is complete, a new ResendRequest goes out.
func (s *Session) drain() []Action {
	var actions []Action
	for len(s.held) > 0 && (s.state == Active || s.state == LogoutSent) && !hasDisconnect(actions) {
		low := s.lowestHeld()
		if low > s.nextIn {
			break
		}
		m := s.held[low]
		delete(s.held, low)
		desc := fmt.Sprintf("35=%s seq=%d", m.MsgType(), low)
		switch {
		case low == s.nextIn && s.consumed[low]:
			delete(s.consumed, low)
			s.nextIn++
			actions = append(actions, s.persist()...)
			actions = append(actions, Evidence{Event: "message dequeued",
				Detail: desc + " was already processed; its seq is now consumed", Level: Info})
		case low == s.nextIn:
			s.nextIn++
			actions = append(actions, s.persist()...)
			actions = append(actions, Evidence{Event: "message dequeued",
				Detail: desc + " processed in sequence after the gap closed", Level: Info})
			actions = append(actions, s.process(m, low)...)
		default:
			delete(s.consumed, low)
			actions = append(actions, Evidence{Event: "held message dropped",
				Detail: desc + " was already covered by a gap fill or resend", Level: Warning})
		}
	}
	s.clearResendIfCovered()
	if len(s.held) == 0 {
		s.processed = map[int]bool{}
	} else if !s.resendOutstanding && !hasDisconnect(actions) && (s.state == Active || s.state == LogoutSent) {
		if low := s.lowestHeld(); low > s.nextIn {
			actions = append(actions, Evidence{Event: "seq gap detected",
				Detail: fmt.Sprintf("held MsgSeqNum %d, expecting %d", low, s.nextIn), Level: Warning})
			actions = append(actions, s.requestResend(s.nextIn, low)...)
		}
	}
	return actions
}

func (s *Session) lowestHeld() int {
	low := 0
	for seq := range s.held {
		if low == 0 || seq < low {
			low = seq
		}
	}
	return low
}

// Held reports how many messages are in the out-of-order queue.
func (s *Session) Held() int { return len(s.held) }

func joinComma(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ", "
		}
		out += it
	}
	return out
}

// sequenceCheck returns (actions, true) if the message must not be processed
// now. A message above next_in is held until the gap before it closes.
func (s *Session) sequenceCheck(msg *codec.Message, seq int) ([]Action, bool) {
	expected := s.nextIn
	if seq == expected {
		s.nextIn = seq + 1
		actions := s.persist()
		s.clearResendIfCovered()
		return actions, false
	}
	if seq > expected {
		actions := []Action{Evidence{Event: "seq gap detected",
			Detail: fmt.Sprintf("received MsgSeqNum %d, expecting %d", seq, expected), Level: Warning}}
		if _, dup := s.held[seq]; dup {
			actions = append(actions, Evidence{Event: "held duplicate ignored",
				Detail: fmt.Sprintf("35=%s seq=%d is already held", msg.MsgType(), seq), Level: Warning})
		} else {
			if len(s.held) >= MaxHeld {
				actions = append(actions, Evidence{Event: "resend queue overflow",
					Detail: fmt.Sprintf("%d messages held behind a gap at %d", len(s.held), expected), Level: Error})
				return append(actions, s.logoutAndDisconnect("Resend queue overflow")...), true
			}
			s.held[seq] = msg
			actions = append(actions, Evidence{Event: "message queued",
				Detail: fmt.Sprintf("35=%s seq=%d held until seq %d arrives (%d held)", msg.MsgType(), seq, expected, len(s.held)),
				Level:  Info})
		}
		if s.resendOutstanding {
			return append(actions, Evidence{Event: "resend already outstanding",
				Detail: fmt.Sprintf("no second ResendRequest for MsgSeqNum %d", seq), Level: Warning}), true
		}
		return append(actions, s.requestResend(expected, seq)...), true
	}
	if msg.Value(TagPossDupFlag) == "Y" {
		return []Action{Evidence{Event: "possdup ignored",
			Detail: fmt.Sprintf("MsgSeqNum %d below expected %d, 43=Y", seq, expected), Level: Warning}}, true
	}
	return s.logoutAndDisconnect(fmt.Sprintf("MsgSeqNum too low, expecting %d but received %d", expected, seq)), true
}

func (s *Session) dispatch(msg *codec.Message, msgType string, seq int) []Action {
	switch msgType {
	case MsgHeartbeat:
		id, ok := msg.Get(TagTestReqID)
		if ok && id != "" && id == s.pendingTestReqID {
			s.pendingTestReqID, s.pendingTestReqAt = "", time.Time{}
			return []Action{Evidence{Event: "testrequest answered", Detail: "112=" + id, Level: Info}}
		}
		return nil

	case MsgTestRequest:
		id, ok := msg.Get(TagTestReqID)
		if !ok {
			return s.reject(seq, RejectRequiredTagMissing, "Required tag missing: TestReqID (112)")
		}
		return s.send(MsgHeartbeat, []codec.Field{codec.F(TagTestReqID, id)})

	case MsgResendRequest:
		return s.onResendRequest(msg, seq)

	case MsgReject:
		detail := fmt.Sprintf("45=%s 371=%s 372=%s 373=%s 58=%s",
			msg.Value(TagRefSeqNum), msg.Value(TagRefTagID), msg.Value(TagRefMsgType),
			msg.Value(TagSessionRejectReason), msg.Value(TagText))
		if code := msg.Value(TagSessionRejectReason); code != "" {
			detail += " (" + s.cfg.Profile.RejectReasonName(code) + ")"
		}
		actions := []Action{Evidence{Event: "reject received", Detail: detail, Level: Warning}}
		if obs, ok := s.app.(RejectObserver); ok {
			actions = append(actions, obs.OnSessionReject(msg)...)
		}
		return actions

	case MsgSequenceReset:
		return s.sequenceReset(msg, seq, true)

	case MsgLogout:
		text := msg.Value(TagText)
		s.outcome.LogoutText = text
		if s.state == LogoutSent {
			s.outcome.CleanLogout = true
			return []Action{
				Evidence{Event: "logout confirmed", Detail: text, Level: Info},
				s.disconnect("Logout confirmed"),
			}
		}
		// A logout we did not initiate: reply and go straight down.
		actions := []Action{Evidence{Event: "logout received", Detail: text, Level: Info}}
		actions = append(actions, s.send(MsgLogout, []codec.Field{codec.F(TagText, "Logout acknowledged")})...)
		s.state = Disconnected
		s.logoutSentAt = time.Time{}
		s.outcome.CleanLogout = true
		s.outcome.LogoutByUs = false
		return append(actions, s.disconnect("Logout requested by counterparty"))

	case MsgLogon:
		return s.reject(seq, "", "Logon received while already logged on")
	}

	// Anything else is an application message: record it and hand it to the
	// app. Never BusinessMessageReject it.
	name := s.cfg.Profile.MsgTypeName(msgType)
	actions := []Action{Evidence{Event: "application message received",
		Detail: fmt.Sprintf("35=%s (%s) seq=%d", msgType, name, seq), Level: Info}}
	return append(actions, s.app.OnAppMessage(msg)...)
}

// ------------------------------------------------------- resend replay

func (s *Session) onResendRequest(msg *codec.Message, seq int) []Action {
	begin, ok := atoi(msg.Get(TagBeginSeqNo))
	if !msg.Has(TagBeginSeqNo) {
		return s.reject(seq, RejectRequiredTagMissing, "Required tag missing: BeginSeqNo (7)")
	}
	if !ok || begin < 1 {
		return s.reject(seq, RejectValueIncorrect, "BeginSeqNo must be >= 1")
	}
	if !msg.Has(TagEndSeqNo) {
		return s.reject(seq, RejectRequiredTagMissing, "Required tag missing: EndSeqNo (16)")
	}
	end, ok := atoi(msg.Get(TagEndSeqNo))
	if !ok || end < 0 {
		return s.reject(seq, RejectIncorrectDataFormat, "EndSeqNo (16) is not a number")
	}

	lastSent := s.nextOut - 1
	endSeq := lastSent
	if end != 0 && end < lastSent {
		endSeq = end
	}
	if begin > endSeq {
		return []Action{Evidence{Event: "resend request for future seqnums ignored",
			Detail: fmt.Sprintf("7=%d 16=%d is beyond our last sent seq %d", begin, end, lastSent), Level: Warning}}
	}
	if s.msgStore == nil {
		return []Action{
			Evidence{Event: "resend request received",
				Detail: fmt.Sprintf("7=%d 16=%d; gap filling to %d", begin, end, endSeq+1), Level: Info},
			s.gapFill(begin, endSeq+1),
		}
	}
	return s.replay(begin, end, endSeq)
}

func (s *Session) isAdminOrMissing(seq int) bool {
	rec, ok := s.msgStore.Get(seq)
	if !ok {
		return true
	}
	return s.cfg.Profile.IsAdmin(rec.MsgType)
}

// replay resends stored application messages on their original seqs and
// collapses every run of admin messages or missing seqs into one gap fill.
func (s *Session) replay(begin, end, endSeq int) []Action {
	actions := []Action{Evidence{Event: "resend request received",
		Detail: fmt.Sprintf("7=%d 16=%d; replaying %d..%d", begin, end, begin, endSeq), Level: Info}}
	replayed, runs := 0, 0
	for cur := begin; cur <= endSeq; {
		if s.isAdminOrMissing(cur) {
			start := cur
			for cur <= endSeq && s.isAdminOrMissing(cur) {
				cur++
			}
			actions = append(actions, s.gapFill(start, cur))
			runs++
			continue
		}
		rec, _ := s.msgStore.Get(cur)
		if send, err := s.replayOne(rec); err != nil {
			// A stored message that no longer decodes is gap-filled rather
			// than resent broken.
			actions = append(actions,
				Evidence{Event: "replay failed", Detail: fmt.Sprintf("seq %d: %v; gap filling", cur, err), Level: Error},
				s.gapFill(cur, cur+1))
			runs++
		} else {
			actions = append(actions, send)
			replayed++
		}
		cur++
	}
	return append(actions, Evidence{Event: "resend summary",
		Detail: fmt.Sprintf("%-5s %d..%d -> replayed %d, gap-filled %d run(s)", "RESEND", begin, endSeq, replayed, runs),
		Level:  Info})
}

func (s *Session) replayOne(rec store.Record) (Send, error) {
	orig, err := codec.DecodeOne([]byte(rec.Raw))
	if err != nil {
		return Send{}, err
	}
	fields := WithPossDup(orig.Fields, orig.Value(TagSendingTime), codec.FormatTime(s.clock.Now()))
	raw := codec.Build(orig.Value(TagBeginString), fields, false)
	detail := fmt.Sprintf("replay of seq %d", rec.Seq)
	if rec.Injected {
		detail = fmt.Sprintf("replay of injected seq %d", rec.Seq)
	}
	return s.finishSend(rec.Seq, rec.MsgType, raw, rec.Injected, detail), nil
}

// WithPossDup returns fields with 52 set to newSendingTime and 43=Y plus
// 122=origSendingTime inserted right after 52 (any existing 43/122 dropped),
// keeping the header order 8, 9, 35, 49, 56, 34, 52, 43, 122.
func WithPossDup(fields []codec.Field, origSendingTime, newSendingTime string) []codec.Field {
	out := make([]codec.Field, 0, len(fields)+2)
	for _, f := range fields {
		if f.Tag == TagPossDupFlag || f.Tag == TagOrigSendingTime {
			continue
		}
		out = append(out, f)
	}
	insertAt := len(out)
	for i, f := range out {
		if f.Tag == TagSendingTime {
			if newSendingTime != "" {
				out[i].Value = newSendingTime
			}
			insertAt = i + 1
			break
		}
	}
	extra := []codec.Field{codec.F(TagPossDupFlag, "Y")}
	if origSendingTime != "" {
		extra = append(extra, codec.F(TagOrigSendingTime, origSendingTime))
	}
	result := append([]codec.Field{}, out[:insertAt]...)
	result = append(result, extra...)
	return append(result, out[insertAt:]...)
}

// ------------------------------------------------------ sequence reset

func (s *Session) sequenceReset(msg *codec.Message, seq int, gapFill bool) []Action {
	raw, has := msg.Get(TagNewSeqNo)
	if !has {
		return s.reject(seq, RejectRequiredTagMissing, "Required tag missing: NewSeqNo (36)")
	}
	newSeq, err := strconv.Atoi(raw)
	if err != nil {
		return s.reject(seq, RejectIncorrectDataFormat, "NewSeqNo (36) is not a number")
	}
	if newSeq < s.nextIn {
		return s.reject(seq, RejectValueIncorrect, "NewSeqNo too low")
	}
	mode := "reset"
	if gapFill {
		mode = "gap fill"
	}
	previous := s.nextIn
	s.nextIn = newSeq
	actions := s.persist()
	s.clearResendIfCovered()
	return append(actions, Evidence{Event: "sequence reset applied",
		Detail: fmt.Sprintf("%s: next_in %d -> %d", mode, previous, newSeq), Level: Info})
}

// --------------------------------------------------------------- timers

// OnTimer is called periodically (at least once a second) by the transport.
func (s *Session) OnTimer() []Action {
	now := s.clock.Now()
	if s.state == LogonSent {
		if s.cfg.LogonTimeout > 0 && now.Sub(s.logonSentAt) >= s.cfg.LogonTimeout {
			return []Action{
				Evidence{Event: "logon timeout", Detail: fmt.Sprintf("no Logon reply within %s", s.cfg.LogonTimeout), Level: Warning},
				s.disconnect("Logon timeout"),
			}
		}
		return nil
	}
	if s.state != Active && s.state != LogoutSent {
		return nil
	}

	if s.state == LogoutSent && !s.logoutSentAt.IsZero() && now.Sub(s.logoutSentAt) >= s.cfg.LogoutTimeout {
		return []Action{
			Evidence{Event: "logout timeout", Detail: fmt.Sprintf("no Logout reply within %s", s.cfg.LogoutTimeout), Level: Warning},
			s.disconnect("Logout timeout"),
		}
	}
	if s.heartBtInt == 0 {
		return nil
	}
	hb := time.Duration(s.heartBtInt) * time.Second

	if s.pendingTestReqID != "" && now.Sub(s.pendingTestReqAt) >= hb {
		return []Action{
			Evidence{Event: "testrequest timeout", Detail: "112=" + s.pendingTestReqID + " unanswered", Level: Warning},
			s.disconnect("TestRequest timeout"),
		}
	}

	var actions []Action
	if now.Sub(s.lastSent) >= hb {
		actions = append(actions, s.send(MsgHeartbeat, nil)...)
	}
	grace := time.Duration(float64(hb) * (1 + s.cfg.HeartbeatGracePct/100))
	if now.Sub(s.lastReceived) >= grace && s.pendingTestReqID == "" {
		actions = append(actions, s.testRequest()...)
	}
	return actions
}

func (s *Session) testRequest() []Action {
	s.testReqCounter++
	id := fmt.Sprintf("TEST-%d", s.testReqCounter)
	s.pendingTestReqID = id
	s.pendingTestReqAt = s.clock.Now()
	return s.send(MsgTestRequest, []codec.Field{codec.F(TagTestReqID, id)})
}

// ------------------------------------------------------------- outbound

// SendTestRequest sends a TestRequest now (TestReqID TEST-<n>). It returns
// the id, or "" if the session is not ACTIVE.
func (s *Session) SendTestRequest() (string, []Action) {
	if s.state != Active {
		return "", nil
	}
	actions := s.testRequest()
	return s.pendingTestReqID, actions
}

// InitiateLogout starts a Logout we own. While LOGON_SENT there is nobody to
// log out from, so the connection is simply dropped.
func (s *Session) InitiateLogout(text string) []Action {
	switch s.state {
	case Active:
		actions := s.send(MsgLogout, []codec.Field{codec.F(TagText, text)})
		s.state = LogoutSent
		s.logoutSentAt = s.clock.Now()
		s.outcome.LogoutByUs = true
		return append(actions, Evidence{Event: "logout initiated", Detail: text, Level: Info})
	case LogonSent:
		return []Action{
			Evidence{Event: "logon abandoned", Detail: text, Level: Warning},
			s.disconnect("Logon abandoned: " + text),
		}
	}
	return nil
}

// ------------------------------------------------- test-only injection

// SkipOutboundSeq advances next_out by n without sending anything, so the
// counterparty sees a gap and has to ask for it. Test-only; not on the CLI.
func (s *Session) SkipOutboundSeq(n int) []Action {
	if n < 1 {
		return []Action{Evidence{Event: "injection refused", Detail: "SkipOutboundSeq needs n >= 1", Level: Warning}}
	}
	before := s.nextOut
	s.nextOut += n
	actions := s.persist()
	return append(actions, Evidence{Event: "injected seq gap",
		Detail:   fmt.Sprintf("outbound MsgSeqNum advanced %d -> %d without sending %d message(s)", before, s.nextOut, n),
		Level:    Warning,
		Injected: true})
}

// SendRaw sends an arbitrary message through the normal send path: the
// session supplies the standard header (8, 9, 49, 56, 34, 52) and 10; fields
// must contain 35 and must not contain those header tags. The message is
// stored and recorded with injected: true. Test-only; not on the CLI.
func (s *Session) SendRaw(fields []codec.Field) ([]Action, error) {
	if s.state != Active && s.state != LogoutSent {
		return nil, fmt.Errorf("SendRaw: session is %s, not ACTIVE", s.state)
	}
	msgType := ""
	var body []codec.Field
	for _, f := range fields {
		switch {
		case f.Tag == TagMsgType:
			if msgType != "" {
				return nil, fmt.Errorf("SendRaw: 35 given twice")
			}
			msgType = f.Value
		case headerTags[f.Tag]:
			return nil, fmt.Errorf("SendRaw: tag %d is supplied by the session", f.Tag)
		default:
			body = append(body, f)
		}
	}
	if msgType == "" {
		return nil, fmt.Errorf("SendRaw: fields must include MsgType (35)")
	}
	actions := s.sendWith(msgType, body, true, "SendRaw")
	return append(actions, Evidence{Event: "injected message",
		Detail: fmt.Sprintf("SendRaw 35=%s seq=%d", msgType, s.nextOut-1), Level: Warning, Injected: true}), nil
}

// ----------------------------------------------------------- orders (A2)

// SendApp sends an application message through the normal path: next seq,
// stored for replay, not injected. The returned Send is the message as sent.
func (s *Session) SendApp(msgType string, body []codec.Field) (Send, []Action, error) {
	if s.state != Active {
		return Send{}, nil, fmt.Errorf("session is %s, not ACTIVE", s.state)
	}
	if msgType == "" || s.cfg.Profile.IsAdmin(msgType) {
		return Send{}, nil, fmt.Errorf("SendApp: 35=%q is not an application message", msgType)
	}
	actions := s.send(msgType, body)
	for _, a := range actions {
		if snd, ok := a.(Send); ok {
			return snd, actions, nil
		}
	}
	return Send{}, actions, fmt.Errorf("SendApp: nothing sent")
}

// SendResendRequest sends a ResendRequest 7=begin 16=end on request (the
// REPL's "resend"); it does not mark a gap as outstanding.
func (s *Session) SendResendRequest(begin, end int) ([]Action, error) {
	if s.state != Active {
		return nil, fmt.Errorf("session is %s, not ACTIVE", s.state)
	}
	if begin < 1 || end < 0 {
		return nil, fmt.Errorf("resend needs begin >= 1 and end >= 0")
	}
	actions := []Action{Evidence{Event: "resend request sent", Detail: fmt.Sprintf("7=%d 16=%d (on request)", begin, end), Level: Info}}
	return append(actions, s.send(MsgResendRequest, []codec.Field{
		codec.F(TagBeginSeqNo, strconv.Itoa(begin)),
		codec.F(TagEndSeqNo, strconv.Itoa(end)),
	})...), nil
}

// ResendStored sends one of our stored messages again on its original seq
// with 43=Y and 122 (a deliberate PossDup resend, e.g. to verify the
// counterparty deduplicates). It is recorded as injected.
func (s *Session) ResendStored(seq int) ([]Action, error) {
	if s.state != Active {
		return nil, fmt.Errorf("session is %s, not ACTIVE", s.state)
	}
	if s.msgStore == nil {
		return nil, fmt.Errorf("no outbound message store")
	}
	rec, ok := s.msgStore.Get(seq)
	if !ok {
		return nil, fmt.Errorf("seq %d is not in the outbound store", seq)
	}
	send, err := s.replayOne(rec)
	if err != nil {
		return nil, err
	}
	send.Injected = true
	send.Detail = fmt.Sprintf("deliberate PossDup resend of seq %d", seq)
	return []Action{Evidence{Event: "possdup resend", Detail: send.Detail, Level: Warning, Injected: true}, send}, nil
}
