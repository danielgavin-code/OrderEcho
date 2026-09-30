package checks

import (
	"sort"
	"strings"
)

// Statuses, and their rank for the verdict (worst wins).
const (
	PASS = "PASS"
	WARN = "WARN"
	FAIL = "FAIL"
)

var rank = map[string]int{PASS: 0, WARN: 1, FAIL: 2}

// Tags the checks read.
const (
	TagAvgPx         = 6
	TagClOrdID       = 11
	TagCumQty        = 14
	TagExecID        = 17
	TagExecTransType = 20
	TagLastPx        = 31
	TagLastQty       = 32
	TagOrderID       = 37
	TagOrderQty      = 38
	TagOrdStatus     = 39
	TagOrigClOrdID   = 41
	TagRefSeqNum     = 45
	TagSenderCompID  = 49
	TagText          = 58
	TagExecType      = 150
	TagLeavesQty     = 151
)

// Message types.
const (
	MsgExecutionReport = "8"
	MsgCancelReject    = "9"
	MsgReject          = "3"
	MsgBusinessReject  = "j"
)

var (
	requestTypes  = map[string]bool{"D": true, "F": true, "G": true}
	responseTypes = map[string]bool{"8": true, "9": true, "3": true, "j": true}
	// WorkingStates are OrdStatus values where the order is still alive.
	WorkingStates = map[string]bool{"0": true, "1": true, "6": true, "E": true, "A": true}
	// TerminalStates are OrdStatus values where it is over.
	TerminalStates = map[string]bool{"2": true, "4": true, "8": true, "C": true, "3": true}
	// FillExecTypes are ExecType values that report a fill.
	FillExecTypes = map[string]bool{"1": true, "2": true, "F": true}
	noOrderID     = map[string]bool{"NONE": true, "": true, "0": true, "UNKNOWN": true}
)

// Result is one check's outcome.
type Result struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Explanation string `json:"explanation"`
	Rule        string `json:"rule"`
}

// Step is one message of a chain; Replay marks a PossDup copy of something
// already seen.
type Step struct {
	Message *Message
	Replay  bool
}

// Chain is every message belonging to one order.
type Chain struct {
	Seed     string
	Steps    []Step
	IDs      map[string]bool
	OrderIDs map[string]bool
	Checks   []Result
}

// Messages are the chain's messages in order.
func (c *Chain) Messages() []*Message {
	out := make([]*Message, 0, len(c.Steps))
	for _, s := range c.Steps {
		out = append(out, s.Message)
	}
	return out
}

// Reports are the chain's ExecutionReports, replays excluded.
func (c *Chain) Reports() []*Message {
	var out []*Message
	for _, s := range c.Steps {
		if s.Message.MsgType() == MsgExecutionReport && !s.Replay {
			out = append(out, s.Message)
		}
	}
	return out
}

// Verdict is the worst check status (PASS for no checks).
func (c *Chain) Verdict() string { return Worst(c.Checks) }

// Worst returns the worst status among results.
func Worst(results []Result) string {
	v := PASS
	for _, r := range results {
		if rank[r.Status] > rank[v] {
			v = r.Status
		}
	}
	return v
}

// ExitCode is 0 PASS, 1 WARN, 2 FAIL, as the Python viewer.
func (c *Chain) ExitCode() int { return rank[c.Verdict()] }

// SortedIDs returns the chain's ClOrdIDs, sorted.
func (c *Chain) SortedIDs() []string { return sortedKeys(c.IDs) }

// SortedOrderIDs returns the chain's OrderIDs, sorted.
func (c *Chain) SortedOrderIDs() []string { return sortedKeys(c.OrderIDs) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func identityIDs(m *Message) []string {
	var ids []string
	for _, tag := range []int{TagClOrdID, TagOrigClOrdID} {
		if v, ok := m.Get(tag); ok && v != "" {
			ids = append(ids, v)
		}
	}
	return ids
}

func orderIDOf(m *Message) (string, bool) {
	v, ok := m.Get(TagOrderID)
	if ok && v != "" && !noOrderID[strings.ToUpper(v)] {
		return v, true
	}
	return "", false
}

// BuildChain is build_chain: start from a ClOrdID or an OrderID, follow 41
// links in both directions and shared 37s until nothing new joins, pull in
// session/business Rejects that answer a request in the chain, mark replays,
// and run the checks.
func BuildChain(messages []*Message, clOrdID, orderID string) *Chain {
	wantedIDs := map[string]bool{}
	if clOrdID != "" {
		wantedIDs[clOrdID] = true
	}
	wantedOrders := map[string]bool{}
	if orderID != "" {
		wantedOrders[orderID] = true
	}
	chosen := map[int]bool{}
	for changed := true; changed; {
		changed = false
		for i, m := range messages {
			if chosen[i] {
				continue
			}
			ids := identityIDs(m)
			own, hasOwn := orderIDOf(m)
			match := hasOwn && wantedOrders[own]
			for _, id := range ids {
				if wantedIDs[id] {
					match = true
				}
			}
			if !match {
				continue
			}
			chosen[i] = true
			for _, id := range ids {
				if !wantedIDs[id] {
					wantedIDs[id] = true
					changed = true
				}
			}
			if hasOwn && !wantedOrders[own] {
				wantedOrders[own] = true
				changed = true
			}
		}
	}
	addSessionRejects(messages, chosen)

	idx := make([]int, 0, len(chosen))
	for i := range chosen {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	ordered := make([]*Message, 0, len(idx))
	for _, i := range idx {
		ordered = append(ordered, messages[i])
	}
	seed := clOrdID
	if seed == "" {
		seed = orderID
	}
	if kept, split := splitDuplicates(ordered, orderID); split {
		ordered = kept
		wantedIDs, wantedOrders = map[string]bool{}, map[string]bool{}
		if clOrdID != "" {
			wantedIDs[clOrdID] = true
		}
		for _, m := range ordered {
			for _, id := range identityIDs(m) {
				wantedIDs[id] = true
			}
			if own, ok := orderIDOf(m); ok {
				wantedOrders[own] = true
			}
		}
		if orderID != "" {
			wantedOrders[orderID] = true
		}
	}
	c := &Chain{Seed: seed, Steps: markReplays(ordered), IDs: wantedIDs, OrderIDs: wantedOrders}
	c.Checks = RunChecks(c)
	return c
}

type seqKey struct {
	session    string
	hasSession bool
	seq        int
}

// sentBySameSide reports whether reject and request were sent by the same
// CompID, in which case the reject cannot be the answer to the request.
//
// Deliberate difference (Python finding): the Python chain builder matches a
// Reject to a request by (session, RefSeqNum) alone. In one side's log both
// directions share that key space, so a Reject *we* sent about the
// counterparty's message N "answers" our own request that happened to be
// sequence N too. Go additionally requires that the Reject did not come from
// the request's sender (when both carry a SenderCompID).
func sentBySameSide(reject, request *Message) bool {
	a, okA := reject.Get(TagSenderCompID)
	b, okB := request.Get(TagSenderCompID)
	return okA && okB && a != "" && a == b
}

func addSessionRejects(messages []*Message, chosen map[int]bool) {
	wanted := map[seqKey]*Message{}
	for i := range chosen {
		m := messages[i]
		if !requestTypes[m.MsgType()] {
			continue
		}
		if seq, ok := m.Seq(); ok {
			k := seqKey{m.Session, m.HasSession, seq}
			if prev, dup := wanted[k]; !dup || m.Index < prev.Index {
				wanted[k] = m // setdefault in message order
			}
		}
	}
	if len(wanted) == 0 {
		return
	}
	for i, m := range messages {
		if chosen[i] {
			continue
		}
		if mt := m.MsgType(); mt != MsgReject && mt != MsgBusinessReject {
			continue
		}
		ref, ok := intOfDec(m.Get(TagRefSeqNum))
		if !ok {
			continue
		}
		req, found := wanted[seqKey{m.Session, m.HasSession, ref}]
		if !found {
			req, found = wanted[seqKey{"", false, ref}]
		}
		if found && !sentBySameSide(m, req) {
			chosen[i] = true
		}
	}
}

type replayKey struct {
	msgType string
	seq     int
	hasSeq  bool
}

func markReplays(messages []*Message) []Step {
	steps := make([]Step, 0, len(messages))
	seenExec := map[string]bool{}
	seenKeys := map[replayKey]bool{}
	for _, m := range messages {
		replay := false
		execID := m.Value(TagExecID)
		seq, hasSeq := m.Seq()
		key := replayKey{m.MsgType(), seq, hasSeq}
		if m.PossDup() {
			if execID != "" && seenExec[execID] {
				replay = true
			} else if execID == "" && seenKeys[key] {
				replay = true
			}
		}
		if execID != "" {
			seenExec[execID] = true
		}
		seenKeys[key] = true
		steps = append(steps, Step{Message: m, Replay: replay})
	}
	return steps
}

// splitDuplicates separates the answer to a duplicate request from the order
// whose ClOrdID it reused (A3 3.2). The chain's established OrderID is the
// first one its reports carry. A later request that reuses a ClOrdID already
// used by an earlier request of the chain is a duplicate; an ExecutionReport
// rejecting it (39=8) on a *different* OrderID, and any 35=3/35=j answering
// it by RefSeqNum, form their own chain together with the duplicate request.
//
// It returns the messages to keep for this chain and whether anything was
// split off. Seeded with the duplicate's OrderID it returns the duplicate's
// chain; otherwise the original's.
//
// Deliberate difference (to be fixed on the Python side later): the Python
// chain builder keeps them together, so the original order FAILs
// order_id_constant.
func splitDuplicates(ordered []*Message, seedOrderID string) ([]*Message, bool) {
	primary := ""
	for _, m := range ordered {
		if mt := m.MsgType(); mt == MsgExecutionReport || mt == MsgCancelReject {
			if id, ok := orderIDOf(m); ok {
				primary = id
				break
			}
		}
	}
	if primary == "" {
		return ordered, false
	}
	used := map[string]int{}
	var dupReqs []*Message
	for _, m := range ordered {
		if !requestTypes[m.MsgType()] || m.PossDup() {
			continue
		}
		id := m.Value(TagClOrdID)
		if id == "" {
			continue
		}
		if used[id] > 0 {
			dupReqs = append(dupReqs, m)
		}
		used[id]++
	}
	if len(dupReqs) == 0 {
		return ordered, false
	}
	split := map[*Message]bool{}
	splitOrders := map[string]bool{}
	for i, m := range ordered {
		if m.MsgType() != MsgExecutionReport || m.Value(TagOrdStatus) != "8" {
			continue
		}
		own, ok := orderIDOf(m)
		if !ok || own == primary {
			continue
		}
		// The latest duplicate request with this ClOrdID sent before it.
		var req *Message
		for _, d := range dupReqs {
			if d.Value(TagClOrdID) == m.Value(TagClOrdID) && d.Index < m.Index && indexOf(ordered, d) < i {
				req = d
			}
		}
		if req == nil {
			continue
		}
		split[m], split[req] = true, true
		splitOrders[own] = true
		if seq, ok := req.Seq(); ok {
			for _, r := range ordered {
				if mt := r.MsgType(); mt == MsgReject || mt == MsgBusinessReject {
					if ref, ok := intOfDec(r.Get(TagRefSeqNum)); ok && ref == seq && !sentBySameSide(r, req) {
						split[r] = true
					}
				}
			}
		}
	}
	if len(split) == 0 {
		return ordered, false
	}
	wantSplit := seedOrderID != "" && splitOrders[seedOrderID]
	var out []*Message
	for _, m := range ordered {
		if split[m] == wantSplit {
			out = append(out, m)
		}
	}
	return out, true
}

func indexOf(list []*Message, m *Message) int {
	for i, x := range list {
		if x == m {
			return i
		}
	}
	return -1
}
