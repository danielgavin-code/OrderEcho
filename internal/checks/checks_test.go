package checks

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
)

// These tests port ../OrderEchoFixEmulator/tests/test_Timeline.py case for
// case (the "real emulator" cases live in the interop suite).

type kv struct {
	tag   int
	value string
}

func fixLine(msgType string, seq int, direction, version, sender, target string, fields []kv) string {
	var body []codec.Field
	for _, f := range fields {
		body = append(body, codec.F(f.tag, f.value))
	}
	raw := codec.Encode(version, msgType, codec.Header{SenderCompID: sender, TargetCompID: target,
		MsgSeqNum: seq, SendingTime: "20260927-09:00:00.000"}, body)
	return fmt.Sprintf("20260927-09:00:%02d.000 %-4s %-8s 35=%-3s %s", seq%60, direction, fmt.Sprintf("seq=%d", seq), msgType, codec.ToPipe(raw))
}

// line mirrors the Python helper: every message is 49=ORDERECHO 56=AGENT.
func line(msgType string, seq int, direction string, fields ...kv) string {
	return fixLine(msgType, seq, direction, "FIX.4.2", "ORDERECHO", "AGENT", fields)
}

type rep struct {
	seq                                                              int
	execType, ordStatus, orderQty, cum, leaves, avg, lastQty, lastPx string
	clOrdID, execID, orderID, version                                string
	extra                                                            []kv
}

func report(r rep) string {
	def := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	execID := def(r.execID, fmt.Sprintf("E-%d", r.seq))
	fields := []kv{{37, def(r.orderID, "O-1")}, {11, def(r.clOrdID, "C1")}, {17, execID},
		{150, r.execType}, {39, r.ordStatus}, {55, "AAPL"}, {54, "1"},
		{38, def(r.orderQty, "1000")}, {40, "2"}, {44, "10.00"}, {32, def(r.lastQty, "0")},
		{31, def(r.lastPx, "0.00")}, {151, def(r.leaves, "1000")}, {14, def(r.cum, "0")},
		{6, def(r.avg, "0.0000")}, {60, "20260927-09:00:00.000"}}
	version := def(r.version, "FIX.4.2")
	if version == "FIX.4.2" {
		fields = append(fields, kv{20, "0"})
	}
	// dict.update semantics: override in place, else append.
	for _, e := range r.extra {
		replaced := false
		for i := range fields {
			if fields[i].tag == e.tag {
				fields[i].value = e.value
				replaced = true
			}
		}
		if !replaced {
			fields = append(fields, e)
		}
	}
	return fixLine("8", r.seq, "OUT", version, "ORDERECHO", "AGENT", fields)
}

func parse(lines []string) []*Message {
	var p Parser
	return p.ParseLines(lines, "t.log", "s", true)
}

func chainOf(lines []string, clOrdID, orderID string) *Chain {
	return BuildChain(parse(lines), clOrdID, orderID)
}

func statusOf(t *testing.T, c *Chain, name string) Result {
	t.Helper()
	for _, r := range c.Checks {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no check named %s", name)
	return Result{}
}

var orderFields = func(id, orig, qty string) []kv {
	f := []kv{{11, id}}
	if orig != "" {
		f = append(f, kv{41, orig})
	}
	return append(f, kv{55, "AAPL"}, kv{54, "1"}, kv{38, qty}, kv{40, "2"}, kv{44, "10.00"}, kv{21, "1"}, kv{60, "20260927-09:00:00.000"})
}

var replaceChain = []string{
	line("D", 1, "IN", orderFields("C1", "", "1000")...),
	report(rep{seq: 2, execType: "0", ordStatus: "0", clOrdID: "C1"}),
	line("G", 3, "IN", orderFields("C2", "C1", "2000")...),
	report(rep{seq: 4, execType: "5", ordStatus: "0", clOrdID: "C2", orderQty: "2000", leaves: "2000", extra: []kv{{41, "C1"}}}),
	line("G", 5, "IN", orderFields("C3", "C2", "3000")...),
	report(rep{seq: 6, execType: "5", ordStatus: "0", clOrdID: "C3", orderQty: "3000", leaves: "3000", extra: []kv{{41, "C2"}}}),
	line("F", 7, "IN", kv{11, "C4"}, kv{41, "C3"}, kv{55, "AAPL"}, kv{54, "1"}, kv{60, "20260927-09:00:00.000"}),
	report(rep{seq: 8, execType: "4", ordStatus: "4", clOrdID: "C4", orderQty: "3000", leaves: "0", extra: []kv{{41, "C3"}}}),
}

var unrelated = []string{
	line("D", 20, "IN", kv{11, "OTHER"}, kv{55, "MSFT"}, kv{54, "1"}, kv{38, "50"}, kv{40, "1"}, kv{21, "1"}, kv{60, "20260927-09:00:00.000"}),
	report(rep{seq: 21, execType: "0", ordStatus: "0", clOrdID: "OTHER", orderID: "O-9", orderQty: "50", leaves: "50"}),
}

func joined(a ...[]string) []string {
	var out []string
	for _, x := range a {
		out = append(out, x...)
	}
	return out
}

func idsOf(c *Chain) string { return strings.Join(c.SortedIDs(), ",") }

// --- chains -------------------------------------------------------------

func TestWholeChainFoundFromAnyClOrdID(t *testing.T) {
	for _, seed := range []string{"C1", "C2", "C3", "C4"} {
		c := chainOf(joined(replaceChain, unrelated), seed, "")
		if idsOf(c) != "C1,C2,C3,C4" || len(c.Steps) != 8 {
			t.Fatalf("%s: ids %s steps %d", seed, idsOf(c), len(c.Steps))
		}
		for _, s := range c.Steps {
			if s.Message.Value(55) != "AAPL" {
				t.Fatalf("%s: unrelated message joined", seed)
			}
		}
	}
}

func TestChainFoundFromOrderID(t *testing.T) {
	c := chainOf(joined(replaceChain, unrelated), "", "O-1")
	if idsOf(c) != "C1,C2,C3,C4" || strings.Join(c.SortedOrderIDs(), ",") != "O-1" {
		t.Fatalf("%s %v", idsOf(c), c.SortedOrderIDs())
	}
}

func TestCancelRejectJoinsChain(t *testing.T) {
	lines := joined(replaceChain, []string{line("9", 9, "OUT", kv{37, "O-1"}, kv{11, "C5"}, kv{41, "C4"}, kv{39, "4"}, kv{434, "1"}, kv{102, "0"}, kv{58, "Order is CANCELED"})})
	c := chainOf(lines, "C1", "")
	if !c.IDs["C5"] {
		t.Fatal("C5 not in chain")
	}
}

func TestUnknownOrderIDDoesNotGlueChains(t *testing.T) {
	lines := joined(replaceChain, unrelated, []string{line("9", 30, "OUT", kv{37, "NONE"}, kv{11, "Z1"}, kv{41, "NOSUCH"}, kv{39, "8"}, kv{434, "1"}, kv{102, "1"}, kv{58, "Unknown order"})})
	c := chainOf(lines, "C1", "")
	if c.IDs["Z1"] || c.OrderIDs["NONE"] {
		t.Fatal("NONE glued chains")
	}
}

func TestEmptyChainIsNotAnError(t *testing.T) {
	c := chainOf(replaceChain, "NOTHERE", "")
	if len(c.Steps) != 0 || c.Verdict() != PASS {
		t.Fatal("empty chain")
	}
}

func TestReplaceAndCancelChainPasses(t *testing.T) {
	c := chainOf(replaceChain, "C1", "")
	if c.Verdict() != PASS || len(c.Checks) != 11 {
		t.Fatalf("%+v", c.Checks)
	}
	names := map[string]bool{}
	for _, r := range c.Checks {
		names[r.Name] = true
		if r.Rule == "" {
			t.Fatalf("%s has no rule", r.Name)
		}
	}
	if len(names) != 11 {
		t.Fatal("check names")
	}
}

// --- one hand-crafted failure per check ---------------------------------

func TestCheck1CumQtyBackwards(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "1", ordStatus: "1", cum: "500", leaves: "500", lastQty: "500", lastPx: "10.00", avg: "10.0000"}),
		report(rep{seq: 2, execType: "1", ordStatus: "1", cum: "400", leaves: "600", lastQty: "100", lastPx: "10.00", avg: "10.0000"}),
	}, "C1", "")
	r := statusOf(t, c, "cum_qty_monotonic")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "500 -> 400") || c.Verdict() != FAIL || c.ExitCode() != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestCheck2WorkingQuantities(t *testing.T) {
	c := chainOf([]string{report(rep{seq: 1, execType: "0", ordStatus: "0", orderQty: "1000", cum: "0", leaves: "900"})}, "C1", "")
	r := statusOf(t, c, "working_quantities")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "OrderQty is 1000") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck3TerminalLeavesOpen(t *testing.T) {
	c := chainOf([]string{report(rep{seq: 1, execType: "4", ordStatus: "4", cum: "0", leaves: "250"})}, "C1", "")
	r := statusOf(t, c, "terminal_quantities")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "LeavesQty is 250") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck3FilledNotWholeOrder(t *testing.T) {
	c := chainOf([]string{report(rep{seq: 1, execType: "2", ordStatus: "2", orderQty: "1000", cum: "900", leaves: "0", lastQty: "900", lastPx: "10.00", avg: "10.0000"})}, "C1", "")
	r := statusOf(t, c, "terminal_quantities")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "CumQty 900") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck4FillsDoNotSum(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "1", ordStatus: "1", cum: "400", leaves: "600", lastQty: "400", lastPx: "10.00", avg: "10.0000"}),
		report(rep{seq: 2, execType: "2", ordStatus: "2", cum: "1000", leaves: "0", lastQty: "100", lastPx: "10.00", avg: "10.0000"}),
	}, "C1", "")
	r := statusOf(t, c, "fill_quantities_sum")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "totalling 500") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck5AvgPxWrong(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "1", ordStatus: "1", cum: "100", leaves: "900", lastQty: "100", lastPx: "10.00", avg: "10.0000"}),
		report(rep{seq: 2, execType: "1", ordStatus: "1", cum: "200", leaves: "800", lastQty: "100", lastPx: "20.00", avg: "10.0000"}),
	}, "C1", "")
	r := statusOf(t, c, "avg_px")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "15") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck5AcceptsWeightedAverage(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "1", ordStatus: "1", cum: "100", leaves: "900", lastQty: "100", lastPx: "10.00", avg: "10.0000"}),
		report(rep{seq: 2, execType: "1", ordStatus: "1", cum: "200", leaves: "800", lastQty: "100", lastPx: "20.00", avg: "15.0000"}),
	}, "C1", "")
	if r := statusOf(t, c, "avg_px"); r.Status != PASS {
		t.Fatalf("%+v", r)
	}
}

func TestCheck5Tolerance(t *testing.T) {
	// 1/3 of the way: 10, 10, 11 -> 10.333333...; 10.3333 is within 0.0001,
	// 10.3331 is not.
	mk := func(avg string) *Chain {
		return chainOf([]string{
			report(rep{seq: 1, execType: "1", ordStatus: "1", cum: "1", leaves: "2", orderQty: "3", lastQty: "1", lastPx: "10", avg: "10"}),
			report(rep{seq: 2, execType: "1", ordStatus: "1", cum: "2", leaves: "1", orderQty: "3", lastQty: "1", lastPx: "10", avg: "10"}),
			report(rep{seq: 3, execType: "2", ordStatus: "2", cum: "3", leaves: "0", orderQty: "3", lastQty: "1", lastPx: "11", avg: avg}),
		}, "C1", "")
	}
	if r := statusOf(t, mk("10.3333"), "avg_px"); r.Status != PASS {
		t.Fatalf("%+v", r)
	}
	if r := statusOf(t, mk("10.3331"), "avg_px"); r.Status != FAIL || !strings.Contains(r.Explanation, "10.333333") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck6RepeatedExecID(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "0", ordStatus: "0", execID: "E-SAME"}),
		report(rep{seq: 2, execType: "1", ordStatus: "1", execID: "E-SAME", cum: "100", leaves: "900", lastQty: "100", lastPx: "10.00", avg: "10.0000"}),
	}, "C1", "")
	r := statusOf(t, c, "exec_ids_unique")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "E-SAME") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck7TwoOrderIDs(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "0", ordStatus: "0", orderID: "O-1"}),
		report(rep{seq: 2, execType: "0", ordStatus: "0", orderID: "O-2"}),
	}, "C1", "")
	r := statusOf(t, c, "order_id_constant")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "O-1") || !strings.Contains(r.Explanation, "O-2") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck8ReportAfterTerminal(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "2", ordStatus: "2", cum: "1000", leaves: "0", lastQty: "1000", lastPx: "10.00", avg: "10.0000"}),
		report(rep{seq: 2, execType: "1", ordStatus: "1", cum: "1100", leaves: "0", lastQty: "100", lastPx: "10.00", avg: "10.0000"}),
	}, "C1", "")
	r := statusOf(t, c, "nothing_after_terminal")
	if r.Status != FAIL || !strings.Contains(r.Explanation, "terminal") {
		t.Fatalf("%+v", r)
	}
}

func TestCheck8IgnoresReplayedPossDup(t *testing.T) {
	c := chainOf([]string{
		report(rep{seq: 1, execType: "2", ordStatus: "2", cum: "1000", leaves: "0", lastQty: "1000", lastPx: "10.00", avg: "10.0000", execID: "E-1"}),
		report(rep{seq: 1, execType: "2", ordStatus: "2", cum: "1000", leaves: "0", lastQty: "1000", lastPx: "10.00", avg: "10.0000", execID: "E-1",
			extra: []kv{{43, "Y"}, {122, "20260927-09:00:01.000"}}}),
	}, "C1", "")
	if len(c.Steps) != 2 || !c.Steps[1].Replay {
		t.Fatal("replay not marked")
	}
	for _, n := range []string{"nothing_after_terminal", "exec_ids_unique", "fill_quantities_sum"} {
		if r := statusOf(t, c, n); r.Status != PASS {
			t.Fatalf("%+v", r)
		}
	}
	if c.Verdict() != PASS {
		t.Fatal("verdict")
	}
}

func TestCheck9Version(t *testing.T) {
	cases := []struct {
		r    rep
		want string
	}{
		{rep{seq: 1, execType: "2", ordStatus: "2", version: "FIX.4.4", cum: "1000", leaves: "0", lastQty: "1000", lastPx: "10.00", avg: "10.0000"}, "150=2"},
		{rep{seq: 1, execType: "F", ordStatus: "2", version: "FIX.4.4", cum: "1000", leaves: "0", lastQty: "1000", lastPx: "10.00", avg: "10.0000", extra: []kv{{20, "0"}}}, "tag 20"},
		{rep{seq: 1, execType: "F", ordStatus: "2", version: "FIX.4.2", cum: "1000", leaves: "0", lastQty: "1000", lastPx: "10.00", avg: "10.0000"}, "150=F"},
	}
	for _, tc := range cases {
		r := statusOf(t, chainOf([]string{report(tc.r)}, "C1", ""), "version_rules")
		if r.Status != FAIL || !strings.Contains(r.Explanation, tc.want) {
			t.Fatalf("%+v", r)
		}
	}
}

func TestCheck10RequestNobodyAnswered(t *testing.T) {
	c := chainOf([]string{line("D", 1, "IN", orderFields("C1", "", "1000")...)}, "C1", "")
	r := statusOf(t, c, "requests_answered")
	if r.Status != WARN || !strings.Contains(r.Explanation, "seq=1") || c.Verdict() != WARN || c.ExitCode() != 1 {
		t.Fatalf("%+v", r)
	}
}

// Ported with realistic CompIDs: the D comes from AGENT and the Reject from
// ORDERECHO. (The Python helper writes both as ORDERECHO; see the next test.)
func TestCheck10SessionRejectCountsAsAnswer(t *testing.T) {
	c := chainOf([]string{
		fixLine("D", 1, "IN", "FIX.4.2", "AGENT", "ORDERECHO", orderFields("C1", "", "1000")),
		line("3", 2, "OUT", kv{45, "1"}, kv{371, "38"}, kv{373, "1"}, kv{58, "Required tag missing: 38"}),
	}, "C1", "")
	if r := statusOf(t, c, "requests_answered"); r.Status != PASS {
		t.Fatalf("%+v", r)
	}
	if len(c.Steps) != 2 {
		t.Fatal("reject not pulled into the chain")
	}
}

// The deliberate difference from Python: a Reject sent by the request's own
// sender is not an answer (Python counts it).
func TestCheck10OwnRejectIsNotAnAnswer(t *testing.T) {
	c := chainOf([]string{
		fixLine("D", 5, "OUT", "FIX.4.2", "AGENT", "ORDERECHO", orderFields("C1", "", "1000")),
		fixLine("3", 9, "OUT", "FIX.4.2", "AGENT", "ORDERECHO", []kv{{45, "5"}, {373, "1"}, {58, "about their seq 5"}}),
	}, "C1", "")
	if r := statusOf(t, c, "requests_answered"); r.Status != WARN || len(c.Steps) != 1 {
		t.Fatalf("%+v steps=%d", r, len(c.Steps))
	}
}

func TestCheck11Framing(t *testing.T) {
	good := report(rep{seq: 1, execType: "0", ordStatus: "0"})
	bad := good[:len(good)-4] + "999|"
	c := chainOf([]string{bad}, "C1", "")
	r := statusOf(t, c, "framing_intact")
	if r.Status != WARN || !strings.Contains(r.Explanation, "bad CheckSum") {
		t.Fatalf("%+v", r)
	}
}

// --- the checks never throw -------------------------------------------

func TestChecksSurviveJunk(t *testing.T) {
	c := chainOf([]string{report(rep{seq: 1, execType: "0", ordStatus: "0", cum: "nonsense", leaves: "", avg: "oops", orderQty: "lots", lastQty: "?", lastPx: "free"})}, "C1", "")
	for _, r := range c.Checks {
		if r.Explanation == "" || (r.Status != PASS && r.Status != WARN && r.Status != FAIL) {
			t.Fatalf("%+v", r)
		}
	}
}

func TestChecksSurviveEmptyChain(t *testing.T) {
	rs := RunChecks(&Chain{Seed: "none"})
	if len(rs) != 11 {
		t.Fatal(len(rs))
	}
	for _, r := range rs {
		if r.Status != PASS {
			t.Fatalf("%+v", r)
		}
	}
}

// --- parser ------------------------------------------------------------

func TestSplitFieldsKeepsOrderAndRepeats(t *testing.T) {
	got := SplitFields("8=FIX.4.2\x0155=A\x0155=B\x01junk\x01=x\x01")
	want := []Field{{8, "FIX.4.2"}, {55, "A"}, {55, "B"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v", got)
	}
}

func TestVerifyFraming(t *testing.T) {
	raw := string(codec.Encode("FIX.4.2", "0", codec.Header{SenderCompID: "A", TargetCompID: "B", MsgSeqNum: 1, SendingTime: "x"}, nil))
	if bl, bc := VerifyFraming(raw); bl || bc {
		t.Fatal("good flagged")
	}
	if bl, bc := VerifyFraming(raw[:len(raw)-4] + "999\x01"); bl || !bc {
		t.Fatal("bad checksum")
	}
	if bl, _ := VerifyFraming(strings.Replace(raw, "9=", "9=1", 1)); !bl {
		t.Fatal("bad length")
	}
	if bl, bc := VerifyFraming("junk"); !bl || !bc {
		t.Fatal("junk")
	}
}

func TestFindMessagesDelimiters(t *testing.T) {
	raw := "8=FIX.4.2|9=5|35=0|10=123|"
	for _, text := range []string{
		"prefix " + raw + " suffix",
		strings.ReplaceAll(raw, "|", "\x01"),
		strings.ReplaceAll(raw, "|", "^A"),
	} {
		f := FindMessages(text)
		if len(f) != 1 {
			t.Fatalf("%q: %v", text, f)
		}
	}
	if f := FindMessages(raw + raw); len(f) != 2 {
		t.Fatal("two on one line")
	}
	// Go ends only at a real 10= field (Python finding: 110= ends it early).
	f := FindMessages("8=FIX.4.2|9=5|35=D|110=100|10=123|")
	if len(f) != 1 || !strings.Contains(f[0].Raw, "110=100|10=123|") {
		t.Fatalf("%v", f)
	}
}

func TestParserEvidenceAndLogLines(t *testing.T) {
	var p Parser
	raw := "8=FIX.4.2|9=5|35=0|10=123|"
	lines := []string{
		`{"ts":"2026-09-27T09:00:00.123Z","kind":"out","session":"emu42","raw":"` + raw + `","injected":true,"detail":"x"}`,
		`{"ts":"2026-09-27T09:00:00.124Z","kind":"event","session":"emu42","raw":null}`,
		"20260927-09:00:00.125 DISC -        -     " + raw + "  # bad checksum",
		"garbage line",
		"2026-09-27 09:00:00.200 : " + raw,
	}
	ms := p.ParseLines(lines, "f", "", false)
	if len(ms) != 3 || p.Stats.Events != 1 || p.Stats.SkippedLines != 1 {
		t.Fatalf("%d msgs stats %+v", len(ms), p.Stats)
	}
	if ms[0].Direction != DirOut || ms[0].Session != "emu42" || !ms[0].Injected || ms[0].TS.Nanosecond() != 123_000_000 {
		t.Fatalf("%+v", ms[0])
	}
	if ms[1].Direction != DirDisc || ms[1].Comment != "bad checksum" {
		t.Fatalf("%+v", ms[1])
	}
	if ms[2].Direction != DirUnknown || ms[2].TS == nil {
		t.Fatalf("%+v", ms[2])
	}
	if s, ok := SessionFromFilename("logs/fix/agent44_20260927.log"); !ok || s != "agent44" {
		t.Fatal(s)
	}
}

func TestMergeChronologically(t *testing.T) {
	var p Parser
	a := p.ParseLines([]string{
		"20260927-09:00:05.000 OUT  seq=1    35=0  8=FIX.4.2|9=5|35=0|10=1|",
		"no stamp 8=FIX.4.2|9=5|35=0|34=99|10=1|",
	}, "a", "", false)
	b := p.ParseLines([]string{"20260927-09:00:01.000 IN   seq=2    35=0  8=FIX.4.2|9=5|35=0|10=2|"}, "b", "", false)
	c := p.ParseLines([]string{"nothing 8=FIX.4.2|9=5|35=0|10=3|"}, "c", "", false)
	merged := MergeChronologically(append(append(append([]*Message{}, c...), a...), b...))
	var order []string
	for _, m := range merged {
		order = append(order, m.Source)
	}
	if strings.Join(order, "") != "baac" {
		t.Fatalf("%v", order)
	}
}

func TestDecimalParsing(t *testing.T) {
	for in, ok := range map[string]bool{"1_000": true, " 12 ": true, "+5": true, "1e3": true, ".5": true, "5.": true,
		"1/2": false, "0x10": false, "Infinity": false, "nan": false, "": false, "1 000": false, "12\x01": false} {
		if _, got := ParseDec(in); got != ok {
			t.Fatalf("%q: %v", in, got)
		}
	}
	for in, want := range map[string]int{" 12": 12, "1_2": 12, "+3": 3, "-1": -1, "00": 0} {
		if got, ok := PyInt(in); !ok || got != want {
			t.Fatalf("%q: %d %v", in, got, ok)
		}
	}
	for _, in := range []string{"1\x01", "1__2", "_1", ""} {
		if _, ok := PyInt(in); ok {
			t.Fatalf("%q accepted", in)
		}
	}
	d, _ := ParseDec("15")
	if FormatRat(d.R, 6) != "15.000000" {
		t.Fatal(FormatRat(d.R, 6))
	}
	x, _ := ParseDec("2.5")
	if FormatRat(x.R, 0) != "2" { // half-even
		t.Fatal(FormatRat(x.R, 0))
	}
}

var _ = sort.Strings

// A3 3.2: the reject of a duplicate request forms its own chain.
func TestDuplicateRejectSplitIntoOwnChain(t *testing.T) {
	d := fixLine("D", 2, "OUT", "FIX.4.2", "AGENT", "ORDERECHO", orderFields("C1", "", "1000"))
	dup := fixLine("D", 3, "OUT", "FIX.4.2", "AGENT", "ORDERECHO", orderFields("C1", "", "1000"))
	lines := []string{
		d,
		report(rep{seq: 2, execType: "0", ordStatus: "0", orderID: "O-1"}),
		dup,
		report(rep{seq: 3, execType: "8", ordStatus: "8", orderID: "O-2", leaves: "0", extra: []kv{{103, "6"}, {58, "Duplicate ClOrdID"}}}),
	}
	orig := chainOf(lines, "C1", "")
	if orig.Verdict() != PASS || len(orig.Steps) != 2 || orig.OrderIDs["O-2"] {
		t.Fatalf("original: %s steps %d %+v", orig.Verdict(), len(orig.Steps), orig.Checks)
	}
	split := chainOf(lines, "", "O-2")
	if split.Verdict() != PASS || len(split.Steps) != 2 || split.Steps[1].Message.Value(103) != "6" {
		t.Fatalf("split: %s steps %d", split.Verdict(), len(split.Steps))
	}
	// No duplicate request -> no split: a second OrderID still FAILs.
	two := chainOf([]string{d, report(rep{seq: 2, execType: "0", ordStatus: "0", orderID: "O-1"}),
		report(rep{seq: 3, execType: "8", ordStatus: "8", orderID: "O-2", leaves: "0"})}, "C1", "")
	if statusOf(t, two, "order_id_constant").Status != FAIL {
		t.Fatal("split without a duplicate request")
	}
}
