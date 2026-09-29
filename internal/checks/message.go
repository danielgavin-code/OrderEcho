// Package checks is the PURE Go port of the emulator's order-chain checks
// (../OrderEchoFixEmulator/orderecho_Timeline.py) and of the parts of its log
// parser (orderecho_LogParse.py) the checks depend on.
//
// It builds an order's chain from any pile of messages -- live ones from the
// agent's order manager, or ones parsed from the agent's or the emulator's FIX
// logs, evidence JSONL, or generic raw FIX lines -- and runs the same eleven
// checks with the same names and PASS/WARN/FAIL semantics. The Python code is
// the reference; parity tests hold the two to the same verdicts. Where Go
// deliberately differs (a Python bug), it is noted at the spot and in the A2
// report.
package checks

import (
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
)

// Directions, as the Python parser names them.
const (
	DirIn      = "in"
	DirOut     = "out"
	DirDisc    = "disc"
	DirUnknown = "unknown"
)

// SOH is the FIX delimiter.
const SOH = "\x01"

// Field is one tag=value pair.
type Field struct {
	Tag   int
	Value string
}

// Message is one FIX message however it was written down (the Python
// ParsedMessage). Raw is normalized to SOH.
type Message struct {
	Raw          string
	Fields       []Field
	TS           *time.Time
	Direction    string
	Session      string
	HasSession   bool
	Injected     bool
	Source       string
	SourceLineNo int
	Comment      string
	BadChecksum  bool
	BadLength    bool
	Index        int
}

// Get returns the first value of tag.
func (m *Message) Get(tag int) (string, bool) {
	for _, f := range m.Fields {
		if f.Tag == tag {
			return f.Value, true
		}
	}
	return "", false
}

// Value is Get without the flag.
func (m *Message) Value(tag int) string { v, _ := m.Get(tag); return v }

// MsgType is 35.
func (m *Message) MsgType() string { return m.Value(35) }

// BeginString is 8.
func (m *Message) BeginString() string { return m.Value(8) }

// Seq is 34 as an int (Python int() semantics); ok=false if absent or junk.
func (m *Message) Seq() (int, bool) {
	v, ok := m.Get(34)
	if !ok {
		return 0, false
	}
	return PyInt(v)
}

// PossDup is 43=Y.
func (m *Message) PossDup() bool { return m.Value(43) == "Y" }

// Suspect is bad framing.
func (m *Message) Suspect() bool { return m.BadChecksum || m.BadLength }

// Pipe renders Raw with '|'.
func (m *Message) Pipe() string { return strings.ReplaceAll(m.Raw, SOH, "|") }

// FromCodec wraps a live, framing-valid message (the order manager's view).
func FromCodec(msg *codec.Message, direction, session string, ts time.Time, index int, injected bool) *Message {
	m := &Message{
		Raw:        string(msg.Raw),
		Direction:  direction,
		Session:    session,
		HasSession: true,
		Injected:   injected,
		Index:      index,
	}
	t := ts
	m.TS = &t
	for _, f := range msg.Fields {
		m.Fields = append(m.Fields, Field{Tag: f.Tag, Value: f.Value})
	}
	return m
}

// SplitFields is split_fields: ordered (tag, value) pairs; repeats kept, a
// chunk without '=' or with a non-integer tag dropped.
func SplitFields(raw string) []Field {
	var out []Field
	for _, chunk := range strings.Split(raw, SOH) {
		if chunk == "" {
			continue
		}
		eq := strings.IndexByte(chunk, '=')
		if eq < 0 {
			continue
		}
		tag, ok := PyInt(chunk[:eq])
		if !ok {
			continue
		}
		out = append(out, Field{Tag: tag, Value: chunk[eq+1:]})
	}
	return out
}

// VerifyFraming is verify_framing: (badLength, badChecksum) for a whole
// SOH-delimited message. It only reports; a broken message is kept.
func VerifyFraming(raw string) (badLength, badChecksum bool) {
	data := []byte(raw)
	first := strings.Index(raw, SOH)
	if first < 0 {
		return true, true
	}
	afterBegin := first + 1
	if !strings.HasPrefix(raw[afterBegin:], "9=") {
		return true, true
	}
	lenEndRel := strings.Index(raw[afterBegin:], SOH)
	if lenEndRel < 0 {
		return true, true
	}
	lengthEnd := afterBegin + lenEndRel
	declared, ok := PyInt(raw[afterBegin+2 : lengthEnd])
	if !ok {
		return true, true
	}
	bodyStart := lengthEnd + 1
	trailer := strings.LastIndex(raw, SOH+"10=")
	if trailer < 0 {
		return true, true
	}
	actual := trailer + 1 - bodyStart
	badLength = actual != declared
	sum := 0
	for _, b := range data[:trailer+1] {
		sum += int(b)
	}
	lo, hi := trailer+4, trailer+7
	if lo > len(raw) {
		return true, true
	}
	if hi > len(raw) {
		hi = len(raw)
	}
	stated, ok := PyInt(raw[lo:hi])
	if !ok {
		return true, true
	}
	badChecksum = sum%256 != stated
	return badLength, badChecksum
}

// Normalize turns the found delimiter into SOH.
func Normalize(raw, delimiter string) string {
	if delimiter == SOH {
		return raw
	}
	return strings.ReplaceAll(raw, delimiter, SOH)
}

// Found is one message located inside a line, with its delimiter.
type Found struct {
	Raw   string
	Delim string
}

// FindMessages locates every FIX message embedded in text, whatever its
// delimiter (SOH, '|', or "^A"), the way the Python MESSAGE_PATTERN does:
//
//	8=FIX(T)?.<digits and dots><delim> ... 10=<1-3 digits><delim>
//
// taking the shortest match, then continuing after it.
//
// Deliberate difference (Python finding): the Python pattern's lazy ".*?10="
// also matches "10=" at the end of a longer tag such as 110= or 210=, which
// cuts the message short and marks it broken. Go only ends a message at a
// "10=" that starts a field (right after a delimiter).
func FindMessages(text string) []Found {
	var out []Found
	pos := 0
	for pos < len(text) {
		start := strings.Index(text[pos:], "8=FIX")
		if start < 0 {
			break
		}
		start += pos
		f, end, ok := matchAt(text, start)
		if !ok {
			pos = start + 1
			continue
		}
		out = append(out, f)
		pos = end
	}
	return out
}

func matchAt(text string, start int) (Found, int, bool) {
	i := start + len("8=FIX")
	if i < len(text) && text[i] == 'T' {
		i++
	}
	if i >= len(text) || text[i] != '.' {
		return Found{}, 0, false
	}
	i++
	// [\d.]+ is greedy, but the regex backtracks if needed; a delimiter can
	// never be a digit or dot, so greedy is exact here.
	j := i
	for j < len(text) && (isDigit(text[j]) || text[j] == '.') {
		j++
	}
	if j == i { // [\d.]+ needs at least one character after "FIX."
		return Found{}, 0, false
	}
	var delim string
	switch {
	case strings.HasPrefix(text[j:], SOH):
		delim = SOH
	case strings.HasPrefix(text[j:], "|") && !strings.HasPrefix(text[j:], "||"):
		delim = "|"
	case strings.HasPrefix(text[j:], "^A"):
		delim = "^A"
	default:
		return Found{}, 0, false
	}
	// Shortest match: the first "<delim>10=\d{1,3}<delim>" at or after the
	// point where the body may start. The delimiter right after BeginString
	// counts as a field start too.
	search := j
	for {
		k := strings.Index(text[search:], delim+"10=")
		if k < 0 {
			return Found{}, 0, false
		}
		k += search
		d := k + len(delim) + 3
		n := 0
		for d+n < len(text) && n < 3 && isDigit(text[d+n]) {
			n++
		}
		// \d{1,3} then the delimiter; with 1..3 digits the regex may stop
		// early, so try each length.
		for l := 1; l <= n; l++ {
			if strings.HasPrefix(text[d+l:], delim) {
				end := d + l + len(delim)
				return Found{Raw: text[start:end], Delim: delim}, end, true
			}
		}
		search = k + len(delim)
	}
}
