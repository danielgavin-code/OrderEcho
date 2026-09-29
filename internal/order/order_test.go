package order

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden (capture once, then keep byte-identical)")

var t0 = time.Date(2026, 9, 29, 9, 30, 0, 0, time.UTC)

type h struct {
	t       *testing.T
	m       *Manager
	prof    *profile.Profile
	clk     *clock.FakeClock
	ourSeq  int
	theirSq int
}

func newH(t *testing.T, p *profile.Profile, opt profile.OrderOptions) *h {
	clk := clock.NewFake(t0)
	m := NewManager(Options{Session: "emu", Profile: p, IDs: &IDs{Prefix: "OE", RunID: "20260929-093000"},
		Clock: clk, Order: opt, SenderID: "AGENT"})
	return &h{t: t, m: m, prof: p, clk: clk, ourSeq: 1, theirSq: 1}
}

func (x *h) encode(msgType, sender, target string, seq int, body []codec.Field, possDup bool) *codec.Message {
	orig := ""
	if possDup {
		orig = codec.FormatTime(x.clk.Now())
	}
	raw := codec.Encode(x.prof.BeginString, msgType, codec.Header{SenderCompID: sender, TargetCompID: target,
		MsgSeqNum: seq, SendingTime: codec.FormatTime(x.clk.Now()), PossDup: possDup, OrigSendingTime: orig}, body)
	m, err := codec.DecodeOne(raw)
	if err != nil {
		x.t.Fatal(err)
	}
	return m
}

// sent simulates the session sending body (SendApp -> OnAppSent).
func (x *h) sent(msgType string, body []codec.Field, injected bool) session.Send {
	x.ourSeq++
	msg := x.encode(msgType, "AGENT", "ORDERECHO", x.ourSeq, body, false)
	s := session.Send{Seq: x.ourSeq, MsgType: msgType, Raw: msg.Raw, Msg: msg, Injected: injected}
	x.m.OnAppSent(s)
	return s
}

func (x *h) in(msgType string, body ...codec.Field) []session.Action {
	x.theirSq++
	msg := x.encode(msgType, "ORDERECHO", "AGENT", x.theirSq, body, false)
	if msgType == "3" {
		return x.m.OnSessionReject(msg)
	}
	return x.m.OnAppMessage(msg)
}

var execN int

func (x *h) er(clOrdID, orig, orderID, execType, status, qty, lastQty, lastPx, cum, leaves, avg string, extra ...codec.Field) []session.Action {
	execN++
	body := []codec.Field{codec.F(37, orderID), codec.F(11, clOrdID)}
	if orig != "" {
		body = append(body, codec.F(41, orig))
	}
	body = append(body, codec.F(17, "E-"+strconv.Itoa(execN)))
	if x.prof.Name == "FIX.4.2" {
		body = append(body, codec.F(20, "0"))
	}
	body = append(body, codec.F(150, execType), codec.F(39, status), codec.F(55, "AAPL"), codec.F(54, "1"),
		codec.F(38, qty), codec.F(40, "2"), codec.F(32, lastQty), codec.F(31, lastPx), codec.F(151, leaves),
		codec.F(14, cum), codec.F(6, avg), codec.F(60, "20260929-09:30:00.000"))
	body = append(body, extra...)
	return x.in("8", body...)
}

func findEv(acts []session.Action, event string) (session.Evidence, bool) {
	for _, a := range acts {
		if e, ok := a.(session.Evidence); ok && e.Event == event {
			return e, true
		}
	}
	return session.Evidence{}, false
}

// ------------------------------------------------------------ golden

func goldenCheck(t *testing.T, name string, msg []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name)
	got := codec.ToPipe(msg) + "\n"
	if *updateGolden {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing (run go test ./internal/order -update-golden once): %v", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden %s differs:\n got %s\nwant %s", name, got, want)
	}
}

func TestGoldenOrderMessages(t *testing.T) {
	for _, p := range []*profile.Profile{profile.FIX42, profile.FIX44} {
		for _, handl := range []bool{true, false} {
			x := newH(t, p, profile.OrderOptions{IncludeHandlInst: handl})
			x.m.opt.Account = ""
			if handl {
				x.m.opt.Account = "ACCT-1"
			}
			o, d, err := x.m.NewOrder(Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "lmt", Price: "227.50", TIF: "day"})
			if err != nil {
				t.Fatal(err)
			}
			hdr := func(seq int) codec.Header {
				return codec.Header{SenderCompID: "AGENT", TargetCompID: "ORDERECHO", MsgSeqNum: seq, SendingTime: codec.FormatTime(t0)}
			}
			tag := strings.ReplaceAll(p.Name, ".", "")
			suffix := "_handlinst"
			if !handl {
				suffix = "_nohandlinst"
			}
			goldenCheck(t, tag+"_D"+suffix+".txt", codec.Encode(p.BeginString, "D", hdr(2), d))
			x.sent("D", d, false)
			x.er(o.Root, "", "O-1", "0", "0", "100", "0", "0.00", "0", "100", "0.0000")
			_, g, err := x.m.Replace("last", "800", "10.50")
			if err != nil {
				t.Fatal(err)
			}
			goldenCheck(t, tag+"_G"+suffix+".txt", codec.Encode(p.BeginString, "G", hdr(3), g))
			_, f, err := x.m.Cancel("last")
			if err != nil {
				t.Fatal(err)
			}
			goldenCheck(t, tag+"_F"+suffix+".txt", codec.Encode(p.BeginString, "F", hdr(4), f))
			// Field layout per spec 4.
			hasTag := func(fs []codec.Field, tg int) bool {
				for _, fld := range fs {
					if fld.Tag == tg {
						return true
					}
				}
				return false
			}
			wantHandl := p.Name == "FIX.4.2" || handl
			if hasTag(d, 21) != wantHandl || hasTag(g, 21) != wantHandl {
				t.Fatalf("%s handl=%v: 21 presence wrong", p.Name, handl)
			}
			if hasTag(d, 1) != handl || !hasTag(d, 59) || !hasTag(f, 37) || !hasTag(g, 37) || !hasTag(g, 44) {
				t.Fatalf("%s: field layout", p.Name)
			}
		}
	}
}

func TestDFieldOrder(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	x.m.opt.Account = "A1"
	_, d, _ := x.m.NewOrder(Spec{Symbol: "MSFT", Qty: "10", Side: "sell", OrdType: "lmt", Price: "1.5", TIF: "ioc"})
	var tags []string
	for _, f := range d {
		tags = append(tags, strconv.Itoa(f.Tag))
	}
	if strings.Join(tags, ",") != "11,21,55,54,60,38,40,44,59,1" {
		t.Fatal(tags)
	}
	_, m, _ := x.m.NewOrder(Spec{Symbol: "MSFT", Qty: "10", Side: "short", OrdType: "mkt"})
	tags = nil
	for _, f := range m {
		tags = append(tags, strconv.Itoa(f.Tag)+"="+f.Value)
	}
	if !strings.Contains(strings.Join(tags, ","), "54=5") || strings.Contains(strings.Join(tags, ","), "44=") {
		t.Fatal(tags)
	}
}

func TestSpecValidation(t *testing.T) {
	bad := []Spec{
		{Symbol: "", Qty: "1", Side: "buy", OrdType: "mkt"},
		{Symbol: "A", Qty: "0", Side: "buy", OrdType: "mkt"},
		{Symbol: "A", Qty: "1.5", Side: "buy", OrdType: "mkt"},
		{Symbol: "A", Qty: "1", Side: "hold", OrdType: "mkt"},
		{Symbol: "A", Qty: "1", Side: "buy", OrdType: "stop"},
		{Symbol: "A", Qty: "1", Side: "buy", OrdType: "lmt"},
		{Symbol: "A", Qty: "1", Side: "buy", OrdType: "lmt", Price: "-1"},
		{Symbol: "A", Qty: "1", Side: "buy", OrdType: "mkt", Price: "10"},
		{Symbol: "A", Qty: "1", Side: "buy", OrdType: "mkt", TIF: "forever"},
	}
	for _, s := range bad {
		if _, _, _, err := s.Validate(); err == nil {
			t.Fatalf("%+v accepted", s)
		}
	}
}

func TestClOrdIDs(t *testing.T) {
	g := &IDs{Prefix: "XY", RunID: "20260929-093000"}
	if a, b := g.Next(), g.Next(); a != "XY-20260929-093000-1" || b != "XY-20260929-093000-2" {
		t.Fatal(a, b)
	}
	if (&IDs{RunID: "r"}).Next() != "OE-r-1" {
		t.Fatal("default prefix")
	}
}

// ------------------------------------------------------- transitions

func TestFullFillTransitionsAndExpectations(t *testing.T) {
	x := newH(t, profile.FIX44, profile.OrderOptions{IncludeHandlInst: true})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	if o.State != Sent {
		t.Fatal(o.State)
	}
	x.sent("D", d, false)
	if o.Requests[0].Seq != 2 {
		t.Fatal("seq not bound")
	}
	acts := x.er(o.Root, "", "O-1", "0", "0", "100", "0", "0.00", "0", "100", "0.0000")
	if o.State != New || o.OrderID != "O-1" {
		t.Fatal(o.State)
	}
	e, ok := findEv(acts, "order report")
	if !ok || e.Order == nil || !strings.Contains(e.Detail, "checks: PASS") {
		t.Fatalf("%+v", acts)
	}
	x.er(o.Root, "", "O-1", "F", "1", "100", "33", "10.00", "33", "67", "10.0000")
	x.er(o.Root, "", "O-1", "F", "2", "100", "67", "11.00", "100", "0", "10.6700")
	if o.State != Filled || !o.Terminal() || o.Verdict != checks.PASS {
		t.Fatalf("%s %s %+v", o.State, o.Verdict, o.Checks)
	}
	// (33*10 + 67*11) / 100 = 10.67 exactly.
	if got := checks.FormatRat(o.AvgExpected(), 4); got != "10.6700" {
		t.Fatal(got)
	}
	if o.CumExpected.RatString() != "100" || o.LeavesExpected().Sign() != 0 || o.Fills != 2 {
		t.Fatal("cum/leaves")
	}
	snap := o.Snapshot()
	if snap["state"] != Filled || snap["avg_px_expected"] != "10.6700" || snap["checks"].(map[string]string)["avg_px"] != "PASS" {
		t.Fatalf("%+v", snap)
	}
	hist := strings.Join(o.History, ",")
	if hist != "SENT,NEW,PARTIALLY_FILLED,FILLED" {
		t.Fatal(hist)
	}
}

func TestLiveCheckFailLoggedImmediately(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	x.sent("D", d, false)
	x.er(o.Root, "", "O-1", "0", "0", "100", "0", "0.00", "0", "100", "0.0000")
	acts := x.er(o.Root, "", "O-1", "1", "1", "100", "50", "10.00", "50", "50", "9.0000") // wrong AvgPx
	e, ok := findEv(acts, "check avg_px")
	if !ok || e.Level != session.Error || !strings.Contains(e.Detail, "FAIL avg_px") {
		t.Fatalf("%+v", acts)
	}
	if o.Verdict != checks.FAIL {
		t.Fatal(o.Verdict)
	}
	// Status did not change on the next report: not logged again.
	acts = x.er(o.Root, "", "O-1", "1", "1", "100", "10", "10.00", "60", "40", "9.0000")
	if _, again := findEv(acts, "check avg_px"); again {
		t.Fatal("unchanged status logged twice")
	}
}

func TestReplaceCancelChainAndCurrentClOrdID(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "ZWZZT", Qty: "500", Side: "buy", OrdType: "lmt", Price: "10.00"})
	x.sent("D", d, false)
	x.er(o.Root, "", "O-7", "0", "0", "500", "0", "0.00", "0", "500", "0.0000")
	_, g, err := x.m.Replace("last", "800", "10.50")
	if err != nil {
		t.Fatal(err)
	}
	gid := g[1].Value
	x.sent("G", g, false)
	// A cancel sent before the replace is acked still chains from the G.
	_, f, _ := x.m.Cancel("last")
	if f[0].Value != gid {
		t.Fatalf("F 41=%s want %s", f[0].Value, gid)
	}
	fid := f[1].Value
	x.sent("F", f, false)
	x.er(gid, o.Root, "O-7", "5", "0", "800", "0", "0.00", "0", "800", "0.0000", codec.F(44, "10.50"))
	x.er(fid, gid, "O-7", "4", "4", "800", "0", "0.00", "0", "0", "0.0000")
	if o.State != Canceled || o.OrderQty.RatString() != "800" || o.Price != "10.50" || o.Verdict != checks.PASS {
		t.Fatalf("%s %s %s %s", o.State, o.OrderQty.RatString(), o.Price, o.Verdict)
	}
	if !strings.Contains(strings.Join(o.History, ","), "REPLACED "+o.Root+"->"+gid) {
		t.Fatal(o.History)
	}
	chain := x.m.Chain(o)
	types := ""
	for _, s := range chain.Steps {
		types += s.Message.MsgType()
	}
	if types != "D8GF88" {
		t.Fatal(types)
	}
	if _, _, err := x.m.Cancel("last"); err == nil {
		t.Fatal("cancel of a canceled order accepted")
	}
}

func TestRejectedReplaceKeepsOrderWorking(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "ZWZZT", Qty: "500", Side: "buy", OrdType: "lmt", Price: "10.00"})
	x.sent("D", d, false)
	x.er(o.Root, "", "O-1", "0", "0", "500", "0", "0.00", "0", "500", "0.0000")
	_, g, _ := x.m.Replace("last", "900", "99.00")
	gid := g[1].Value
	x.sent("G", g, false)
	x.in("9", codec.F(37, "O-1"), codec.F(11, gid), codec.F(41, o.Root), codec.F(39, "0"), codec.F(434, "2"), codec.F(102, "2"), codec.F(58, "outside band"))
	if o.State != New || o.Current != o.Root {
		t.Fatalf("state %s current %s", o.State, o.Current)
	}
	if r := o.Requests[1]; !r.Rejected || !r.Answered() {
		t.Fatal("cancel reject not attached")
	}
}

func TestRuleRejectAndUnknownOrder(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "KO", Qty: "100", Side: "buy", OrdType: "lmt", Price: "60.00"})
	x.sent("D", d, false)
	x.er(o.Root, "", "O-2", "8", "8", "100", "0", "0.00", "0", "0", "0.0000", codec.F(103, "0"), codec.F(58, "Rejected by OrderEcho rule"))
	if o.State != Rejected || !o.Requests[0].Rejected {
		t.Fatal(o.State)
	}
	acts := x.er("NOT-OURS", "", "O-9", "0", "0", "1", "0", "0", "0", "1", "0")
	if e, ok := findEv(acts, "unsolicited or unknown order"); !ok || e.Level != session.Warning {
		t.Fatalf("%+v", acts)
	}
	if len(x.m.History()) != 3 { // D, the reject, the unknown report
		t.Fatalf("history %d", len(x.m.History()))
	}
}

func TestDuplicateClOrdIDViaSendRawAttached(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "ZWZZT", Qty: "500", Side: "buy", OrdType: "lmt", Price: "10.00"})
	x.sent("D", d, false)
	x.er(o.Root, "", "O-1", "0", "0", "500", "0", "0.00", "0", "500", "0.0000")
	dup := x.sent("D", d, true) // SendRaw of the same D
	acts := x.er(o.Root, "", "O-2", "8", "8", "500", "0", "0.00", "0", "0", "0.0000", codec.F(103, "6"), codec.F(58, "Duplicate ClOrdID"))
	if _, ok := findEv(acts, "duplicate request rejected"); !ok {
		t.Fatalf("%+v", acts)
	}
	if o.State != New || o.OrderID != "O-1" {
		t.Fatalf("original order disturbed: %s %s", o.State, o.OrderID)
	}
	var dupReq *Request
	for _, r := range o.Requests {
		if r.Seq == dup.Seq {
			dupReq = r
		}
	}
	if dupReq == nil || !dupReq.Rejected || !dupReq.Injected || !strings.Contains(dupReq.Answers[0], "103=6") {
		t.Fatalf("%+v", dupReq)
	}
}

func TestSessionRejectAttachedBySeq(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	s := x.sent("D", d, false)
	x.in("3", codec.F(45, strconv.Itoa(s.Seq)), codec.F(371, "60"), codec.F(373, "1"), codec.F(58, "Required tag missing: 60"))
	if o.State != Rejected || !o.Requests[0].Rejected {
		t.Fatal(o.State)
	}
	if o.Verdict != checks.PASS { // the D is answered by the Reject
		t.Fatalf("%+v", o.Checks)
	}
}

func TestCancelUnknownViaSendRaw(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	s := x.sent("F", []codec.Field{codec.F(41, "NOSUCH"), codec.F(11, "RAW-F-1"), codec.F(55, "AAPL"), codec.F(54, "1"), codec.F(60, "x"), codec.F(38, "1")}, true)
	acts := x.in("9", codec.F(37, "NONE"), codec.F(11, "RAW-F-1"), codec.F(41, "NOSUCH"), codec.F(39, "8"), codec.F(434, "1"), codec.F(102, "1"), codec.F(58, "Unknown order NOSUCH"))
	if _, ok := findEv(acts, "order request rejected"); !ok {
		t.Fatalf("%+v", acts)
	}
	req := x.m.bySeq[s.Seq]
	if req == nil || !req.Rejected || !strings.Contains(req.Answers[0], "102=1") {
		t.Fatalf("%+v", req)
	}
	c := checks.BuildChain(x.m.History(), "RAW-F-1", "")
	if c.Verdict() != checks.PASS || len(c.Steps) != 2 {
		t.Fatalf("%s %d", c.Verdict(), len(c.Steps))
	}
}

func TestReplayedReportIgnoredForState(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	x.sent("D", d, false)
	x.er(o.Root, "", "O-1", "2", "2", "100", "100", "10.00", "100", "0", "10.0000")
	// Same ExecID again, PossDup.
	x.theirSq++
	orig := x.m.History()[len(x.m.History())-1]
	var body []codec.Field
	for _, f := range orig.Fields {
		switch f.Tag {
		case 8, 9, 10, 35, 49, 56, 34, 52:
			continue
		}
		body = append(body, codec.F(f.Tag, f.Value))
	}
	x.m.OnAppMessage(x.encode("8", "ORDERECHO", "AGENT", 3, body, true))
	if o.Fills != 1 || o.CumExpected.RatString() != "100" || o.Verdict != checks.PASS {
		t.Fatalf("fills %d verdict %s", o.Fills, o.Verdict)
	}
}

func TestForgetUndoesUnsentOrder(t *testing.T) {
	x := newH(t, profile.FIX42, profile.OrderOptions{})
	o, d, _ := x.m.NewOrder(Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	x.m.Forget(o, d[0].Value)
	if len(x.m.Orders()) != 0 || x.m.Last() != nil {
		t.Fatal("not forgotten")
	}
}
