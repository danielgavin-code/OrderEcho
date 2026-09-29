//go:build interop

package interop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

var logLineRE = regexp.MustCompile(`^(\S+ +\S+ +\S+ +\S+ +)(.*?)((?:  # .*)?)$`)

type logLine struct {
	prefix, raw, suffix string
	msg                 *codec.Message
}

func readLog(t *testing.T, path string) []*logLine {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []*logLine
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		m := logLineRE.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("unparsable line %q", l)
		}
		msg, err := codec.DecodeOne(codec.FromPipe(m[2]))
		if err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		out = append(out, &logLine{prefix: m[1], raw: m[2], suffix: m[3], msg: msg})
	}
	return out
}

func setField(msg *codec.Message, tag int, value string) {
	for i := range msg.Fields {
		if msg.Fields[i].Tag == tag {
			msg.Fields[i].Value = value
			return
		}
	}
	// Append before the CheckSum; Build drops/recomputes 8/9/10 anyway.
	msg.Fields = append(msg.Fields, codec.F(tag, value))
}

func (l *logLine) rebuild() {
	l.raw = codec.ToPipe(codec.Build(l.msg.Value(8), l.msg.Fields, false))
}

func writeLog(t *testing.T, dir, name string, lines []*logLine) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.prefix + l.raw + l.suffix + "\n")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func cloneLines(lines []*logLine) []*logLine {
	out := make([]*logLine, 0, len(lines))
	for _, l := range lines {
		c := *l
		m := *l.msg
		m.Fields = append([]codec.Field(nil), l.msg.Fields...)
		c.msg = &m
		out = append(out, &c)
	}
	return out
}

// reportsOf returns the ER lines of a chain (by ClOrdID) in log order.
func reportsOf(lines []*logLine, ids map[string]bool) []*logLine {
	var out []*logLine
	for _, l := range lines {
		if l.msg.MsgType() == "8" && (ids[l.msg.Value(11)] || ids[l.msg.Value(41)]) {
			out = append(out, l)
		}
	}
	return out
}

// TestParityTampered takes real agent logs from a live emulator run and
// breaks them one way per check; Go and Python must fail/warn identically.
func TestParityTampered(t *testing.T) {
	e, a, _ := startTrading(t, "FIX.4.4")
	odd := sendOrder(t, a, order.Spec{Symbol: "NOK", Qty: "1000", Side: "buy", OrdType: "mkt"})
	hold := sendOrder(t, a, order.Spec{Symbol: "ZWZZT", Qty: "500", Side: "buy", OrdType: "lmt", Price: "10.00"})
	waitState(t, a, hold, order.New)
	_, gid, err := a.ag.Replace("last", "800", "10.50")
	if err != nil {
		t.Fatal(err)
	}
	waitOrder(t, a, hold, "replace ack", 10*time.Second, func(o *order.Order) bool { return o.Accepted == gid })
	if _, _, err := a.ag.Cancel("last"); err != nil {
		t.Fatal(err)
	}
	waitState(t, a, hold, order.Canceled)
	waitState(t, a, odd, order.Filled)
	verdict(t, "tamper base: NOK odd lots (4.4)", a, odd, checks.PASS)
	verdict(t, "tamper base: ZWZZT replace/cancel (4.4)", a, hold, checks.PASS)
	finishAgent(t, a)
	_ = e

	base := readLog(t, a.fixLogPath())
	name := filepath.Base(a.fixLogPath())
	oddIDs := map[string]bool{odd.Root: true}
	holdIDs := map[string]bool{}
	a.ag.With(func(*order.Manager) {
		for _, id := range hold.ClOrdIDs {
			holdIDs[id] = true
		}
	})

	type tamper struct {
		check, want, root string
		mutate            func(lines []*logLine) []*logLine
	}
	tampers := []tamper{
		{"cum_qty_monotonic", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			r := reportsOf(ls, oddIDs)[3] // third fill: cum 6 -> 1
			setField(r.msg, 14, "1")
			r.rebuild()
			return ls
		}},
		{"working_quantities", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			r := reportsOf(ls, oddIDs)[1] // first partial
			setField(r.msg, 151, "1000")
			r.rebuild()
			return ls
		}},
		{"terminal_quantities", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			rs := reportsOf(ls, oddIDs)
			setField(rs[len(rs)-1].msg, 151, "5")
			rs[len(rs)-1].rebuild()
			return ls
		}},
		{"fill_quantities_sum", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			r := reportsOf(ls, oddIDs)[2] // fill of 2 -> 3
			setField(r.msg, 32, "3")
			r.rebuild()
			return ls
		}},
		{"avg_px", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			rs := reportsOf(ls, oddIDs)
			setField(rs[len(rs)-1].msg, 6, "99.0000")
			rs[len(rs)-1].rebuild()
			return ls
		}},
		{"exec_ids_unique", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			rs := reportsOf(ls, oddIDs)
			setField(rs[2].msg, 17, rs[1].msg.Value(17))
			rs[2].rebuild()
			return ls
		}},
		{"order_id_constant", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			r := reportsOf(ls, oddIDs)[2]
			setField(r.msg, 37, "O-OTHER")
			r.rebuild()
			return ls
		}},
		{"nothing_after_terminal", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			rs := reportsOf(ls, oddIDs)
			last := rs[len(rs)-1]
			extra := cloneLines([]*logLine{last})[0]
			setField(extra.msg, 17, last.msg.Value(17)+"-X")
			setField(extra.msg, 34, "9999")
			extra.rebuild()
			extra.prefix = strings.Replace(extra.prefix, extra.prefix[:21], "20991231-23:59:59.999", 1)
			return append(ls, extra)
		}},
		{"version_rules", checks.FAIL, odd.Root, func(ls []*logLine) []*logLine {
			r := reportsOf(ls, oddIDs)[1]
			setField(r.msg, 150, "1") // a 4.2 partial-fill ExecType on 4.4
			r.rebuild()
			return ls
		}},
		{"version_rules", checks.FAIL, hold.Root, func(ls []*logLine) []*logLine {
			r := reportsOf(ls, holdIDs)[0]
			setField(r.msg, 20, "0") // ExecTransType was removed in 4.4
			r.rebuild()
			return ls
		}},
		{"requests_answered", checks.WARN, hold.Root, func(ls []*logLine) []*logLine {
			rs := reportsOf(ls, holdIDs)
			drop := rs[len(rs)-1] // the cancel's answer
			var out []*logLine
			for _, l := range ls {
				if l != drop {
					out = append(out, l)
				}
			}
			return out
		}},
		{"framing_intact", checks.WARN, hold.Root, func(ls []*logLine) []*logLine {
			r := reportsOf(ls, holdIDs)[1]
			sum := r.raw[len(r.raw)-4 : len(r.raw)-1]
			bad := "000"
			if sum == "000" {
				bad = "001"
			}
			r.raw = r.raw[:len(r.raw)-4] + bad + "|"
			return ls
		}},
	}

	var cases []parityCase
	type expect struct{ check, want string }
	wants := map[string]expect{}
	for i, tp := range tampers {
		lines := tp.mutate(cloneLines(base))
		path := writeLog(t, filepath.Join(t.TempDir(), fmt.Sprintf("t%02d", i)), name, lines)
		c := parityCase{Name: fmt.Sprintf("tampered agent FIX log: %s (%s)", tp.check, tp.root), Files: []string{path}, ClOrdID: tp.root}
		cases = append(cases, c)
		wants[c.Name] = expect{tp.check, tp.want}
	}

	// One tamper on the evidence JSONL too: AvgPx edited in the "raw".
	evLines := strings.Split(strings.TrimRight(mustRead(t, a.ev.Path), "\n"), "\n")
	edited := false
	for i := len(evLines) - 1; i >= 0 && !edited; i-- {
		var rec map[string]any
		json.Unmarshal([]byte(evLines[i]), &rec)
		raw, _ := rec["raw"].(string)
		if rec["kind"] == "in" && strings.Contains(raw, "|11="+odd.Root+"|") && strings.Contains(raw, "|39=2|") {
			msg, _ := codec.DecodeOne(codec.FromPipe(raw))
			setField(msg, 6, "99.0000")
			rec["raw"] = codec.ToPipe(codec.Build(msg.Value(8), msg.Fields, false))
			b, _ := json.Marshal(rec)
			evLines[i] = string(b)
			edited = true
		}
	}
	if !edited {
		t.Fatal("no terminal report in evidence")
	}
	evPath := filepath.Join(t.TempDir(), filepath.Base(a.ev.Path))
	os.WriteFile(evPath, []byte(strings.Join(evLines, "\n")+"\n"), 0o644)
	evCase := parityCase{Name: "tampered agent evidence: avg_px (" + odd.Root + ")", Files: []string{evPath}, ClOrdID: odd.Root}
	cases = append(cases, evCase)
	wants[evCase.Name] = expect{"avg_px", checks.FAIL}

	assertParity(t, cases)
	for _, c := range cases {
		g := goParity(c)
		w := wants[c.Name]
		found := false
		for _, s := range g.Checks {
			if s.Name == w.check {
				found = true
				if s.Status != w.want {
					t.Errorf("%s: %s is %s, want %s", c.Name, w.check, s.Status, w.want)
				}
			}
		}
		if !found || g.Verdict == checks.PASS {
			t.Errorf("%s: tamper not detected (%s)", c.Name, g.nonPass())
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestParityKnownDivergences pins the three places where Go deliberately
// does not follow the Python checks (REPORT_A2 section 6). Each case must
// diverge exactly as documented; if Python is fixed, this test says so.
func TestParityKnownDivergences(t *testing.T) {
	requireEmulator(t)
	dir := t.TempDir()
	enc := func(version, msgType, sender, target string, seq int, body ...codec.Field) string {
		return codec.ToPipe(codec.Encode(version, msgType, codec.Header{SenderCompID: sender, TargetCompID: target,
			MsgSeqNum: seq, SendingTime: "20260929-09:00:00.000"}, body))
	}
	line := func(dir string, seq int, raw string) string {
		return fmt.Sprintf("20260929-09:00:%02d.000 %-4s %-8s %-5s %s", seq, dir, fmt.Sprintf("seq=%d", seq), "35=?", raw)
	}
	d := []codec.Field{codec.F(11, "DV-1"), codec.F(21, "1"), codec.F(55, "AAPL"), codec.F(54, "1"),
		codec.F(60, "20260929-09:00:00.000"), codec.F(38, "100"), codec.F(40, "1")}
	ack := func(extra ...codec.Field) []codec.Field {
		return append([]codec.Field{codec.F(37, "O-1"), codec.F(11, "DV-1"), codec.F(17, "E-1"), codec.F(20, "0"),
			codec.F(150, "0"), codec.F(39, "0"), codec.F(55, "AAPL"), codec.F(54, "1"), codec.F(38, "100"),
			codec.F(40, "1"), codec.F(32, "0"), codec.F(31, "0"), codec.F(151, "100"), codec.F(14, "0"), codec.F(6, "0")}, extra...)
	}
	write := func(name string, lines ...string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		return p
	}

	// 1. Our own Reject about their seq 5 "answers" our own D on seq 5 in Python.
	ownReject := write("dv1_20260929.log",
		line("OUT", 5, enc("FIX.4.2", "D", "AGENT", "ORDERECHO", 5, d...)),
		line("OUT", 6, enc("FIX.4.2", "3", "AGENT", "ORDERECHO", 6, codec.F(45, "5"), codec.F(373, "1"), codec.F(58, "about their seq 5"))))
	// 2. A tag ending in 10 (110=MinQty) makes Python cut the D short.
	dMin := append(append([]codec.Field{}, d...), codec.F(110, "100"))
	tag110 := write("dv2_20260929.log",
		line("OUT", 2, enc("FIX.4.2", "D", "AGENT", "ORDERECHO", 2, dMin...)),
		line("IN", 2, enc("FIX.4.2", "8", "ORDERECHO", "AGENT", 2, ack()...)))
	// 3. A non-ASCII (UTF-8) text makes Python's latin-1 checksum disagree.
	nonASCII := write("dv3_20260929.log",
		line("OUT", 2, enc("FIX.4.2", "D", "AGENT", "ORDERECHO", 2, d...)),
		line("IN", 2, enc("FIX.4.2", "8", "ORDERECHO", "AGENT", 2, ack(codec.F(58, "reçu"))...)))

	type div struct {
		c             parityCase
		check, goWant string
		pyWant        string
	}
	divs := []div{
		{parityCase{Name: "divergence 1: own Reject counted as an answer", Files: []string{ownReject}, ClOrdID: "DV-1"}, "requests_answered", checks.WARN, checks.PASS},
		{parityCase{Name: "divergence 2: tag 110= truncates the message", Files: []string{tag110}, ClOrdID: "DV-1"}, "framing_intact", checks.PASS, checks.WARN},
		{parityCase{Name: "divergence 3: non-ASCII value breaks the checksum", Files: []string{nonASCII}, ClOrdID: "DV-1"}, "framing_intact", checks.PASS, checks.WARN},
	}
	var cases []parityCase
	for _, d := range divs {
		cases = append(cases, d.c)
	}
	py := pythonParity(t, cases)
	for i, d := range divs {
		g := goParity(d.c)
		gs, ps := statusOf(g, d.check), statusOf(py[i], d.check)
		line := fmt.Sprintf("%-70s go=%s py=%s [%s: go %s, py %s]", d.c.Name, g.Verdict, py[i].Verdict, d.check, gs, ps)
		parityLog.Lock()
		parityLog.lines = append(parityLog.lines, "  DIVERGE  "+line+" (documented, expected)")
		parityLog.Unlock()
		if gs != d.goWant || ps != d.pyWant {
			t.Errorf("%s: go %s=%s (want %s), python %s=%s (want %s)", d.c.Name, d.check, gs, d.goWant, d.check, ps, d.pyWant)
		}
		t.Logf("%s", line)
	}
}

func statusOf(r parityResult, name string) string {
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status
		}
	}
	return "?"
}
