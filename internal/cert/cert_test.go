package cert

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

// ------------------------------------------------------------ fake driver

type fake struct {
	t       *testing.T
	clk     *clock.FakeClock
	hist    *History
	version string
	up      bool
	inSeq   int
	outSeq  int
	orders  map[string]*OrderInfo
	n       int
	// react is called after every request we send; it may add inbound
	// messages (after advancing the clock) to simulate the counterparty.
	react       func(f *fake, kind string, m *codec.Message)
	calls       []string
	dropped     string
	failConnect bool
}

func newFake(t *testing.T, version string) *fake {
	clk := clock.NewFake(time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC))
	return &fake{t: t, clk: clk, hist: NewHistory(clk), version: version, up: true, inSeq: 1, outSeq: 1, orders: map[string]*OrderInfo{}}
}

func (f *fake) msg(dir, msgType string, body ...codec.Field) *codec.Message {
	seq := &f.outSeq
	sender, target := "AGENT", "VENUE"
	if dir == "in" {
		seq = &f.inSeq
		sender, target = target, sender
	}
	*seq++
	raw := codec.Encode(f.version, msgType, codec.Header{SenderCompID: sender, TargetCompID: target, MsgSeqNum: *seq,
		SendingTime: codec.FormatTime(f.clk.Now())}, body)
	m, _ := codec.DecodeOne(raw)
	return m
}

func (f *fake) in(msgType string, body ...codec.Field) *codec.Message {
	m := f.msg("in", msgType, body...)
	f.hist.Add("in", m)
	return m
}

func (f *fake) out(msgType string, body ...codec.Field) *codec.Message {
	m := f.msg("out", msgType, body...)
	f.hist.Add("out", m)
	return m
}

func (f *fake) Info() SessionInfo {
	return SessionInfo{SessionID: "s1", FixVersion: f.version, SenderCompID: "AGENT", TargetCompID: "VENUE", HeartbeatSec: 30}
}
func (f *fake) Connected() bool { return f.up }
func (f *fake) Connect(reset bool) error {
	f.calls = append(f.calls, fmt.Sprintf("connect reset=%v", reset))
	if f.failConnect {
		return fmt.Errorf("connection refused")
	}
	f.up = true
	b := []codec.Field{codec.F(98, "0"), codec.F(108, "30")}
	if reset {
		f.inSeq, f.outSeq = 0, 0
		b = append(b, codec.F(141, "Y"))
	}
	f.out("A", b...)
	f.in("A", b...)
	return nil
}
func (f *fake) Logout() error {
	f.calls = append(f.calls, "logout")
	f.out("5")
	f.in("5")
	f.up = false
	return nil
}
func (f *fake) Abrupt() error   { f.calls = append(f.calls, "abrupt"); f.up = false; return nil }
func (f *fake) Dropped() string { d := f.dropped; f.dropped = ""; return d }
func (f *fake) TestRequest() (string, error) {
	f.n++
	id := fmt.Sprintf("TEST-%d", f.n)
	m := f.out("1", codec.F(112, id))
	if f.react != nil {
		f.react(f, "testreq", m)
	}
	return id, nil
}
func (f *fake) SkipOutboundSeq(n int) error { f.outSeq += n; return nil }
func (f *fake) ResendRequest(b, e int) error {
	m := f.out("2", codec.F(7, strconv.Itoa(b)), codec.F(16, strconv.Itoa(e)))
	if f.react != nil {
		f.react(f, "resend", m)
	}
	return nil
}
func (f *fake) ResendStored(seq int) error { return nil }
func (f *fake) NewOrder(spec order.Spec) (string, error) {
	f.n++
	root := fmt.Sprintf("C%d", f.n)
	f.orders[root] = &OrderInfo{Root: root, Current: root, ClOrdIDs: []string{root}, State: "SENT"}
	m := f.out("D", codec.F(11, root), codec.F(55, spec.Symbol), codec.F(54, "1"), codec.F(38, spec.Qty), codec.F(40, "1"))
	f.orders[root].Seqs = append(f.orders[root].Seqs, f.outSeq)
	if f.react != nil {
		f.react(f, "D", m)
	}
	return root, nil
}
func (f *fake) Cancel(root string, allow bool) (string, error) {
	f.n++
	id := fmt.Sprintf("C%d", f.n)
	o := f.orders[root]
	o.ClOrdIDs = append(o.ClOrdIDs, id)
	m := f.out("F", codec.F(41, o.Current), codec.F(11, id))
	o.Current = id
	o.Seqs = append(o.Seqs, f.outSeq)
	if f.react != nil {
		f.react(f, "F", m)
	}
	return id, nil
}
func (f *fake) Replace(root, qty, px string) (string, error) { return "", fmt.Errorf("not in fake") }
func (f *fake) SendRaw(fields []codec.Field) (int, error) {
	mt := ""
	var body []codec.Field
	for _, x := range fields {
		if x.Tag == 35 {
			mt = x.Value
		} else {
			body = append(body, x)
		}
	}
	m := f.out(mt, body...)
	if f.react != nil {
		f.react(f, "raw", m)
	}
	return f.outSeq, nil
}
func (f *fake) Order(root string) (OrderInfo, bool) {
	o, ok := f.orders[root]
	if !ok {
		return OrderInfo{}, false
	}
	return *o, true
}
func (f *fake) Chain(id string) *checks.Chain {
	var ms []*checks.Message
	for i, e := range f.hist.Since(0) {
		ms = append(ms, checks.FromCodec(e.Msg, e.Dir, "s1", e.TS, i, false))
	}
	return checks.BuildChain(ms, id, "")
}
func (f *fake) History() *History { return f.hist }
func (f *fake) NextIn() int       { return f.inSeq + 1 }
func (f *fake) NewID() string     { f.n++; return fmt.Sprintf("R%d", f.n) }
func (f *fake) Mark(e, d string)  { f.calls = append(f.calls, "mark "+e) }
func (f *fake) Offsets() Offsets  { return Offsets{} }

// ack answers a D with an acknowledgement after delay.
func ack(f *fake, root string, delay time.Duration, execType, status string, extra ...codec.Field) {
	f.clk.Advance(delay)
	body := []codec.Field{codec.F(37, "O-"+root), codec.F(11, root), codec.F(17, "E-"+strconv.Itoa(f.inSeq+1)),
		codec.F(150, execType), codec.F(39, status), codec.F(55, "AAPL"), codec.F(54, "1"), codec.F(38, "100"),
		codec.F(32, "0"), codec.F(31, "0"), codec.F(151, "100"), codec.F(14, "0"), codec.F(6, "0")}
	for _, e := range extra { // dict-update semantics
		replaced := false
		for i := range body {
			if body[i].Tag == e.Tag {
				body[i].Value, replaced = e.Value, true
			}
		}
		if !replaced {
			body = append(body, e)
		}
	}
	f.in("8", body...)
	f.orders[root].OrderID = "O-" + root
}

func suiteFrom(t *testing.T, yaml string) *Suite {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.yaml")
	os.WriteFile(p, []byte(yaml), 0o644)
	s, err := LoadSuite(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const head = `
suite: t
title: T
fix_version: FIX.4.2
vars: { symbol: AAPL, qty: "100" }
sections: [ { id: ORD, name: Orders } ]
cases:
`

func runWith(f *fake, s *Suite, opt Options) *RunResult {
	opt.Suite, opt.Driver, opt.Clock = s, f, f.clk
	opt.Sleep = func(d time.Duration) { f.clk.Advance(d) }
	return Run(opt)
}

// ------------------------------------------------------------ parsing

func TestSuiteValidationErrorsNameTheCase(t *testing.T) {
	cases := map[string]string{
		`  - { id: "1", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { frobnicate: {} } ] }`:                          `case "1" step 1: unknown step type "frobnicate"`,
		`  - { id: "2", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { expect: { msg: "8", exec_type: NEWISH } } ] }`: `case "2" step 1: unknown exec_type "NEWISH"`,
		`  - { id: "3", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { expect: { msg: "8", colour: red } } ] }`:       `case "3" step 1: expect: unknown key(s) colour`,
		`  - { id: "4", section: ORD, title: x, task: y, required: maybe, level: basic, mode: auto, steps: [ { session: testreq } ] }`:                       `case "4": 'required' must be true or false`,
		`  - { id: "5", section: XXX, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { session: testreq } ] }`:                        `case "5": unknown section "XXX"`,
		`  - { id: "6", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { control: { description: d } } ] }`:             `case "6": an auto case cannot have control`,
		`  - { id: "7", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { send: { msg: F, order: nope } } ] }`:           `case "7" step 1: order "nope" is not defined`,
		`  - { id: "8", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { session: { teleport: 1 } } ] }`:                `case "8" step 1: unknown session action "teleport"`,
		`  - { id: "9", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { expect: { msg: "8", within: soon } } ] }`:      `case "9" step 1: expect.within: bad duration "soon"`,
		`  - { id: "10", section: ORD, title: x, task: y, required: true, level: basic, mode: manual, steps: [ { session: testreq } ] }`:                     `case "10": a manual case has only manual steps`,
		`  - { id: "11", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { send: { msg: D, side: buy } } ] }`:            `case "11" step 1: send D needs side, ord_type, symbol and qty`,
	}
	for body, want := range cases {
		p := filepath.Join(t.TempDir(), "s.yaml")
		os.WriteFile(p, []byte(head+body+"\n"), 0o644)
		_, err := LoadSuite(p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
}

func TestShippedSuitesAndTargetsLoad(t *testing.T) {
	s42, err := LoadSuite("../../certs/order_entry_fix42.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s44, err := LoadSuite("../../certs/order_entry_fix44.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if s44.FixVersion != "FIX.4.4" || len(s44.Cases) != len(s42.Cases) || s44.Vars["limit_buy"] != "1.00" || s44.Extends == "" {
		t.Fatalf("4.4 variant: %s %d %q", s44.FixVersion, len(s44.Cases), s44.Vars["limit_buy"])
	}
	for _, p := range []string{"../../certs/targets/emulator.yaml", "../../certs/targets/generic.yaml"} {
		tg, err := LoadTarget(p)
		if err != nil {
			t.Fatal(err)
		}
		for id := range tg.NotApplicable {
			if s42.Case(id) == nil {
				t.Fatalf("%s: N/A for unknown case %s", p, id)
			}
		}
	}
}

// The drift test: the suite is the checklist, row for row.
func TestSuiteMatchesChecklistHTML(t *testing.T) {
	data, err := os.ReadFile("../../certs/source/cert_order_entry.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	secRe := regexp.MustCompile(`\{\s*id:\s*'(s\d+)',\s*num:\s*'([A-Z]+)',\s*name:\s*'([^']*)'\s*\}`)
	sections := map[string]string{}
	names := map[string]string{}
	for _, m := range secRe.FindAllStringSubmatch(src, -1) {
		sections[m[1]] = m[2]
		names[m[2]] = html.UnescapeString(m[3])
	}
	rowRe := regexp.MustCompile(`\{\s*section:\s*'(s\d+)',\s*step:\s*'([^']+)',\s*area:\s*'([^']*)',\s*task:\s*'([^']*)',\s*level:\s*'(\w+)',\s*req:\s*(true|false)\s*\}`)
	rows := rowRe.FindAllStringSubmatch(src, -1)
	if len(rows) < 60 || len(sections) != 9 {
		t.Fatalf("parsed %d rows / %d sections from the HTML", len(rows), len(sections))
	}
	for _, file := range []string{"../../certs/order_entry_fix42.yaml", "../../certs/order_entry_fix44.yaml"} {
		s, err := LoadSuite(file)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, r := range rows {
			step, task := r[2], html.UnescapeString(r[4])
			c := s.Case(step)
			if c == nil {
				t.Errorf("%s: checklist step %s missing from the suite", file, step)
				continue
			}
			seen[step] = true
			if c.Task != task {
				t.Errorf("%s: %s task drifted:\n suite: %q\n  html: %q", file, step, c.Task, task)
			}
			if c.Section != sections[r[1]] || c.Area != r[3] || c.Level != r[5] || c.Required != (r[6] == "true") {
				t.Errorf("%s: %s section/area/level/req drifted", file, step)
			}
		}
		for _, c := range s.Cases {
			if !seen[c.ID] {
				t.Errorf("%s: case %s is not in the checklist", file, c.ID)
			}
		}
		for _, sec := range s.Sections {
			if names[sec.ID] != sec.Name {
				t.Errorf("%s: section %s name %q, html %q", file, sec.ID, sec.Name, names[sec.ID])
			}
		}
		if len(s.Cases) != len(rows) {
			t.Errorf("%s: %d cases for %d rows", file, len(s.Cases), len(rows))
		}
	}
}

// ------------------------------------------------------------ vars / names

func TestSubstitute(t *testing.T) {
	vars := map[string]string{"symbol": "AAPL", "qty": "100"}
	got, err := Substitute("{{symbol}} x {{ qty }}", vars, func(n string) (string, bool) {
		if n == "uid" {
			return "U1", true
		}
		return "", false
	})
	if err != nil || got != "AAPL x 100" {
		t.Fatal(got, err)
	}
	if _, err := Substitute("{{nope}}", vars, nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatal(err)
	}
	m := MergeVars(map[string]string{"a": "suite", "b": "suite"}, map[string]string{"b": "target"}, map[string]string{"c": "cli"})
	if m["a"] != "suite" || m["b"] != "target" || m["c"] != "cli" {
		t.Fatal(m)
	}
}

func TestYAMLScalarsKeptVerbatim(t *testing.T) {
	s := suiteFrom(t, head+`  - { id: "1", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { session: testreq } ] }
`)
	_ = s
	s2 := suiteFrom(t, strings.Replace(head, `qty: "100"`, `qty: "100", px: 1.00, lot: 0100`, 1)+`  - { id: "1", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { session: testreq } ] }
`)
	if s2.Vars["px"] != "1.00" || s2.Vars["lot"] != "0100" {
		t.Fatalf("%q %q", s2.Vars["px"], s2.Vars["lot"])
	}
}

func TestSemanticNamesPerVersion(t *testing.T) {
	mk := func(version, et, st string) *codec.Message {
		raw := codec.Encode(version, "8", codec.Header{SenderCompID: "A", TargetCompID: "B", MsgSeqNum: 1, SendingTime: "x"},
			[]codec.Field{codec.F(150, et), codec.F(39, st)})
		m, _ := codec.DecodeOne(raw)
		return m
	}
	cases := []struct {
		name, version, et, st string
		want                  bool
	}{
		{"TRADE", "FIX.4.2", "1", "1", true}, {"TRADE", "FIX.4.2", "2", "2", true}, {"TRADE", "FIX.4.2", "F", "2", false},
		{"TRADE", "FIX.4.4", "F", "1", true}, {"TRADE", "FIX.4.4", "2", "2", false},
		{"FILL", "FIX.4.2", "2", "2", true}, {"FILL", "FIX.4.4", "F", "2", true}, {"FILL", "FIX.4.4", "F", "1", false},
		{"PARTIAL_FILL", "FIX.4.2", "1", "1", true}, {"PARTIAL_FILL", "FIX.4.4", "F", "1", true}, {"PARTIAL_FILL", "FIX.4.4", "1", "1", false},
		{"NEW", "FIX.4.4", "0", "0", true}, {"CANCELED", "FIX.4.2", "4", "4", true}, {"PENDING_NEW", "FIX.4.2", "A", "A", true},
	}
	for _, c := range cases {
		if got := ExecTypeMatches(c.name, c.version, mk(c.version, c.et, c.st)); got != c.want {
			t.Fatalf("%s %s 150=%s 39=%s: %v", c.name, c.version, c.et, c.st, got)
		}
	}
	if v, _ := OrdStatusValue("PARTIALLY_FILLED"); v != "1" {
		t.Fatal(v)
	}
}

// ------------------------------------------------------------ expect

const orderCase = `  - id: "4.1"
    section: ORD
    title: Market Buy
    task: t
    required: true
    level: basic
    mode: auto
    steps:
      - send: { msg: D, ref: o1, side: buy, ord_type: mkt, symbol: "{{symbol}}", qty: "{{qty}}" }
      - expect: { exec_type: NEW, within: 5s }
      - expect: { exec_type: FILL, ord_status: FILLED, tags: { 14: "{{qty}}" }, within: 5s }
`

func TestExpectMatchesInOrderAndPasses(t *testing.T) {
	f := newFake(t, "FIX.4.4")
	f.react = func(f *fake, kind string, m *codec.Message) {
		if kind == "D" {
			root := m.Value(11)
			ack(f, root, 100*time.Millisecond, "0", "0")
			ack(f, root, 100*time.Millisecond, "F", "2", codec.F(14, "100"))
		}
	}
	r := runWith(f, suiteFrom(t, head+orderCase), Options{})
	c := r.Cases[0]
	// 3 steps plus the cleanup cancel (the fake never marks orders terminal).
	if c.Status != StatusPass || len(c.Steps) != 4 || c.Steps[3].Type != "cleanup" || c.Orders[0] != "C1" {
		t.Fatalf("%+v", c)
	}
	if ExitCode(r, 0) != ExitOK {
		t.Fatal("exit")
	}
}

func TestExpectTimeoutFailsWithReason(t *testing.T) {
	f := newFake(t, "FIX.4.2")
	f.react = func(f *fake, kind string, m *codec.Message) {
		if kind == "D" {
			ack(f, m.Value(11), 50*time.Millisecond, "0", "0") // acked, never filled
		}
	}
	r := runWith(f, suiteFrom(t, head+orderCase), Options{})
	c := r.Cases[0]
	if c.Status != StatusFail || !strings.Contains(c.Reason, "step 3 (expect): timed out after 5s waiting for") ||
		!strings.Contains(c.Reason, "last relevant message: 35=8") {
		t.Fatalf("%s: %s", c.Status, c.Reason)
	}
	t2, _ := time.Parse("2006-01-02T15:04:05.000Z", c.Steps[1].TS)
	t3, _ := time.Parse("2006-01-02T15:04:05.000Z", c.Steps[2].TS)
	if el := t3.Sub(t2); el < 5*time.Second || el > 5100*time.Millisecond {
		t.Fatalf("expect waited %s, want its 5s window", el)
	}
	if ExitCode(r, 0) != ExitFail {
		t.Fatal("exit")
	}
}

func TestExpectAnyOfNoneOptionalAndOrderFilter(t *testing.T) {
	body := `  - id: "7.3"
    section: ORD
    title: reject
    task: t
    required: true
    level: basic
    mode: auto
    steps:
      - send: { msg: D, ref: other, side: buy, ord_type: mkt, symbol: X, qty: "1" }
      - send: { msg: D, ref: o1, side: buy, ord_type: mkt, symbol: "{{symbol}}", qty: "{{qty}}" }
      - expect: { any_of: [ { exec_type: REJECTED }, { msg: j } ], within: 5s }
      - expect: { exec_type: PARTIAL_FILL, none: true, within: 2s }
      - expect: { exec_type: CANCELED, within: 1s, optional: true }
`
	f := newFake(t, "FIX.4.2")
	f.react = func(f *fake, kind string, m *codec.Message) {
		if kind == "D" && m.Value(11) == "C1" {
			ack(f, "C1", 10*time.Millisecond, "8", "8") // the *other* order is rejected first
		}
		if kind == "D" && m.Value(11) == "C2" {
			f.clk.Advance(200 * time.Millisecond)
			f.in("j", codec.F(45, strconv.Itoa(f.outSeq)), codec.F(372, "D"), codec.F(380, "4"))
		}
	}
	r := runWith(f, suiteFrom(t, head+body), Options{})
	c := r.Cases[0]
	if c.Status != StatusPass {
		t.Fatalf("%s: %s %+v", c.Status, c.Reason, c.Steps)
	}
	if !strings.Contains(c.Steps[2].Detail, "35=j") {
		t.Fatalf("matched the wrong order's reject: %s", c.Steps[2].Detail)
	}
	if c.Steps[4].Status != StepSkipped {
		t.Fatalf("optional: %+v", c.Steps[4])
	}
}

func TestSessionAndAssertSteps(t *testing.T) {
	body := `  - id: "3.3"
    section: ORD
    title: testreq
    task: t
    required: true
    level: basic
    mode: auto
    steps:
      - session: testreq
      - expect: { msg: "0", tags: { 112: "{{last_testreq_id}}" }, within: 1s }
      - assert_sent: { msg: "1", present: [112] }
      - assert_sent: { msg: "1", recommended: [9999] }
      - session: logout
      - session: { reconnect: { reset: true } }
      - assert_sent: { msg: A, tags: { 141: "Y" } }
`
	f := newFake(t, "FIX.4.2")
	f.react = func(f *fake, kind string, m *codec.Message) {
		if kind == "testreq" {
			f.clk.Advance(time.Millisecond)
			f.in("0", codec.F(112, m.Value(112)))
		}
	}
	r := runWith(f, suiteFrom(t, head+body), Options{})
	c := r.Cases[0]
	if c.Status != StatusPass || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "recommended tag(s) absent: 9999") {
		t.Fatalf("%s %s %v", c.Status, c.Reason, c.Warnings)
	}
	if strings.Join(f.calls, ",") != "mark cert case start,logout,connect reset=true,mark cert case end" {
		t.Fatal(f.calls)
	}
}

func TestSessionDropIsError(t *testing.T) {
	f := newFake(t, "FIX.4.2")
	f.react = func(f *fake, kind string, m *codec.Message) {
		if kind == "D" {
			f.up = false
			f.dropped = "connection closed by counterparty"
		}
	}
	f.failConnect = false
	s := suiteFrom(t, head+orderCase)
	r := runWith(f, s, Options{})
	c := r.Cases[0]
	if c.Status != StatusError || !strings.Contains(c.Reason, "session dropped: connection closed by counterparty") {
		t.Fatalf("%s %s", c.Status, c.Reason)
	}
	if ExitCode(r, 0) != ExitRunnerError {
		t.Fatal("exit")
	}
	// If it cannot come back, the session failed: exit 3.
	f2 := newFake(t, "FIX.4.2")
	f2.react = func(f *fake, kind string, m *codec.Message) {
		if kind == "D" {
			f.up, f.dropped, f.failConnect = false, "connection lost", true
		}
	}
	r2 := runWith(f2, s, Options{})
	if !r2.SessionDead || ExitCode(r2, 0) != ExitDropped {
		t.Fatalf("dead %v exit %d", r2.SessionDead, ExitCode(r2, 0))
	}
}

func TestCaseTimeout(t *testing.T) {
	body := `  - id: "x"
    section: ORD
    title: long
    task: t
    required: true
    level: basic
    mode: auto
    steps:
      - session: { wait: 30s }
      - expect: { msg: "0", within: 60s }
`
	f := newFake(t, "FIX.4.2")
	r := runWith(f, suiteFrom(t, head+body), Options{CaseTimeout: 40 * time.Second})
	if c := r.Cases[0]; c.Status != StatusFail || !strings.Contains(c.Reason, "case timeout after 40s") {
		t.Fatalf("%s %s", c.Status, c.Reason)
	}
}

// ------------------------------------------------------------ modes

const modes = `  - { id: "m1", section: ORD, title: manual, task: t, required: true, level: basic, mode: manual, steps: [ { manual: { prompt: "confirm it" } } ] }
  - { id: "a1", section: ORD, title: assisted, task: t, required: true, level: basic, mode: assisted, steps: [ { control: { description: "venue sends a TestRequest", post: "/sessions/{session}/test-request" } }, { expect: { msg: "1", within: 2s } } ] }
  - { id: "a2", section: ORD, title: nobody can, task: t, required: false, level: basic, mode: assisted, steps: [ { control: { description: "venue ends the day" } } ] }
  - { id: "n1", section: ORD, title: n/a, task: t, required: true, level: basic, mode: auto, steps: [ { session: testreq } ] }
`

func TestModesAttestationsAndExitCodes(t *testing.T) {
	s := suiteFrom(t, head+modes)
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted = append(posted, r.Method+" "+r.URL.Path)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	tgt := &Target{Name: "emu", ControlAPI: srv.URL, Sessions: map[string]string{"s1": "agent42"}, Vars: map[string]string{},
		NotApplicable: map[string]string{"n1": "not on this venue"}}

	f := newFake(t, "FIX.4.2")
	// The control call "makes" the venue send a TestRequest.
	realDo := http.DefaultTransport
	_ = realDo
	go func() {}()
	r := runWith(f, s, Options{Target: tgt, HTTP: &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(req)
		f.in("1", codec.F(112, "V-1"))
		return resp, err
	})}})
	st := map[string]*CaseResult{}
	for _, c := range r.Cases {
		st[c.ID] = c
	}
	if st["m1"].Status != StatusPending || !strings.Contains(st["m1"].Reason, "confirm it") {
		t.Fatalf("manual %+v", st["m1"])
	}
	if st["a1"].Status != StatusPass || len(posted) != 1 || posted[0] != "POST /sessions/agent42/test-request" {
		t.Fatalf("assisted %s %s %v", st["a1"].Status, st["a1"].Reason, posted)
	}
	if st["a2"].Status != StatusBlocked || st["a2"].Reason != "BLOCKED: needs counterparty action: venue ends the day" {
		t.Fatalf("blocked %+v", st["a2"])
	}
	if st["n1"].Status != StatusNA || !strings.Contains(st["n1"].Reason, "not on this venue") {
		t.Fatalf("n/a %+v", st["n1"])
	}
	if ExitCode(r, 0) != ExitIncomplete { // required manual pending; the blocked one is optional
		t.Fatalf("exit %d", ExitCode(r, 0))
	}

	// With attestations for the manual and the blocked case: exit 0, recorded verbatim.
	att := map[string]Attestation{
		"m1": {Status: "pass", By: "Dan", Note: "checked", File: "a.yaml"},
		"a2": {Status: "na", By: "Dan", Note: "venue has no DFD"},
	}
	f2 := newFake(t, "FIX.4.2")
	r2 := runWith(f2, s, Options{Target: &Target{Name: "generic", NotApplicable: map[string]string{"n1": "x"}}, Attest: att})
	for _, c := range r2.Cases {
		st[c.ID] = c
	}
	if st["m1"].Status != StatusPass || st["m1"].Attestation == nil || st["m1"].Attestation.By != "Dan" || st["m1"].Reason != "attested pass by Dan: checked" {
		t.Fatalf("%+v", st["m1"])
	}
	if st["a2"].Status != StatusNA {
		t.Fatalf("%+v", st["a2"])
	}
	// No control API: a1 is BLOCKED (required) -> 7.
	if st["a1"].Status != StatusBlocked || ExitCode(r2, 0) != ExitIncomplete {
		t.Fatalf("%s exit %d", st["a1"].Status, ExitCode(r2, 0))
	}
	r2.Required = map[string]int{StatusPass: 3}
	r2.Counts = map[string]int{StatusPass: 3}
	if ExitCode(r2, 0) != ExitOK || ExitCode(r2, 4) != ExitLogoutTO {
		t.Fatal("exit ok / logout timeout")
	}
	if ExitCode(&RunResult{NeverLoggedOn: true}, 0) != ExitLogonFailed {
		t.Fatal("logon failed")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAttestationFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "att.yaml")
	os.WriteFile(p, []byte(`"1.1": { status: pass, by: "Dan Gavin", note: "docs received 2026-09-29" }
"9.3": { status: FAIL, by: ops, note: "no approval yet" }
`), 0o644)
	a, err := LoadAttestations(p)
	if err != nil || a["1.1"].By != "Dan Gavin" || a["9.3"].Status != "fail" || a["1.1"].File != "att.yaml" {
		t.Fatalf("%+v %v", a, err)
	}
	os.WriteFile(p, []byte(`"1.1": { status: maybe, by: x }`), 0o644)
	if _, err := LoadAttestations(p); err == nil || !strings.Contains(err.Error(), `"1.1"`) {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte(`"1.1": { status: pass }`), 0o644)
	if _, err := LoadAttestations(p); err == nil || !strings.Contains(err.Error(), "'by'") {
		t.Fatal(err)
	}
}

func TestStopOnFailAndAggregation(t *testing.T) {
	s := suiteFrom(t, head+orderCase+strings.Replace(orderCase, `"4.1"`, `"4.2"`, 1))
	f := newFake(t, "FIX.4.2") // never answers
	r := runWith(f, s, Options{StopOnFail: true})
	if r.Cases[0].Status != StatusFail || r.Cases[1].Status != StatusNotRun || r.Counts[StatusFail] != 1 || r.Required[StatusNotRun] != 1 {
		t.Fatalf("%+v", r.Counts)
	}
	dir := t.TempDir()
	if err := WriteResults(dir, r); err != nil {
		t.Fatal(err)
	}
	sum, _ := os.ReadFile(filepath.Join(dir, "summary.txt"))
	res, _ := os.ReadFile(filepath.Join(dir, "results.json"))
	if !strings.Contains(string(sum), "4.1") || !strings.Contains(string(res), `"status": "FAIL"`) || !strings.Contains(string(res), `"steps"`) {
		t.Fatalf("%s", sum)
	}
}

func TestRejectFailsFast(t *testing.T) {
	f := newFake(t, "FIX.4.2")
	f.react = func(f *fake, kind string, m *codec.Message) {
		if kind == "D" {
			ack(f, m.Value(11), 20*time.Millisecond, "8", "8", codec.F(58, "Strict broker rejects everything"))
		}
	}
	r := runWith(f, suiteFrom(t, head+orderCase), Options{})
	c := r.Cases[0]
	if c.Status != StatusFail || !strings.Contains(c.Reason, "step 2 (expect): the counterparty rejected the order") || !strings.Contains(c.Reason, "58=Strict broker rejects everything") {
		t.Fatalf("%s", c.Reason)
	}
	t1, _ := time.Parse("2006-01-02T15:04:05.000Z", c.Steps[0].TS)
	t2, _ := time.Parse("2006-01-02T15:04:05.000Z", c.Steps[1].TS)
	if t2.Sub(t1) > time.Second {
		t.Fatalf("did not fail fast: %s", t2.Sub(t1))
	}
}
