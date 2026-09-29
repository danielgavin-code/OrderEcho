//go:build interop

package interop

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

// ------------------------------------------------------------ helpers

func sendOrder(t *testing.T, a *agent, spec order.Spec) *order.Order {
	t.Helper()
	o, err := a.ag.NewOrder(spec)
	if err != nil {
		t.Fatalf("order %+v: %v", spec, err)
	}
	t.Logf("sent D %s %s %s %s %s %s", o.Root, spec.Side, spec.Qty, spec.Symbol, spec.OrdType, spec.Price)
	return o
}

func waitOrder(t *testing.T, a *agent, o *order.Order, what string, timeout time.Duration, cond func(o *order.Order) bool) {
	t.Helper()
	if !a.ag.WaitFor(timeout, func(*order.Manager) bool { return cond(o) }) {
		v := a.ag.View(o)
		t.Fatalf("timed out after %s waiting for %s: %s is %s (%d reports)", timeout, what, o.Root, v.State, v.Reports)
	}
}

func waitState(t *testing.T, a *agent, o *order.Order, state string) {
	t.Helper()
	waitOrder(t, a, o, "state "+state, 10*time.Second, func(o *order.Order) bool { return o.State == state })
}

// verdict is the scenario's Go verdict: the live one, which must equal the
// offline Go checks on the agent's own FIX log.
func verdict(t *testing.T, scenario string, a *agent, o *order.Order, want string) string {
	t.Helper()
	v := a.ag.View(o)
	var p checks.Parser
	offline := checks.BuildChain(checks.MergeChronologically(p.ParseFile(a.fixLogPath())), o.Root, "")
	if offline.Verdict() != v.Verdict {
		t.Fatalf("%s: live verdict %s but offline verdict %s", o.Root, v.Verdict, offline.Verdict())
	}
	var bad []string
	for _, c := range offline.Checks {
		if c.Status != checks.PASS {
			bad = append(bad, fmt.Sprintf("%s=%s (%s)", c.Name, c.Status, c.Explanation))
		}
	}
	recordVerdict("%-44s %-30s %-17s %s %s", scenario, o.Root, v.State, v.Verdict, strings.Join(bad, "; "))
	t.Logf("VERDICT %s %s: %s %s", scenario, o.Root, v.State, v.Verdict)
	if want != "" && v.Verdict != want {
		t.Fatalf("%s: verdict %s, want %s: %v", o.Root, v.Verdict, want, bad)
	}
	return v.Verdict
}

// finishAgent logs out cleanly and waits, so the logs are complete.
func finishAgent(t *testing.T, a *agent) {
	t.Helper()
	if err := a.ini.Logout("interop: scenario done"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if r := a.wait(15 * time.Second); !r.Outcome.CleanLogout {
		t.Fatalf("logout not clean: %+v", r.Outcome)
	}
}

func roots(a *agent, extra ...string) []string {
	var out []string
	a.ag.With(func(m *order.Manager) {
		for _, o := range m.Orders() {
			out = append(out, o.Root)
		}
	})
	return append(out, extra...)
}

func lastReport(t *testing.T, a *agent, o *order.Order) *checks.Message {
	t.Helper()
	c := a.ag.Chain(o)
	reps := c.Reports()
	if len(reps) == 0 {
		t.Fatalf("%s: no reports", o.Root)
	}
	return reps[len(reps)-1]
}

func startTrading(t *testing.T, version string) (*emulator, *agent, string) {
	e := startEmulator(t)
	id, emu := "emu42", "agent42"
	if version == "FIX.4.4" {
		id, emu = "emu44", "agent44"
	}
	a := startAgent(t, agentSpec{id: id, version: version, target: "ORDERECHO", port: e.fixPort, reset: true})
	a.waitActive(10*time.Second, 1)
	return e, a, emu
}

// ------------------------------------------------------------ scenarios

// A2 1: full fill (A-D) on 4.2 and 4.4 -> PASS; 4.4 fills are 150=F.
func TestA2Scenario01FullFill(t *testing.T) {
	for _, version := range []string{"FIX.4.2", "FIX.4.4"} {
		e, a, emu := startTrading(t, version)
		o := sendOrder(t, a, order.Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
		waitState(t, a, o, order.Filled)
		fill := lastReport(t, a, o)
		want := "2"
		if version == "FIX.4.4" {
			want = "F"
		}
		if fill.Value(150) != want || fill.Value(31) != "227.50" || fill.BeginString() != version {
			t.Fatalf("%s fill: %s", version, fill.Pipe())
		}
		lim := sendOrder(t, a, order.Spec{Symbol: "CSCO", Qty: "250", Side: "sell", OrdType: "lmt", Price: "99.50", TIF: "day"})
		waitState(t, a, lim, order.Filled)
		verdict(t, "1 full fill "+version+" (AAPL mkt)", a, o, checks.PASS)
		verdict(t, "1 full fill "+version+" (CSCO lmt day)", a, lim, checks.PASS)
		finishAgent(t, a)
		assertParity(t, casesFor("A2-1 "+version, a, e, emu, roots(a)))
	}
}

// A2 2: partials (E-G) -> PASS, order left working.
func TestA2Scenario02Partials(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.2")
	o := sendOrder(t, a, order.Spec{Symbol: "EFG", Qty: "1000", Side: "buy", OrdType: "lmt", Price: "10.00"})
	waitOrder(t, a, o, "two partial fills", 10*time.Second, func(o *order.Order) bool { return o.Fills == 2 })
	time.Sleep(1200 * time.Millisecond) // nothing more is scheduled ("then: leave")
	v := a.ag.View(o)
	if v.State != order.PartiallyFilled || v.Terminal {
		t.Fatalf("state %s", v.State)
	}
	a.ag.With(func(*order.Manager) {
		if o.CumExpected.RatString() != "500" || o.LeavesExpected().RatString() != "500" || o.CumReported != "500" {
			t.Errorf("cum %s leaves %s reported %s", o.CumExpected.RatString(), o.LeavesExpected().RatString(), o.CumReported)
		}
	})
	verdict(t, "2 partials EFG (left working)", a, o, checks.PASS)
	finishAgent(t, a)
	assertParity(t, casesFor("A2-2", a, e, emu, roots(a)))
}

// A2 3: odd lots with fill_rest (N-P: 1,2,3,405,remainder) -> PASS, AvgPx exact.
func TestA2Scenario03OddLots(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.4")
	o := sendOrder(t, a, order.Spec{Symbol: "NOK", Qty: "1000", Side: "buy", OrdType: "mkt"})
	waitState(t, a, o, order.Filled)
	c := a.ag.Chain(o)
	var lots []string
	for _, r := range c.Reports() {
		if checks.FillExecTypes[r.Value(150)] {
			lots = append(lots, r.Value(32))
		}
	}
	if strings.Join(lots, ",") != "1,2,3,405,589" {
		t.Fatalf("lots %v", lots)
	}
	a.ag.With(func(*order.Manager) {
		reported, ok := checks.ParseDec(o.AvgReported)
		if !ok || o.AvgExpected().Cmp(reported.R) != 0 {
			t.Errorf("AvgPx reported %s, expected exactly %s", o.AvgReported, o.AvgExpected().FloatString(6))
		}
	})
	verdict(t, "3 odd lots NOK 1/2/3/405/589", a, o, checks.PASS)
	finishAgent(t, a)
	assertParity(t, casesFor("A2-3", a, e, emu, roots(a)))
}

// A2 4: hold (ZWZZT) -> replace last 800 10.50 -> cancel last -> PASS; chain has D, G, F.
func TestA2Scenario04HoldReplaceCancel(t *testing.T) {
	for _, version := range []string{"FIX.4.2", "FIX.4.4"} {
		e, a, emu := startTrading(t, version)
		o := sendOrder(t, a, order.Spec{Symbol: "ZWZZT", Qty: "500", Side: "buy", OrdType: "lmt", Price: "10.00"})
		waitState(t, a, o, order.New)
		_, gid, err := a.ag.Replace("last", "800", "10.50")
		if err != nil {
			t.Fatal(err)
		}
		waitOrder(t, a, o, "replace ack", 10*time.Second, func(o *order.Order) bool { return o.Accepted == gid })
		_, fid, err := a.ag.Cancel("last")
		if err != nil {
			t.Fatal(err)
		}
		waitState(t, a, o, order.Canceled)
		c := a.ag.Chain(o)
		types := ""
		for _, s := range c.Steps {
			types += s.Message.MsgType()
		}
		if types != "D8G8F8" {
			t.Fatalf("chain %s", types)
		}
		a.ag.With(func(*order.Manager) {
			if o.OrderQty.RatString() != "800" || o.Price != "10.50" || o.Current != fid {
				t.Errorf("qty %s px %s current %s", o.OrderQty.RatString(), o.Price, o.Current)
			}
		})
		verdict(t, "4 hold/replace/cancel ZWZZT "+version, a, o, checks.PASS)
		finishAgent(t, a)
		assertParity(t, casesFor("A2-4 "+version, a, e, emu, roots(a)))
	}
}

// A2 5: rule reject (K-M) and band reject -> PASS with REJECTED; the strict
// session rejects everything.
func TestA2Scenario05Rejects(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.2")
	rule := sendOrder(t, a, order.Spec{Symbol: "KO", Qty: "100", Side: "buy", OrdType: "lmt", Price: "60.00"})
	band := sendOrder(t, a, order.Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "lmt", Price: "500.00"})
	waitState(t, a, rule, order.Rejected)
	waitState(t, a, band, order.Rejected)
	if r := lastReport(t, a, rule); r.Value(103) != "0" || r.Value(58) != "Rejected by OrderEcho rule" {
		t.Fatalf("rule reject %s", r.Pipe())
	}
	if r := lastReport(t, a, band); r.Value(103) != "3" || !strings.Contains(r.Value(58), "band") {
		t.Fatalf("band reject %s", r.Pipe())
	}
	verdict(t, "5 rule reject KO", a, rule, checks.PASS)
	verdict(t, "5 band reject AAPL lmt 500", a, band, checks.PASS)
	finishAgent(t, a)
	assertParity(t, casesFor("A2-5", a, e, emu, roots(a)))

	s := startAgent(t, agentSpec{id: "strict", version: "FIX.4.2", target: "STRICTBRK", port: e.strictPort, reset: true})
	s.waitActive(10*time.Second, 1)
	st := sendOrder(t, s, order.Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	waitState(t, s, st, order.Rejected)
	if r := lastReport(t, s, st); r.Value(58) != "Strict broker rejects everything" {
		t.Fatalf("strict %s", r.Pipe())
	}
	verdict(t, "5 strict broker rejects all", s, st, checks.PASS)
	finishAgent(t, s)
	assertParity(t, casesFor("A2-5 strict", s, e, "strict-broker", roots(s)))
}

// A2 6: unsolicited cancel (H-J rule) -> PASS.
func TestA2Scenario06UnsolicitedCancel(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.4")
	o := sendOrder(t, a, order.Spec{Symbol: "HON", Qty: "100", Side: "buy", OrdType: "lmt", Price: "10.00"})
	waitState(t, a, o, order.Canceled)
	if r := lastReport(t, a, o); r.Value(150) != "4" || !strings.Contains(r.Value(58), "h-to-j-cancel") {
		t.Fatalf("%s", r.Pipe())
	}
	verdict(t, "6 unsolicited cancel HON", a, o, checks.PASS)
	finishAgent(t, a)
	assertParity(t, casesFor("A2-6", a, e, emu, roots(a)))
}

// A2 7: manual fills via the control API at two prices -> AvgPx exact -> PASS.
func TestA2Scenario07ManualFills(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.2")
	o := sendOrder(t, a, order.Spec{Symbol: "ZWZZT", Qty: "1000", Side: "buy", OrdType: "lmt", Price: "10.00"})
	waitState(t, a, o, order.New)
	oid := a.ag.View(o).OrderID
	e.post("/orders/"+oid+"/fill", map[string]any{"qty": 300, "price": "10.00"})
	waitOrder(t, a, o, "first manual fill", 5*time.Second, func(o *order.Order) bool { return o.Fills == 1 })
	e.post("/orders/"+oid+"/fill", map[string]any{"qty": 200, "price": "11.00"})
	waitOrder(t, a, o, "second manual fill", 5*time.Second, func(o *order.Order) bool { return o.Fills == 2 })
	a.ag.With(func(*order.Manager) {
		want := big.NewRat(104, 10) // (300*10 + 200*11) / 500
		reported, _ := checks.ParseDec(o.AvgReported)
		if o.AvgExpected().Cmp(want) != 0 || reported.R.Cmp(want) != 0 || o.AvgReported != "10.4000" {
			t.Errorf("avg expected %s reported %s", o.AvgExpected().FloatString(6), o.AvgReported)
		}
	})
	verdict(t, "7 manual fills 300@10 + 200@11", a, o, checks.PASS)
	finishAgent(t, a)
	assertParity(t, casesFor("A2-7", a, e, emu, roots(a)))
}

// A2 8: the emulator adds 9999=FOO to an ER; the agent tolerates it.
func TestA2Scenario08UnknownTagTolerated(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.4")
	e.post("/sessions/agent44/inject/next", map[string]any{"msg_type": "8", "set": map[string]string{"9999": "FOO"}})
	o := sendOrder(t, a, order.Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	waitState(t, a, o, order.Filled)
	c := a.ag.Chain(o)
	tagged := 0
	for _, r := range c.Reports() {
		if r.Value(9999) == "FOO" {
			tagged++
		}
	}
	if tagged != 1 {
		t.Fatalf("%d reports carry 9999=FOO", tagged)
	}
	data, _ := os.ReadFile(e.fixLog("agent44"))
	_ = data
	if !strings.Contains(e.fixLog("agent44"), "# injected: set 9999=FOO") {
		t.Fatal("emulator did not record the injection")
	}
	verdict(t, "8 ER with 9999=FOO", a, o, checks.PASS)
	finishAgent(t, a)
	assertParity(t, casesFor("A2-8", a, e, emu, roots(a)))
}

// A2 9: a duplicate ClOrdID via SendRaw -> 103=6 reject received and attached.
func TestA2Scenario09DuplicateClOrdID(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.2")
	o := sendOrder(t, a, order.Spec{Symbol: "ZWZZT", Qty: "500", Side: "buy", OrdType: "lmt", Price: "10.00"})
	waitState(t, a, o, order.New)
	var body []codec.Field
	a.ag.With(func(m *order.Manager) {
		for _, f := range m.History()[0].Fields {
			switch f.Tag {
			case 8, 9, 10, 34, 49, 52, 56:
				continue
			}
			body = append(body, codec.F(f.Tag, f.Value))
		}
	})
	if err := a.ini.SendRaw(body); err != nil {
		t.Fatal(err)
	}
	var dup order.Request
	waitOrder(t, a, o, "duplicate answered", 5*time.Second, func(o *order.Order) bool {
		for _, r := range o.Requests {
			if r.Injected && r.Answered() {
				dup = *r
				return true
			}
		}
		return false
	})
	if !dup.Rejected || !strings.Contains(dup.Answers[0], "103=6") || !strings.Contains(dup.Answers[0], "Duplicate ClOrdID") {
		t.Fatalf("dup request %+v", dup)
	}
	if v := a.ag.View(o); v.State != order.New {
		t.Fatalf("original order disturbed: %s", v.State)
	}
	// Two OrderIDs now share the ClOrdID, so order_id_constant fails in Go and
	// in Python alike: the checks' honest verdict on a duplicate ClOrdID.
	verdict(t, "9 duplicate ClOrdID via SendRaw", a, o, "")
	finishAgent(t, a)
	assertParity(t, casesFor("A2-9", a, e, emu, roots(a)))
}

// A2 10: a TestRequest behind a gap is answered after the gap fill.
func TestA2Scenario10TestRequestBehindGap(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.2")
	e.post("/sessions/agent42/inject/seq-gap", map[string]int{"skip": 3})
	resp := e.post("/sessions/agent42/test-request", nil)
	id, _ := resp["test_req_id"].(string)
	waitFor(t, 5*time.Second, "emulator TestRequest answered behind the gap", func() bool {
		return e.status("agent42").PendingTestReqID == ""
	})
	data, _ := os.ReadFile(a.fixLogPath())
	log := string(data)
	iRR := strings.Index(log, " 35=2  ")
	iGF := strings.Index(log, " 35=4  ")
	iHB := strings.Index(log, "|112="+id+"|10=")
	hbLine := ""
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, " OUT ") && strings.Contains(l, " 35=0 ") && strings.Contains(l, "|112="+id+"|") {
			hbLine = l
		}
	}
	if iRR < 0 || iGF < 0 || hbLine == "" || !(iRR < iGF && iGF < strings.Index(log, hbLine)) || iHB < 0 {
		t.Fatalf("expected ResendRequest, gap fill, then Heartbeat 112=%s:\n%s", id, log)
	}
	evData, _ := os.ReadFile(a.ev.Path)
	if !strings.Contains(string(evData), "message queued") || !strings.Contains(string(evData), "message dequeued") {
		t.Fatal("queue/dequeue not in evidence")
	}
	inSync(t, a, e, "agent42")
	o := sendOrder(t, a, order.Spec{Symbol: "AAPL", Qty: "100", Side: "buy", OrdType: "mkt"})
	waitState(t, a, o, order.Filled)
	verdict(t, "10 TestRequest behind gap, then AAPL", a, o, checks.PASS)
	finishAgent(t, a)
	assertParity(t, casesFor("A2-10", a, e, emu, roots(a)))
}

// A2 11: cancel of an unknown ClOrdID via SendRaw -> 35=9 102=1 attached.
func TestA2Scenario11CancelUnknown(t *testing.T) {
	e, a, emu := startTrading(t, "FIX.4.4")
	id := "RAWF-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	err := a.ini.SendRaw([]codec.Field{codec.F(35, "F"), codec.F(41, "NOSUCH"), codec.F(11, id), codec.F(55, "AAPL"),
		codec.F(54, "1"), codec.F(60, codec.FormatTime(time.Now())), codec.F(38, "100")})
	if err != nil {
		t.Fatal(err)
	}
	var answers []string
	if !a.ag.WaitFor(5*time.Second, func(m *order.Manager) bool {
		for _, msg := range m.History() {
			if msg.MsgType() == "9" && msg.Value(11) == id {
				answers = append(answers, msg.Pipe())
				return true
			}
		}
		return false
	}) {
		t.Fatal("no cancel reject")
	}
	if !strings.Contains(answers[0], "|102=1|") || !strings.Contains(answers[0], "|37=NONE|") {
		t.Fatalf("%s", answers[0])
	}
	c := a.ag.Chain(&order.Order{Root: id})
	if c.Verdict() != checks.PASS || len(c.Steps) != 2 {
		t.Fatalf("chain %s steps %d", c.Verdict(), len(c.Steps))
	}
	recordVerdict("%-44s %-30s %-17s %s", "11 cancel unknown ClOrdID via SendRaw", id, "(no order)", c.Verdict())
	finishAgent(t, a)
	assertParity(t, casesFor("A2-11", a, e, emu, []string{id}))
}
