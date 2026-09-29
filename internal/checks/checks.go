package checks

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Epsilon is the AvgPx tolerance, as in Python (Decimal("0.0001")).
var Epsilon = big.NewRat(1, 10000)

// CheckNames lists the eleven checks in the order they run.
var CheckNames = []string{
	"cum_qty_monotonic", "working_quantities", "terminal_quantities",
	"fill_quantities_sum", "avg_px", "exec_ids_unique", "order_id_constant",
	"nothing_after_terminal", "version_rules", "requests_answered",
	"framing_intact",
}

// RunChecks runs every check; a check that panics is itself a FAIL.
func RunChecks(c *Chain) []Result {
	funcs := []func(*Chain) Result{
		CheckCumQtyMonotonic, CheckWorkingQuantities, CheckTerminalQuantities,
		CheckFillQuantitiesSum, CheckAvgPx, CheckExecIDsUnique,
		CheckOrderIDConstant, CheckNothingAfterTerminal, CheckVersionRules,
		CheckRequestsAnswered, CheckFramingIntact,
	}
	out := make([]Result, 0, len(funcs))
	for i, f := range funcs {
		out = append(out, safe(CheckNames[i], f, c))
	}
	return out
}

func safe(name string, f func(*Chain) Result, c *Chain) (r Result) {
	defer func() {
		if p := recover(); p != nil {
			r = Result{Name: name, Status: FAIL, Explanation: fmt.Sprintf("the check itself failed: %v", p)}
		}
	}()
	return f(c)
}

func dec(m *Message, tag int) (Dec, bool) { return DecOf(m.Get(tag)) }

// where points at a message in a way a human can find in the file.
func where(m *Message) string {
	var bits []string
	if seq, ok := m.Seq(); ok {
		bits = append(bits, fmt.Sprintf("seq=%d", seq))
	}
	if e := m.Value(TagExecID); e != "" {
		bits = append(bits, "17="+e)
	}
	if m.Source != "" && m.SourceLineNo != 0 {
		bits = append(bits, fmt.Sprintf("%s:%d", m.Source, m.SourceLineNo))
	}
	if len(bits) == 0 {
		return fmt.Sprintf("message #%d", m.Index)
	}
	return strings.Join(bits, " ")
}

// 1. CumQty (14) may only ever go up across a chain's reports.
func CheckCumQtyMonotonic(c *Chain) Result {
	const name, rule = "cum_qty_monotonic", "CumQty never decreases"
	var prev *Dec
	for _, m := range c.Reports() {
		cum, ok := dec(m, TagCumQty)
		if !ok {
			continue
		}
		if prev != nil && cum.R.Cmp(prev.R) < 0 {
			return Result{name, FAIL, fmt.Sprintf("CumQty went backwards, %s -> %s, at %s", prev.Text, cum.Text, where(m)), rule}
		}
		cc := cum
		prev = &cc
	}
	if prev == nil {
		return Result{name, PASS, "no reports carried a CumQty", rule}
	}
	return Result{name, PASS, fmt.Sprintf("CumQty rose to %s without ever falling", prev.Text), rule}
}

// 2. While an order is working, CumQty + LeavesQty == OrderQty.
func CheckWorkingQuantities(c *Chain) Result {
	const name, rule = "working_quantities", "working states: CumQty + LeavesQty == OrderQty"
	checked := 0
	for _, m := range c.Reports() {
		status := m.Value(TagOrdStatus)
		if _, has := m.Get(TagOrdStatus); !has || !WorkingStates[status] {
			continue
		}
		cum, ok1 := dec(m, TagCumQty)
		leaves, ok2 := dec(m, TagLeavesQty)
		qty, ok3 := dec(m, TagOrderQty)
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		checked++
		sum := new(big.Rat).Add(cum.R, leaves.R)
		if sum.Cmp(qty.R) != 0 {
			return Result{name, FAIL, fmt.Sprintf("39=%s: CumQty %s + LeavesQty %s = %s, but OrderQty is %s, at %s",
				status, cum.Text, leaves.Text, ratText(sum), qty.Text, where(m)), rule}
		}
	}
	if checked > 0 {
		return Result{name, PASS, fmt.Sprintf("%d working report(s) balanced", checked), rule}
	}
	return Result{name, PASS, "no working reports to check", rule}
}

// 3. A terminal report leaves nothing open; a fill has filled it all.
func CheckTerminalQuantities(c *Chain) Result {
	const name, rule = "terminal_quantities", "terminal states: LeavesQty == 0, and 39=2 implies CumQty == OrderQty"
	checked := 0
	for _, m := range c.Reports() {
		status := m.Value(TagOrdStatus)
		if _, has := m.Get(TagOrdStatus); !has || !TerminalStates[status] {
			continue
		}
		leaves, ok := dec(m, TagLeavesQty)
		if !ok {
			continue
		}
		checked++
		if leaves.R.Sign() != 0 {
			return Result{name, FAIL, fmt.Sprintf("39=%s is terminal but LeavesQty is %s, at %s", status, leaves.Text, where(m)), rule}
		}
		if status == "2" {
			cum, ok1 := dec(m, TagCumQty)
			qty, ok2 := dec(m, TagOrderQty)
			if ok1 && ok2 && cum.R.Cmp(qty.R) != 0 {
				return Result{name, FAIL, fmt.Sprintf("39=2 (Filled) with CumQty %s but OrderQty %s, at %s", cum.Text, qty.Text, where(m)), rule}
			}
		}
	}
	if checked > 0 {
		return Result{name, PASS, fmt.Sprintf("%d terminal report(s) consistent", checked), rule}
	}
	return Result{name, PASS, "no terminal reports to check", rule}
}

// 4. The fills add up to the final CumQty.
func CheckFillQuantitiesSum(c *Chain) Result {
	const name, rule = "fill_quantities_sum", "sum of LastQty over fills == final CumQty"
	total := new(big.Rat)
	fills := 0
	var final *Dec
	for _, m := range c.Reports() {
		if et, has := m.Get(TagExecType); has && FillExecTypes[et] {
			if last, ok := dec(m, TagLastQty); ok {
				total.Add(total, last.R)
				fills++
			}
		}
		if cum, ok := dec(m, TagCumQty); ok {
			cc := cum
			final = &cc
		}
	}
	if fills == 0 {
		return Result{name, PASS, "no fills in this chain", rule}
	}
	if final == nil {
		return Result{name, PASS, "no CumQty to compare the fills against", rule}
	}
	if total.Cmp(final.R) != 0 {
		return Result{name, FAIL, fmt.Sprintf("%d fill(s) totalling %s but the last CumQty is %s", fills, ratText(total), final.Text), rule}
	}
	return Result{name, PASS, fmt.Sprintf("%d fill(s) totalling %s match CumQty", fills, ratText(total)), rule}
}

// 5. AvgPx is the quantity-weighted mean of the fills, to 0.0001.
func CheckAvgPx(c *Chain) Result {
	const name, rule = "avg_px", "AvgPx == sum(LastQty x LastPx) / CumQty, within 0.0001"
	notional, quantity := new(big.Rat), new(big.Rat)
	var last *Message
	for _, m := range c.Reports() {
		if et, has := m.Get(TagExecType); !has || !FillExecTypes[et] {
			continue
		}
		q, ok1 := dec(m, TagLastQty)
		px, ok2 := dec(m, TagLastPx)
		if !ok1 || !ok2 {
			continue
		}
		notional.Add(notional, new(big.Rat).Mul(q.R, px.R))
		quantity.Add(quantity, q.R)
		last = m
	}
	if quantity.Sign() == 0 || last == nil {
		return Result{name, PASS, "no fills to average", rule}
	}
	stated, ok := dec(last, TagAvgPx)
	if !ok {
		return Result{name, PASS, "no AvgPx reported", rule}
	}
	expected := new(big.Rat).Quo(notional, quantity)
	diff := new(big.Rat).Sub(expected, stated.R)
	if diff.Abs(diff).Cmp(Epsilon) > 0 {
		return Result{name, FAIL, fmt.Sprintf("AvgPx reported %s but the fills average %s, at %s",
			stated.Text, FormatRat(expected, 6), where(last)), rule}
	}
	return Result{name, PASS, fmt.Sprintf("AvgPx %s matches the fills to within 0.0001", stated.Text), rule}
}

// 6. Every ExecutionReport in a chain has its own ExecID.
func CheckExecIDsUnique(c *Chain) Result {
	const name, rule = "exec_ids_unique", "ExecIDs are unique within the chain"
	seen := map[string]*Message{}
	for _, m := range c.Reports() {
		id := m.Value(TagExecID)
		if id == "" {
			continue
		}
		if first, dup := seen[id]; dup {
			return Result{name, FAIL, fmt.Sprintf("ExecID %s used twice, at %s and %s", id, where(first), where(m)), rule}
		}
		seen[id] = m
	}
	return Result{name, PASS, fmt.Sprintf("%d ExecID(s), all distinct", len(seen)), rule}
}

// 7. One order keeps one OrderID, however often its ClOrdID changes.
func CheckOrderIDConstant(c *Chain) Result {
	const name, rule = "order_id_constant", "OrderID is constant across the chain"
	var found []string
	for _, m := range c.Messages() {
		if id, ok := orderIDOf(m); ok && !contains(found, id) {
			found = append(found, id)
		}
	}
	if len(found) > 1 {
		return Result{name, FAIL, fmt.Sprintf("the chain carries %d OrderIDs: %s", len(found), strings.Join(found, ", ")), rule}
	}
	if len(found) == 1 {
		return Result{name, PASS, fmt.Sprintf("OrderID %s throughout", found[0]), rule}
	}
	return Result{name, PASS, "no OrderID on any message", rule}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// 8. Once an order is done, nothing more happens to it (replays excepted).
func CheckNothingAfterTerminal(c *Chain) Result {
	const name, rule = "nothing_after_terminal", "no state change after a terminal report (replays excepted)"
	var terminal *Message
	for _, s := range c.Steps {
		m := s.Message
		if m.MsgType() != MsgExecutionReport || s.Replay {
			continue
		}
		status := m.Value(TagOrdStatus)
		if terminal != nil {
			return Result{name, FAIL, fmt.Sprintf("39=%s at %s was terminal, but another report (39=%s) followed at %s",
				terminal.Value(TagOrdStatus), where(terminal), statusText(m), where(m)), rule}
		}
		if _, has := m.Get(TagOrdStatus); has && TerminalStates[status] {
			terminal = m
		}
	}
	if terminal != nil {
		return Result{name, PASS, fmt.Sprintf("terminal 39=%s was the last word", terminal.Value(TagOrdStatus)), rule}
	}
	return Result{name, PASS, "the order never reached a terminal state", rule}
}

func statusText(m *Message) string {
	if v, ok := m.Get(TagOrdStatus); ok {
		return v
	}
	return "None"
}

// 9. 4.4 fills are 150=F and carry no tag 20; 4.2 fills are 150=1 or 2.
func CheckVersionRules(c *Chain) Result {
	const name, rule = "version_rules", "ExecType and ExecTransType match the message's FIX version"
	for _, m := range c.Reports() {
		et := m.Value(TagExecType)
		switch m.BeginString() {
		case "FIX.4.4":
			if _, has := m.Get(TagExecTransType); has {
				return Result{name, FAIL, fmt.Sprintf("FIX.4.4 report carries tag 20, which was removed in 4.4, at %s", where(m)), rule}
			}
			if et == "1" || et == "2" {
				return Result{name, FAIL, fmt.Sprintf("FIX.4.4 fill uses 150=%s; 4.4 reports trades as 150=F, at %s", et, where(m)), rule}
			}
		case "FIX.4.2":
			if et == "F" {
				return Result{name, FAIL, fmt.Sprintf("FIX.4.2 fill uses 150=F, which arrived in 4.4; 4.2 reports 150=1 or 150=2, at %s", where(m)), rule}
			}
		}
	}
	return Result{name, PASS, "every report matches its version's conventions", rule}
}

// 10. Every order request in the chain got some answer.
func CheckRequestsAnswered(c *Chain) Result {
	const name, rule = "requests_answered", "each D/F/G is answered by an ER, a cancel reject or a Reject"
	var requests []*Message
	for _, m := range c.Messages() {
		if requestTypes[m.MsgType()] {
			requests = append(requests, m)
		}
	}
	if len(requests) == 0 {
		return Result{name, PASS, "no order requests in this chain", rule}
	}
	answered := map[string]bool{}
	type ref struct {
		seq  int
		from *Message
	}
	var refs []ref
	for _, m := range c.Messages() {
		if !responseTypes[m.MsgType()] {
			continue
		}
		for _, tag := range []int{TagClOrdID, TagOrigClOrdID} {
			if v := m.Value(tag); v != "" {
				answered[v] = true
			}
		}
		if seq, ok := intOfDec(m.Get(TagRefSeqNum)); ok {
			refs = append(refs, ref{seq, m})
		}
	}
	var unanswered []*Message
	for _, req := range requests {
		if id := req.Value(TagClOrdID); id != "" && answered[id] {
			continue
		}
		hit := false
		if seq, ok := req.Seq(); ok {
			for _, r := range refs {
				// Same deliberate difference as the chain builder: a Reject
				// from the request's own sender is not an answer to it.
				if r.seq == seq && !sentBySameSide(r.from, req) {
					hit = true
				}
			}
		}
		if !hit {
			unanswered = append(unanswered, req)
		}
	}
	if len(unanswered) > 0 {
		var listed []string
		for _, m := range unanswered {
			seq := "None"
			if s, ok := m.Seq(); ok {
				seq = strconv.Itoa(s)
			}
			id := "None"
			if v, ok := m.Get(TagClOrdID); ok {
				id = v
			}
			listed = append(listed, fmt.Sprintf("35=%s seq=%s 11=%s", m.MsgType(), seq, id))
		}
		return Result{name, WARN, fmt.Sprintf("%d request(s) with no response in this log: %s", len(unanswered), strings.Join(listed, ", ")), rule}
	}
	return Result{name, PASS, fmt.Sprintf("all %d request(s) answered", len(requests)), rule}
}

// 11. No message in the chain arrived with a broken CheckSum or BodyLength.
func CheckFramingIntact(c *Chain) Result {
	const name, rule = "framing_intact", "every message in the chain is correctly framed (9 and 10 agree)"
	var broken []*Message
	for _, m := range c.Messages() {
		if m.BadChecksum || m.BadLength {
			broken = append(broken, m)
		}
	}
	if len(broken) == 0 {
		return Result{name, PASS, fmt.Sprintf("all %d message(s) correctly framed", len(c.Steps)), rule}
	}
	var listed []string
	for _, m := range broken {
		what := "bad BodyLength"
		if m.BadChecksum {
			what = "bad CheckSum"
		}
		listed = append(listed, fmt.Sprintf("%s at %s", what, where(m)))
	}
	return Result{name, WARN, fmt.Sprintf("%d message(s) with broken framing, so what they say cannot be trusted: %s", len(broken), strings.Join(listed, ", ")), rule}
}
