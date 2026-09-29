package session

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/store"
)

// ------------------------------------------------------------ harness

type harness struct {
	t        *testing.T
	clk      *clock.FakeClock
	seq      *store.MemorySeqStore
	msgs     *store.MemoryMessageStore
	app      *recordingApp
	s        *Session
	prof     *profile.Profile
	theirSeq int
}

type recordingApp struct{ got []*codec.Message }

func (a *recordingApp) OnAppMessage(m *codec.Message) []Action { a.got = append(a.got, m); return nil }

func cfgFor(p *profile.Profile, reset bool) Config {
	return Config{
		SessionID:         "emu42",
		Profile:           p,
		SenderCompID:      "AGENT",
		TargetCompID:      "ORDERECHO",
		HeartbeatSec:      30,
		ResetOnLogon:      reset,
		LogonTimeout:      10 * time.Second,
		LogoutTimeout:     10 * time.Second,
		HeartbeatGracePct: 20,
	}
}

func newHarness(t *testing.T, reset bool, out, in int) *harness {
	t.Helper()
	h := &harness{t: t, clk: clock.NewFake(time.Time{}), seq: store.NewMemorySeqStore(out, in),
		msgs: store.NewMemoryMessageStore(), app: &recordingApp{}, prof: profile.FIX42}
	s, err := New(cfgFor(h.prof, reset), h.seq, h.msgs, h.clk, h.app)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	return h
}

// msg builds an inbound message from the counterparty (49=ORDERECHO 56=AGENT).
func (h *harness) msg(seq int, msgType string, body ...codec.Field) *codec.Message {
	return h.msgFrom(h.prof.BeginString, "ORDERECHO", "AGENT", seq, msgType, false, body...)
}

func (h *harness) msgFrom(begin, sender, target string, seq int, msgType string, possDup bool, body ...codec.Field) *codec.Message {
	h.t.Helper()
	orig := ""
	if possDup {
		orig = codec.FormatTime(h.clk.Now())
	}
	raw := codec.Encode(begin, msgType, codec.Header{SenderCompID: sender, TargetCompID: target,
		MsgSeqNum: seq, SendingTime: codec.FormatTime(h.clk.Now()), PossDup: possDup, OrigSendingTime: orig}, body)
	m, err := codec.DecodeOne(raw)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func (h *harness) logonReply(seq int, extra ...codec.Field) *codec.Message {
	body := append([]codec.Field{codec.F(98, "0"), codec.F(108, "30")}, extra...)
	return h.msg(seq, "A", body...)
}

// active drives the session to ACTIVE with a reset logon; next_in is 2 and
// next_out is 2 afterwards.
func (h *harness) active() {
	h.t.Helper()
	h.s.OnConnect()
	acts := h.s.OnMessage(h.logonReply(1, codec.F(141, "Y")))
	if h.s.State() != Active {
		h.t.Fatalf("not active: %s %v", h.s.State(), acts)
	}
	h.theirSeq = 1
}

func (h *harness) next(msgType string, body ...codec.Field) []Action {
	h.theirSeq++
	return h.s.OnMessage(h.msg(h.theirSeq, msgType, body...))
}

func sends(acts []Action) []Send {
	var out []Send
	for _, a := range acts {
		if s, ok := a.(Send); ok {
			out = append(out, s)
		}
	}
	return out
}

func evidences(acts []Action) []Evidence {
	var out []Evidence
	for _, a := range acts {
		if e, ok := a.(Evidence); ok {
			out = append(out, e)
		}
	}
	return out
}

func disconnects(acts []Action) []Disconnect {
	var out []Disconnect
	for _, a := range acts {
		if d, ok := a.(Disconnect); ok {
			out = append(out, d)
		}
	}
	return out
}

func findEvidence(acts []Action, event string) (Evidence, bool) {
	for _, e := range evidences(acts) {
		if e.Event == event {
			return e, true
		}
	}
	return Evidence{}, false
}

func mustOneSend(t *testing.T, acts []Action, msgType string) Send {
	t.Helper()
	ss := sends(acts)
	if len(ss) != 1 || ss[0].MsgType != msgType {
		t.Fatalf("want one 35=%s send, got %+v", msgType, describeSends(ss))
	}
	return ss[0]
}

func describeSends(ss []Send) []string {
	var out []string
	for _, s := range ss {
		out = append(out, codec.ToPipe(s.Raw))
	}
	return out
}

func assertLogoutThenDisconnect(t *testing.T, acts []Action, textPart string) {
	t.Helper()
	ss := sends(acts)
	if len(ss) != 1 || ss[0].MsgType != "5" {
		t.Fatalf("want a Logout, got %v", describeSends(ss))
	}
	if !strings.Contains(ss[0].Msg.Value(58), textPart) {
		t.Fatalf("Logout 58=%q, want it to contain %q", ss[0].Msg.Value(58), textPart)
	}
	ds := disconnects(acts)
	if len(ds) != 1 {
		t.Fatalf("want one Disconnect, got %v", acts)
	}
	// Logout must come before the Disconnect.
	iSend, iDisc := -1, -1
	for i, a := range acts {
		switch a.(type) {
		case Send:
			iSend = i
		case Disconnect:
			iDisc = i
		}
	}
	if iSend > iDisc {
		t.Fatal("Disconnect before Logout")
	}
}

// ------------------------------------------------------------ logon sent

func TestLogonSentOnConnectWithReset(t *testing.T) {
	h := newHarness(t, true, 7, 9)
	h.msgs.Append(store.Record{Seq: 3, MsgType: "8", Raw: "x"})
	acts := h.s.OnConnect()
	logon := mustOneSend(t, acts, "A")
	m := logon.Msg
	if m.Value(98) != "0" || m.Value(108) != "30" || m.Value(141) != "Y" {
		t.Fatalf("logon %s", logon.Msg.Pipe())
	}
	if logon.Seq != 1 || m.Value(34) != "1" || m.Value(49) != "AGENT" || m.Value(56) != "ORDERECHO" || m.Value(8) != "FIX.4.2" {
		t.Fatalf("logon header %s", logon.Msg.Pipe())
	}
	if h.s.State() != LogonSent {
		t.Fatalf("state %s", h.s.State())
	}
	if h.seq.NextOut != 2 || h.seq.NextIn != 1 {
		t.Fatalf("persisted %d/%d", h.seq.NextOut, h.seq.NextIn)
	}
	if h.msgs.Archives != 1 {
		t.Fatal("store not archived on reset")
	}
	if _, ok := findEvidence(acts, "store archived"); !ok {
		t.Fatal("no store archived evidence")
	}
	if r, ok := h.msgs.Get(1); !ok || r.MsgType != "A" {
		t.Fatal("logon not stored")
	}
}

func TestLogonSentOnConnectWithoutReset(t *testing.T) {
	h := newHarness(t, false, 7, 9)
	acts := h.s.OnConnect()
	logon := mustOneSend(t, acts, "A")
	if logon.Msg.Has(141) {
		t.Fatalf("unexpected 141: %s", logon.Msg.Pipe())
	}
	if logon.Seq != 7 {
		t.Fatalf("seq %d", logon.Seq)
	}
	if h.msgs.Archives != 0 {
		t.Fatal("store archived without reset")
	}
	if h.s.NextOut() != 8 || h.s.NextIn() != 9 {
		t.Fatalf("seqs %d/%d", h.s.NextOut(), h.s.NextIn())
	}
}

func TestFIX44LogonUsesProfile(t *testing.T) {
	clk := clock.NewFake(time.Time{})
	s, err := New(cfgFor(profile.FIX44, true), store.NewMemorySeqStore(1, 1), nil, clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	logon := mustOneSend(t, s.OnConnect(), "A")
	if logon.Msg.Value(8) != "FIX.4.4" {
		t.Fatal(logon.Msg.Pipe())
	}
}

// ------------------------------------------------------- logon reply

func TestValidLogonReply(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	acts := h.s.OnMessage(h.logonReply(1, codec.F(141, "Y")))
	if h.s.State() != Active {
		t.Fatalf("state %s", h.s.State())
	}
	if len(sends(acts)) != 0 || len(disconnects(acts)) != 0 {
		t.Fatalf("unexpected actions %v", acts)
	}
	if _, ok := findEvidence(acts, "logon accepted"); !ok {
		t.Fatal("no logon accepted evidence")
	}
	if h.s.NextIn() != 2 || h.s.NextOut() != 2 || h.seq.NextIn != 2 {
		t.Fatalf("seqs %d/%d", h.s.NextOut(), h.s.NextIn())
	}
	if h.s.HeartBtInt() != 30 || !h.s.Outcome().LoggedOn || h.s.Logons() != 1 {
		t.Fatal("outcome")
	}
}

func TestLogonReplyWrongCompIDs(t *testing.T) {
	for name, pair := range map[string][2]string{
		"sender": {"OTHER", "AGENT"},
		"target": {"ORDERECHO", "OTHER"},
	} {
		h := newHarness(t, true, 1, 1)
		h.s.OnConnect()
		m := h.msgFrom("FIX.4.2", pair[0], pair[1], 1, "A", false, codec.F(98, "0"), codec.F(108, "30"), codec.F(141, "Y"))
		acts := h.s.OnMessage(m)
		assertLogoutThenDisconnect(t, acts, "CompID problem")
		if h.s.Outcome().LoggedOn {
			t.Fatalf("%s: logged on", name)
		}
	}
}

func TestLogonReplyWrongBeginString(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	m := h.msgFrom("FIX.4.4", "ORDERECHO", "AGENT", 1, "A", false, codec.F(98, "0"), codec.F(108, "30"))
	acts := h.s.OnMessage(m)
	assertLogoutThenDisconnect(t, acts, "Incorrect BeginString, expected FIX.4.2")
}

func TestLogonReplyHeartBtInt(t *testing.T) {
	for _, hb := range []string{"0", "-5", "abc"} {
		h := newHarness(t, true, 1, 1)
		h.s.OnConnect()
		acts := h.s.OnMessage(h.msg(1, "A", codec.F(98, "0"), codec.F(108, hb), codec.F(141, "Y")))
		assertLogoutThenDisconnect(t, acts, "HeartBtInt")
		if h.s.State() == Active {
			t.Fatalf("108=%s accepted", hb)
		}
	}
	// A different interval is refused under heartbeat_mismatch: refuse.
	hr := newHarness(t, true, 1, 1)
	hr.s.cfg.HeartbeatMismatch = HeartbeatMismatchRefuse
	hr.s.OnConnect()
	assertLogoutThenDisconnect(t, hr.s.OnMessage(hr.msg(1, "A", codec.F(98, "0"), codec.F(108, "60"), codec.F(141, "Y"))), "HeartBtInt mismatch")
	// Missing 108 entirely.
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	acts := h.s.OnMessage(h.msg(1, "A", codec.F(98, "0"), codec.F(141, "Y")))
	assertLogoutThenDisconnect(t, acts, "HeartBtInt")
}

func TestLogonTimeout(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	h.clk.Advance(9900 * time.Millisecond)
	if acts := h.s.OnTimer(); len(acts) != 0 {
		t.Fatalf("early: %v", acts)
	}
	h.clk.Advance(100 * time.Millisecond)
	acts := h.s.OnTimer()
	ds := disconnects(acts)
	if len(ds) != 1 || ds[0].Reason != "Logon timeout" {
		t.Fatalf("got %v", acts)
	}
	if len(sends(acts)) != 0 {
		t.Fatal("sent something on logon timeout")
	}
}

func TestLogoutInsteadOfLogon(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	// The strict broker answers on its own version with its own seq.
	m := h.msgFrom("FIX.4.2", "STRICTBRK", "AGENT", 17, "5", false, codec.F(58, "Incorrect BeginString, expected FIX.4.2"))
	acts := h.s.OnMessage(m)
	// A2 3.5: we answer their Logout with ours, then disconnect.
	assertLogoutThenDisconnect(t, acts, "Logout acknowledged")
	if lo := sends(acts)[0]; lo.Seq != 2 || lo.Msg.Value(56) != "ORDERECHO" {
		t.Fatalf("logout reply %s", lo.Msg.Pipe())
	}
	ev, ok := findEvidence(acts, "logon refused")
	if !ok || !strings.Contains(ev.Detail, "Incorrect BeginString, expected FIX.4.2") {
		t.Fatalf("evidence %+v", ev)
	}
	o := h.s.Outcome()
	if !o.Refused || o.RefusalText != "Incorrect BeginString, expected FIX.4.2" || o.LoggedOn || o.CleanLogout {
		t.Fatalf("outcome %+v", o)
	}
}

func TestGapOnLogonReply(t *testing.T) {
	h := newHarness(t, false, 5, 3)
	h.s.OnConnect() // our logon is seq 5
	acts := h.s.OnMessage(h.logonReply(7))
	if h.s.State() != Active {
		t.Fatalf("state %s", h.s.State())
	}
	rr := mustOneSend(t, acts, "2")
	if rr.Msg.Value(7) != "3" || rr.Msg.Value(16) != "0" || rr.Seq != 6 {
		t.Fatalf("resend %s", rr.Msg.Pipe())
	}
	// logon accepted is recorded before the ResendRequest goes out.
	iAcc, iRR := -1, -1
	for i, a := range acts {
		if e, ok := a.(Evidence); ok && e.Event == "logon accepted" {
			iAcc = i
		}
		if s, ok := a.(Send); ok && s.MsgType == "2" {
			iRR = i
		}
	}
	if iAcc == -1 || iAcc > iRR {
		t.Fatal("ResendRequest before logon accepted")
	}
	if h.s.NextIn() != 3 || !h.s.ResendOutstanding() {
		t.Fatalf("next_in %d", h.s.NextIn())
	}
}

func TestLogonReplySeqTooLow(t *testing.T) {
	h := newHarness(t, false, 5, 10)
	h.s.OnConnect()
	acts := h.s.OnMessage(h.logonReply(4))
	assertLogoutThenDisconnect(t, acts, "MsgSeqNum too low, expecting 10 but received 4")
}

func TestFirstMessageNotLogon(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	acts := h.s.OnMessage(h.msg(1, "0"))
	if len(disconnects(acts)) != 1 || len(sends(acts)) != 0 {
		t.Fatalf("got %v", acts)
	}
}

// --------------------------------------------- heartbeat / TestRequest

func TestHeartbeatAfterSilence(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.clk.Advance(29 * time.Second)
	if len(sends(h.s.OnTimer())) != 0 {
		t.Fatal("heartbeat too early")
	}
	h.next("0") // inbound traffic keeps the TestRequest away
	h.clk.Advance(1 * time.Second)
	hb := mustOneSend(t, h.s.OnTimer(), "0")
	if hb.Msg.Has(112) {
		t.Fatal("unsolicited heartbeat carries 112")
	}
}

func TestTestRequestAndTimeout(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	// Keep sending so heartbeats do not interfere; silence inbound for 36s.
	h.clk.Advance(35 * time.Second)
	acts := h.s.OnTimer() // heartbeat only (grace is 36s)
	if ss := sends(acts); len(ss) != 1 || ss[0].MsgType != "0" {
		t.Fatalf("at 35s: %v", describeSends(ss))
	}
	h.clk.Advance(1 * time.Second)
	tr := mustOneSend(t, h.s.OnTimer(), "1")
	if tr.Msg.Value(112) != "TEST-1" || h.s.PendingTestReqID() != "TEST-1" {
		t.Fatalf("test request %s", tr.Msg.Pipe())
	}
	// No second TestRequest while one is pending.
	h.clk.Advance(29 * time.Second)
	for _, s := range sends(h.s.OnTimer()) {
		if s.MsgType == "1" {
			t.Fatal("second TestRequest while pending")
		}
	}
	h.clk.Advance(1 * time.Second)
	acts = h.s.OnTimer()
	ds := disconnects(acts)
	if len(ds) != 1 || ds[0].Reason != "TestRequest timeout" {
		t.Fatalf("got %v", acts)
	}
}

func TestTestRequestAnswered(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	id, acts := h.s.SendTestRequest()
	tr := mustOneSend(t, acts, "1")
	if id != "TEST-1" || tr.Msg.Value(112) != "TEST-1" {
		t.Fatal(tr.Msg.Pipe())
	}
	acts = h.next("0", codec.F(112, "TEST-1"))
	if _, ok := findEvidence(acts, "testrequest answered"); !ok || h.s.PendingTestReqID() != "" {
		t.Fatalf("not answered: %v", acts)
	}
	id2, _ := h.s.SendTestRequest()
	if id2 != "TEST-2" {
		t.Fatalf("counter: %s", id2)
	}
}

func TestInboundTestRequestAnswered(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	hb := mustOneSend(t, h.next("1", codec.F(112, "EMU-7")), "0")
	if hb.Msg.Value(112) != "EMU-7" {
		t.Fatal(hb.Msg.Pipe())
	}
}

// ------------------------------------------------------ sequence rules

func TestGapSendsSingleResendRequest(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active() // next_in = 2
	acts := h.s.OnMessage(h.msg(5, "0"))
	rr := mustOneSend(t, acts, "2")
	if rr.Msg.Value(7) != "2" || rr.Msg.Value(16) != "0" {
		t.Fatal(rr.Msg.Pipe())
	}
	if h.s.NextIn() != 2 {
		t.Fatalf("expected advanced on gap: %d", h.s.NextIn())
	}
	acts = h.s.OnMessage(h.msg(6, "0"))
	if len(sends(acts)) != 0 {
		t.Fatalf("second ResendRequest: %v", describeSends(sends(acts)))
	}
	if _, ok := findEvidence(acts, "resend already outstanding"); !ok {
		t.Fatal("no outstanding evidence")
	}
	// The counterparty gap-fills 2..6 and resends 6.
	gf := h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "4", true, codec.F(123, "Y"), codec.F(36, "7"))
	h.s.OnMessage(gf)
	if h.s.NextIn() != 7 || h.s.ResendOutstanding() {
		t.Fatalf("after gap fill next_in=%d outstanding=%v", h.s.NextIn(), h.s.ResendOutstanding())
	}
}

func TestSeqTooLow(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.next("0")
	h.next("0") // next_in = 4
	acts := h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "0", true))
	if len(sends(acts)) != 0 || len(disconnects(acts)) != 0 {
		t.Fatalf("possdup not ignored: %v", acts)
	}
	if _, ok := findEvidence(acts, "possdup ignored"); !ok || h.s.NextIn() != 4 {
		t.Fatal("possdup evidence / next_in")
	}
	acts = h.s.OnMessage(h.msg(2, "0"))
	assertLogoutThenDisconnect(t, acts, "MsgSeqNum too low, expecting 4 but received 2")
}

func TestSequenceResetModes(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active() // next_in 2
	// Gap fill mode: normal seq check, then jump.
	h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "4", true, codec.F(123, "Y"), codec.F(36, "10")))
	if h.s.NextIn() != 10 {
		t.Fatalf("gap fill: next_in %d", h.s.NextIn())
	}
	// Reset mode ignores 34 entirely (even far too low).
	acts := h.s.OnMessage(h.msg(1, "4", codec.F(36, "20")))
	if h.s.NextIn() != 20 || len(sends(acts)) != 0 {
		t.Fatalf("reset mode: next_in %d %v", h.s.NextIn(), acts)
	}
	if h.seq.NextIn != 20 {
		t.Fatal("not persisted")
	}
	// NewSeqNo lower than expected -> Reject 373=5, expected unchanged.
	acts = h.s.OnMessage(h.msg(99, "4", codec.F(36, "15")))
	rej := mustOneSend(t, acts, "3")
	if rej.Msg.Value(373) != "5" || rej.Msg.Value(58) != "NewSeqNo too low" || rej.Msg.Value(45) != "99" {
		t.Fatal(rej.Msg.Pipe())
	}
	if h.s.NextIn() != 20 {
		t.Fatalf("next_in changed: %d", h.s.NextIn())
	}
}

// ------------------------------------------------------ resend replay

// stored builds an outbound message as the session would have sent it at a
// given seq and puts it in the store.
func (h *harness) stored(seq int, msgType string, sentAt time.Time, injected bool, body ...codec.Field) []byte {
	raw := codec.Encode("FIX.4.2", msgType, codec.Header{SenderCompID: "AGENT", TargetCompID: "ORDERECHO",
		MsgSeqNum: seq, SendingTime: codec.FormatTime(sentAt)}, body)
	h.msgs.Append(store.Record{Seq: seq, MsgType: msgType, FixVersion: "FIX.4.2", Raw: string(raw), Injected: injected})
	return raw
}

func replayHarness(t *testing.T) (*harness, map[int][]byte) {
	h := newHarness(t, false, 1, 1)
	h.s.OnConnect() // seq 1 = Logon
	h.s.OnMessage(h.logonReply(1))
	h.theirSeq = 1
	t0 := h.clk.Now()
	orig := map[int][]byte{}
	// 1 A (stored by OnConnect), 2 0, 3 D, 4 D, 5 0, 6 1, 7 D, 8 missing, 9 D
	h.stored(2, "0", t0, false)
	orig[3] = h.stored(3, "D", t0.Add(1*time.Second), false, codec.F(11, "C1"), codec.F(55, "AAPL"))
	orig[4] = h.stored(4, "D", t0.Add(2*time.Second), false, codec.F(11, "C2"), codec.F(55, "MSFT"))
	h.stored(5, "0", t0, false)
	h.stored(6, "1", t0, false, codec.F(112, "TEST-9"))
	orig[7] = h.stored(7, "D", t0.Add(3*time.Second), true, codec.F(11, "C3"), codec.F(9999, "FOO"))
	orig[9] = h.stored(9, "D", t0.Add(4*time.Second), false, codec.F(11, "C4"))
	h.s.nextOut = 10
	h.clk.Advance(1 * time.Minute)
	return h, orig
}

func TestResendRequestReplay(t *testing.T) {
	h, orig := replayHarness(t)
	storedBefore := h.msgs.Len()
	acts := h.next("2", codec.F(7, "1"), codec.F(16, "0"))
	ss := sends(acts)
	type exp struct {
		seq     int
		msgType string
		newSeq  int
	}
	want := []exp{{1, "4", 3}, {3, "D", 0}, {4, "D", 0}, {5, "4", 7}, {7, "D", 0}, {8, "4", 9}, {9, "D", 0}}
	if len(ss) != len(want) {
		t.Fatalf("got %d sends: %v", len(ss), describeSends(ss))
	}
	now := codec.FormatTime(h.clk.Now())
	for i, w := range want {
		s := ss[i]
		if s.Seq != w.seq || s.MsgType != w.msgType || s.Msg.Value(34) != strconv.Itoa(w.seq) {
			t.Fatalf("send %d: %s", i, s.Msg.Pipe())
		}
		if s.Msg.Value(43) != "Y" || s.Msg.Value(52) != now {
			t.Fatalf("send %d: 43/52 %s", i, s.Msg.Pipe())
		}
		if _, err := codec.DecodeOne(s.Raw); err != nil {
			t.Fatalf("send %d invalid framing: %v", i, err)
		}
		if w.msgType == "4" {
			if s.Msg.Value(123) != "Y" || s.Msg.Value(36) != strconv.Itoa(w.newSeq) || s.Msg.Value(122) != now {
				t.Fatalf("gap fill %d: %s", i, s.Msg.Pipe())
			}
			continue
		}
		o, _ := codec.DecodeOne(orig[w.seq])
		if s.Msg.Value(122) != o.Value(52) {
			t.Fatalf("replay %d: 122=%s want original 52 %s", w.seq, s.Msg.Value(122), o.Value(52))
		}
		// Every other field identical, in order.
		strip := func(m *codec.Message) []codec.Field {
			var out []codec.Field
			for _, f := range m.Fields {
				switch f.Tag {
				case 9, 10, 43, 52, 122:
					continue
				}
				out = append(out, f)
			}
			return out
		}
		a, b := strip(s.Msg), strip(o)
		if len(a) != len(b) {
			t.Fatalf("replay %d fields differ", w.seq)
		}
		for j := range a {
			if a[j] != b[j] {
				t.Fatalf("replay %d field %d: %v vs %v", w.seq, j, a[j], b[j])
			}
		}
		// Header order 8,9,35,49,56,34,52,43,122.
		var tags []int
		for _, f := range s.Msg.Fields[:9] {
			tags = append(tags, f.Tag)
		}
		if got := fmtInts(tags); got != "8 9 35 49 56 34 52 43 122" {
			t.Fatalf("replay %d header order %s", w.seq, got)
		}
	}
	// Injected original keeps its mutation and says so.
	if !ss[4].Injected || ss[4].Detail != "replay of injected seq 7" || ss[4].Msg.Value(9999) != "FOO" {
		t.Fatalf("injected replay: %+v", ss[4].Detail)
	}
	if ss[1].Injected || ss[1].Detail != "replay of seq 3" {
		t.Fatalf("plain replay detail %q", ss[1].Detail)
	}
	if h.s.NextOut() != 10 {
		t.Fatalf("replay consumed next_out: %d", h.s.NextOut())
	}
	if h.msgs.Len() != storedBefore {
		t.Fatal("replay/gap fill stored")
	}
	if e, ok := findEvidence(acts, "resend summary"); !ok || !strings.Contains(e.Detail, "1..9 -> replayed 4, gap-filled 3 run(s)") {
		t.Fatalf("summary %+v", e)
	}
}

func fmtInts(v []int) string {
	var parts []string
	for _, n := range v {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, " ")
}

func TestResendRequestBounded(t *testing.T) {
	h, _ := replayHarness(t)
	ss := sends(h.next("2", codec.F(7, "2"), codec.F(16, "4")))
	if len(ss) != 3 || ss[0].MsgType != "4" || ss[0].Msg.Value(36) != "3" || ss[1].Seq != 3 || ss[2].Seq != 4 {
		t.Fatalf("bounded: %v", describeSends(ss))
	}
	// 16 beyond what we sent is clamped to next_out-1.
	ss = sends(h.next("2", codec.F(7, "9"), codec.F(16, "50")))
	if len(ss) != 1 || ss[0].Seq != 9 || ss[0].MsgType != "D" {
		t.Fatalf("clamped: %v", describeSends(ss))
	}
	// B > end -> nothing sent.
	acts := h.next("2", codec.F(7, "12"), codec.F(16, "0"))
	if len(sends(acts)) != 0 {
		t.Fatalf("future range: %v", describeSends(sends(acts)))
	}
	if _, ok := findEvidence(acts, "resend request for future seqnums ignored"); !ok {
		t.Fatal("no evidence for future range")
	}
}

func TestResendRequestAllAdminCollapses(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.s.OnTimer()
	h.clk.Advance(30 * time.Second)
	h.next("0")
	h.s.OnTimer() // heartbeat seq 2
	ss := sends(h.next("2", codec.F(7, "1"), codec.F(16, "0")))
	if len(ss) != 1 || ss[0].MsgType != "4" || ss[0].Seq != 1 || ss[0].Msg.Value(36) != "3" {
		t.Fatalf("got %v", describeSends(ss))
	}
}

func TestResendWithoutStoreGapFills(t *testing.T) {
	clk := clock.NewFake(time.Time{})
	s, _ := New(cfgFor(profile.FIX42, true), store.NewMemorySeqStore(1, 1), nil, clk, nil)
	h := &harness{t: t, clk: clk, prof: profile.FIX42, s: s}
	h.active()
	ss := sends(h.next("2", codec.F(7, "1"), codec.F(16, "0")))
	if len(ss) != 1 || ss[0].Msg.Value(36) != "2" {
		t.Fatalf("got %v", describeSends(ss))
	}
}

// --------------------------------------------------------------- logout

func TestCounterpartyLogout(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	acts := h.next("5", codec.F(58, "Logout via control API"))
	lo := mustOneSend(t, acts, "5")
	if lo.Seq != 2 {
		t.Fatalf("logout seq %d", lo.Seq)
	}
	if len(disconnects(acts)) != 1 {
		t.Fatal("no disconnect")
	}
	o := h.s.Outcome()
	if !o.CleanLogout || o.LogoutByUs || o.LogoutText != "Logout via control API" {
		t.Fatalf("outcome %+v", o)
	}
}

func TestInitiatedLogoutConfirmed(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	lo := mustOneSend(t, h.s.InitiateLogout("bye"), "5")
	if lo.Msg.Value(58) != "bye" || h.s.State() != LogoutSent {
		t.Fatal(lo.Msg.Pipe())
	}
	acts := h.next("5", codec.F(58, "Logout acknowledged"))
	if len(sends(acts)) != 0 || len(disconnects(acts)) != 1 {
		t.Fatalf("got %v", acts)
	}
	if o := h.s.Outcome(); !o.CleanLogout || !o.LogoutByUs {
		t.Fatalf("outcome %+v", o)
	}
}

func TestInitiatedLogoutTimeout(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.s.InitiateLogout("bye")
	h.clk.Advance(9 * time.Second)
	if len(disconnects(h.s.OnTimer())) != 0 {
		t.Fatal("early logout timeout")
	}
	h.clk.Advance(1 * time.Second)
	ds := disconnects(h.s.OnTimer())
	if len(ds) != 1 || ds[0].Reason != "Logout timeout" {
		t.Fatalf("got %v", ds)
	}
	if h.s.Outcome().CleanLogout {
		t.Fatal("timeout counted as clean")
	}
}

func TestLogoutWhileLogonSentDisconnects(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	acts := h.s.InitiateLogout("shutting down")
	if len(sends(acts)) != 0 || len(disconnects(acts)) != 1 {
		t.Fatalf("got %v", acts)
	}
}

// ------------------------------------------------------ inbound misc

func TestApplicationMessageToAppNeverRejected(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	acts := h.next("8", codec.F(37, "O-1"), codec.F(150, "0"))
	if len(sends(acts)) != 0 {
		t.Fatalf("replied to ER: %v", describeSends(sends(acts)))
	}
	if len(h.app.got) != 1 || h.app.got[0].Value(37) != "O-1" {
		t.Fatal("app hook not called")
	}
	if _, ok := findEvidence(acts, "application message received"); !ok {
		t.Fatal("no evidence")
	}
	if h.s.NextIn() != 3 {
		t.Fatalf("next_in %d", h.s.NextIn())
	}
}

func TestInboundSessionReject(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	acts := h.next("3", codec.F(45, "5"), codec.F(373, "1"), codec.F(58, "Required tag missing"))
	e, ok := findEvidence(acts, "reject received")
	if !ok || e.Level != Warning || !strings.Contains(e.Detail, "45=5") || !strings.Contains(e.Detail, "Required tag missing") {
		t.Fatalf("evidence %+v", e)
	}
	if len(sends(acts)) != 0 {
		t.Fatal("replied to Reject")
	}
}

func TestLogonWhileActiveRejected(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	rej := mustOneSend(t, h.next("A", codec.F(98, "0"), codec.F(108, "30")), "3")
	if rej.Msg.Value(58) != "Logon received while already logged on" {
		t.Fatal(rej.Msg.Pipe())
	}
}

func TestWrongCompIDWhileActive(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	acts := h.s.OnMessage(h.msgFrom("FIX.4.2", "EVIL", "AGENT", 2, "0", false))
	ss := sends(acts)
	if len(ss) != 2 || ss[0].MsgType != "3" || ss[0].Msg.Value(373) != "9" || ss[1].MsgType != "5" {
		t.Fatalf("got %v", describeSends(ss))
	}
	if len(disconnects(acts)) != 1 {
		t.Fatal("no disconnect")
	}
}

func TestBeginStringMismatchWhileActive(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	acts := h.s.OnMessage(h.msgFrom("FIX.4.4", "ORDERECHO", "AGENT", 2, "0", false))
	assertLogoutThenDisconnect(t, acts, "Incorrect BeginString, expected FIX.4.2")
}

func TestMissingHeaderTagRejected(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	raw := codec.Build("FIX.4.2", []codec.Field{codec.F(35, "0"), codec.F(49, "ORDERECHO"), codec.F(56, "AGENT"), codec.F(34, "2")}, false)
	m, _ := codec.DecodeOne(raw)
	rej := mustOneSend(t, h.s.OnMessage(m), "3")
	if rej.Msg.Value(373) != "1" || !strings.Contains(rej.Msg.Value(58), "52") {
		t.Fatal(rej.Msg.Pipe())
	}
}

func TestDiscardedFrame(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	acts := h.s.OnDiscarded(&codec.DiscardedFrame{Raw: []byte("junk"), Reason: "Bad CheckSum (10): declared 000, computed 123"})
	e, ok := findEvidence(acts, "frame discarded")
	if !ok || e.Level != Warning || len(sends(acts)) != 0 {
		t.Fatalf("got %v", acts)
	}
}

// ---------------------------------------------------------- reconnect

func TestReconnectDoesNotReset(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.next("0")
	h.s.OnTimer()
	h.s.OnDisconnect()
	out, in := h.s.NextOut(), h.s.NextIn()
	if out != 2 || in != 3 {
		t.Fatalf("before reconnect %d/%d", out, in)
	}
	logon := mustOneSend(t, h.s.OnConnect(), "A")
	if logon.Msg.Has(141) || logon.Seq != 2 {
		t.Fatalf("reconnect logon %s", logon.Msg.Pipe())
	}
	h.s.OnMessage(h.logonReply(3))
	if h.s.State() != Active || h.s.NextIn() != 4 || h.s.Logons() != 2 {
		t.Fatalf("state %s next_in %d", h.s.State(), h.s.NextIn())
	}
}

func TestResetRetriedUntilLogonAccepted(t *testing.T) {
	h := newHarness(t, true, 4, 4)
	h.s.OnConnect()
	h.s.OnDisconnect() // dropped before any reply
	logon := mustOneSend(t, h.s.OnConnect(), "A")
	if logon.Msg.Value(141) != "Y" || logon.Seq != 1 {
		t.Fatalf("second attempt %s", logon.Msg.Pipe())
	}
}

func TestRequestResetForcesReset(t *testing.T) {
	h := newHarness(t, false, 4, 4)
	h.s.RequestReset()
	logon := mustOneSend(t, h.s.OnConnect(), "A")
	if logon.Msg.Value(141) != "Y" || logon.Seq != 1 {
		t.Fatal(logon.Msg.Pipe())
	}
}

// ------------------------------------------------------------ injection

func TestSkipOutboundSeq(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active() // next_out 2
	acts := h.s.SkipOutboundSeq(3)
	e, ok := findEvidence(acts, "injected seq gap")
	if !ok || !e.Injected || len(sends(acts)) != 0 {
		t.Fatalf("got %v", acts)
	}
	if h.s.NextOut() != 5 || h.seq.NextOut != 5 {
		t.Fatalf("next_out %d", h.s.NextOut())
	}
	_, acts = h.s.SendTestRequest()
	if s := mustOneSend(t, acts, "1"); s.Seq != 5 {
		t.Fatalf("seq after skip %d", s.Seq)
	}
	// The counterparty asks for 2..0: the skipped seqs collapse into one gap
	// fill, and the TestRequest at 5 (admin) joins the run.
	ss := sends(h.next("2", codec.F(7, "2"), codec.F(16, "0")))
	if len(ss) != 1 || ss[0].Seq != 2 || ss[0].Msg.Value(36) != "6" {
		t.Fatalf("got %v", describeSends(ss))
	}
}

func TestSendRaw(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	acts, err := h.s.SendRaw([]codec.Field{codec.F(35, "D"), codec.F(11, "X1"), codec.F(55, "ZVZZT")})
	if err != nil {
		t.Fatal(err)
	}
	s := mustOneSend(t, acts, "D")
	if !s.Injected || s.Seq != 2 || s.Msg.Value(11) != "X1" || s.Msg.Value(49) != "AGENT" {
		t.Fatalf("send %+v %s", s.Injected, s.Msg.Pipe())
	}
	if e, ok := findEvidence(acts, "injected message"); !ok || !e.Injected {
		t.Fatal("no injected evidence")
	}
	if r, ok := h.msgs.Get(2); !ok || !r.Injected || !bytes.Equal([]byte(r.Raw), s.Raw) {
		t.Fatal("not stored as injected")
	}
	if _, err := h.s.SendRaw([]codec.Field{codec.F(35, "D"), codec.F(34, "99")}); err == nil {
		t.Fatal("header tag accepted")
	}
	if _, err := h.s.SendRaw([]codec.Field{codec.F(11, "x")}); err == nil {
		t.Fatal("missing 35 accepted")
	}
}

func TestSeqnumsPersistedOnEveryChange(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.next("1", codec.F(112, "X"))
	if h.seq.NextOut != h.s.NextOut() || h.seq.NextIn != h.s.NextIn() {
		t.Fatalf("store %d/%d session %d/%d", h.seq.NextOut, h.seq.NextIn, h.s.NextOut(), h.s.NextIn())
	}
}

// ------------------------------------------------------------- A2 3.2

func TestHeartBtIntMismatchWarnsByDefault(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.s.OnConnect()
	acts := h.s.OnMessage(h.msg(1, "A", codec.F(98, "0"), codec.F(108, "60"), codec.F(141, "Y")))
	e, ok := findEvidence(acts, "heartbeat mismatch")
	if !ok || e.Level != Warning || h.s.State() != Active || h.s.HeartBtInt() != 30 {
		t.Fatalf("state %s hb %d %v", h.s.State(), h.s.HeartBtInt(), acts)
	}
	if len(sends(acts)) != 0 {
		t.Fatal("sent on mismatch warn")
	}
}

// ------------------------------------------------------------- A2 3.1

func TestHeldMessageReleasedAfterGapFill(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active() // next_in 2
	// The emulator skipped 2..4; its TestRequest arrives on 5.
	acts := h.s.OnMessage(h.msg(5, "1", codec.F(112, "EMU-1")))
	if len(sends(acts)) != 1 || sends(acts)[0].MsgType != "2" {
		t.Fatalf("want only a ResendRequest, got %v", describeSends(sends(acts)))
	}
	if _, ok := findEvidence(acts, "message queued"); !ok || h.s.Held() != 1 {
		t.Fatal("not queued")
	}
	// Its gap fill covers 2..5 (the TestRequest is admin), NewSeqNo 6.
	acts = h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "4", true, codec.F(123, "Y"), codec.F(36, "6")))
	hb := mustOneSend(t, acts, "0")
	if hb.Msg.Value(112) != "EMU-1" {
		t.Fatalf("heartbeat %s", hb.Msg.Pipe())
	}
	if _, ok := findEvidence(acts, "message dequeued"); !ok {
		t.Fatal("no dequeue evidence")
	}
	if h.s.NextIn() != 6 || h.s.Held() != 0 || h.s.ResendOutstanding() {
		t.Fatalf("next_in %d held %d outstanding %v", h.s.NextIn(), h.s.Held(), h.s.ResendOutstanding())
	}
}

func TestHeldMessagesProcessedInSeqOrder(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active() // next_in 2
	h.s.OnMessage(h.msg(4, "1", codec.F(112, "B")))
	h.s.OnMessage(h.msg(3, "1", codec.F(112, "A")))
	if h.s.Held() != 2 {
		t.Fatalf("held %d", h.s.Held())
	}
	// Seq 2 arrives (resent): 2, then held 3 and 4 in order.
	acts := h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "0", true))
	ss := sends(acts)
	if len(ss) != 2 || ss[0].Msg.Value(112) != "A" || ss[1].Msg.Value(112) != "B" {
		t.Fatalf("got %v", describeSends(ss))
	}
	if h.s.NextIn() != 5 || h.s.ResendOutstanding() {
		t.Fatalf("next_in %d", h.s.NextIn())
	}
}

func TestHeldOriginalDroppedWhenReplayed(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.s.OnMessage(h.msg(3, "8", codec.F(37, "O-1"), codec.F(17, "E-3")))
	h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "4", true, codec.F(123, "Y"), codec.F(36, "3")))
	// 3 was dequeued in sequence on the gap fill -> processed once.
	if len(h.app.got) != 1 || h.s.NextIn() != 4 {
		t.Fatalf("app got %d next_in %d", len(h.app.got), h.s.NextIn())
	}
	// A replay of 3 afterwards is a possdup below expected: ignored.
	h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 3, "8", true, codec.F(37, "O-1"), codec.F(17, "E-3")))
	if len(h.app.got) != 1 {
		t.Fatal("replay processed twice")
	}
	// Held original dropped when its replay was processed first.
	h.s.OnMessage(h.msg(7, "8", codec.F(17, "E-7")))                                                             // held, RR sent
	h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 4, "4", true, codec.F(123, "Y"), codec.F(36, "7"))) // 4..6 filled
	if len(h.app.got) != 2 || h.s.NextIn() != 8 {
		t.Fatalf("app got %d next_in %d", len(h.app.got), h.s.NextIn())
	}
	h.s.OnMessage(h.msg(10, "8", codec.F(17, "E-10")))
	h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 8, "4", true, codec.F(123, "Y"), codec.F(36, "10")))
	acts := h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 10, "8", true, codec.F(17, "E-10")))
	_ = acts
	if len(h.app.got) != 3 || h.s.NextIn() != 11 {
		t.Fatalf("app got %d next_in %d", len(h.app.got), h.s.NextIn())
	}
}

func TestHeldCoveredByGapFillProcessedNotLost(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.s.OnMessage(h.msg(4, "8", codec.F(17, "E-4"))) // an ER behind a gap
	// A gapfill-mode counterparty skips over it.
	acts := h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "4", true, codec.F(123, "Y"), codec.F(36, "5")))
	if len(h.app.got) != 1 || h.app.got[0].Value(17) != "E-4" {
		t.Fatalf("held ER lost: %v", acts)
	}
	e, ok := findEvidence(acts, "message dequeued")
	if !ok || !strings.Contains(e.Detail, "covered by a gap fill") {
		t.Fatalf("%+v", e)
	}
}

func TestQueueOverflow(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	for i := 0; i < MaxHeld; i++ {
		acts := h.s.OnMessage(h.msg(10+i, "0"))
		if len(disconnects(acts)) != 0 {
			t.Fatalf("disconnected after %d", i)
		}
	}
	acts := h.s.OnMessage(h.msg(10+MaxHeld, "0"))
	assertLogoutThenDisconnect(t, acts, "Resend queue overflow")
}

func TestHeldDroppedOnDisconnect(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()
	h.s.OnMessage(h.msg(9, "0"))
	acts := h.s.OnDisconnect()
	if _, ok := findEvidence(acts, "held messages dropped on disconnect"); !ok || h.s.Held() != 0 {
		t.Fatal("held not cleared")
	}
}

func TestSendApp(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	if _, _, err := h.s.SendApp("D", nil); err == nil {
		t.Fatal("sent while disconnected")
	}
	h.active()
	snd, acts, err := h.s.SendApp("D", []codec.Field{codec.F(11, "C1")})
	if err != nil || snd.Seq != 2 || snd.Injected || len(sends(acts)) != 1 {
		t.Fatalf("%v %+v", err, snd)
	}
	if r, ok := h.msgs.Get(2); !ok || r.Injected {
		t.Fatal("not stored")
	}
	if _, _, err := h.s.SendApp("0", nil); err == nil {
		t.Fatal("admin accepted")
	}
	acts, err = h.s.SendResendRequest(1, 0)
	if err != nil || mustOneSend(t, acts, "2").Msg.Value(7) != "1" {
		t.Fatal(err)
	}
}

func TestHeldOriginalDroppedAfterItsReplay(t *testing.T) {
	h := newHarness(t, true, 1, 1)
	h.active()                                                                                  // next_in 2
	h.s.OnMessage(h.msg(3, "8", codec.F(17, "E-3")))                                            // held
	h.s.OnMessage(h.msgFrom("FIX.4.2", "ORDERECHO", "AGENT", 2, "8", true, codec.F(17, "E-2"))) // replay of 2
	if len(h.app.got) != 2 {                                                                    // 2, then held 3 dequeued
		t.Fatalf("app got %d", len(h.app.got))
	}
	h2 := newHarness(t, true, 1, 1)
	h2.active()
	h2.s.OnMessage(h2.msg(2+1, "8", codec.F(17, "E-3")))
	// The counterparty resends 3 too (16=0) and it arrives before the held copy drains:
	// simulate by replaying 2 as a gap fill to 3 and then 3 itself.
	h2.s.held[3] = h2.msg(3, "8", codec.F(17, "E-3"))
	h2.s.processed[3] = true
	h2.s.nextIn = 4
	acts := h2.s.drain()
	if e, ok := findEvidence(acts, "held message dropped"); !ok || !strings.Contains(e.Detail, "already processed") {
		t.Fatalf("%v", acts)
	}
}
