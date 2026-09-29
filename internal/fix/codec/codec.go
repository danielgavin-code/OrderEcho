// Package codec frames, validates, encodes and displays FIX messages.
//
// Framing is our own (BeginString -> BodyLength -> CheckSum) so BodyLength (9)
// and CheckSum (10) are always verified here, and a frame that fails either
// check is handed back intact as a DiscardedFrame rather than dropped: per the
// FIX spec such a message is discarded, never rejected, but the evidence log
// must still see it.
package codec

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// SOH is the FIX field delimiter.
	SOH = byte(0x01)
	// Pipe is the display delimiter used in logs and evidence.
	Pipe = "|"

	// MaxFrameBytes bounds a frame; anything larger is garbage.
	MaxFrameBytes = 1 << 20

	// TimeLayout is SendingTime / OrigSendingTime: YYYYMMDD-HH:MM:SS.sss UTC.
	TimeLayout = "20060102-15:04:05.000"
)

// frameMarker locates frames by any FIX BeginString, not just our own: a
// counterparty answering in the wrong version must still be decoded so the
// session can report what it said.
var frameMarker = []byte("8=FIX")

// FormatTime renders t in the SendingTime format, UTC, millisecond precision.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

// ToPipe replaces SOH with '|' for display.
func ToPipe(raw []byte) string {
	return strings.ReplaceAll(string(raw), "\x01", Pipe)
}

// ToDelimiter renders raw with the configured delimiter: "SOH" keeps the real
// SOH characters, anything else replaces them.
func ToDelimiter(raw []byte, delimiter string) string {
	if delimiter == "SOH" {
		return string(raw)
	}
	return strings.ReplaceAll(string(raw), "\x01", delimiter)
}

// FromPipe turns a '|'-delimited display string back into wire bytes.
func FromPipe(s string) []byte {
	return []byte(strings.ReplaceAll(s, Pipe, "\x01"))
}

// Field is one tag=value pair.
type Field struct {
	Tag   int
	Value string
}

// F is a short constructor for a Field.
func F(tag int, value string) Field { return Field{Tag: tag, Value: value} }

// Message is a decoded, framing-valid FIX message. Fields keep wire order and
// repeated tags; Raw is the exact bytes received (or sent).
type Message struct {
	Fields []Field
	Raw    []byte
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

// GetNth returns the nth (1-based) value of a repeated tag.
func (m *Message) GetNth(tag, nth int) (string, bool) {
	for _, f := range m.Fields {
		if f.Tag == tag {
			nth--
			if nth == 0 {
				return f.Value, true
			}
		}
	}
	return "", false
}

// Value returns the first value of tag, or "" if absent.
func (m *Message) Value(tag int) string {
	v, _ := m.Get(tag)
	return v
}

// Has reports whether tag is present.
func (m *Message) Has(tag int) bool {
	_, ok := m.Get(tag)
	return ok
}

// MsgType is tag 35, or "".
func (m *Message) MsgType() string { return m.Value(35) }

// SeqNum is MsgSeqNum (34) as an int; ok is false if absent or not a number.
func (m *Message) SeqNum() (int, bool) {
	v, ok := m.Get(34)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Pairs returns the ordered [tag, value] pairs the evidence log records.
func (m *Message) Pairs() [][2]string {
	out := make([][2]string, 0, len(m.Fields))
	for _, f := range m.Fields {
		out = append(out, [2]string{strconv.Itoa(f.Tag), f.Value})
	}
	return out
}

// Pipe renders Raw with '|'.
func (m *Message) Pipe() string { return ToPipe(m.Raw) }

// DiscardedFrame is bytes that could not be accepted as a message, with why.
type DiscardedFrame struct {
	Raw    []byte
	Reason string
}

// Pipe renders Raw with '|'.
func (d *DiscardedFrame) Pipe() string { return ToPipe(d.Raw) }

// Item is what Decode returns: a *Message or a *DiscardedFrame.
type Item interface{ isItem() }

func (*Message) isItem()        {}
func (*DiscardedFrame) isItem() {}

func checksum(data []byte) int {
	sum := 0
	for _, b := range data {
		sum += int(b)
	}
	return sum % 256
}

// ---------------------------------------------------------------- encode

// Header carries the standard header values the encoder writes after 35.
type Header struct {
	SenderCompID    string
	TargetCompID    string
	MsgSeqNum       int
	SendingTime     string // already formatted
	PossDup         bool   // 43=Y
	OrigSendingTime string // 122, written when non-empty
}

// Encode builds a wire message with field order
// 8, 9, 35, 49, 56, 34, 52, [43, 122], body..., 10.
func Encode(beginString, msgType string, h Header, body []Field) []byte {
	fields := make([]Field, 0, len(body)+8)
	fields = append(fields,
		F(35, msgType),
		F(49, h.SenderCompID),
		F(56, h.TargetCompID),
		F(34, strconv.Itoa(h.MsgSeqNum)),
		F(52, h.SendingTime),
	)
	if h.PossDup {
		fields = append(fields, F(43, "Y"))
	}
	if h.OrigSendingTime != "" {
		fields = append(fields, F(122, h.OrigSendingTime))
	}
	fields = append(fields, body...)
	return Build(beginString, fields, false)
}

// Build frames an ordered field list: 8 and 9 are prepended, 10 appended, and
// any 8/9/10 already in fields are dropped and recomputed. With
// corruptChecksum the CheckSum is deliberately wrong.
func Build(beginString string, fields []Field, corruptChecksum bool) []byte {
	var body bytes.Buffer
	for _, f := range fields {
		if f.Tag == 8 || f.Tag == 9 || f.Tag == 10 {
			continue
		}
		body.WriteString(strconv.Itoa(f.Tag))
		body.WriteByte('=')
		body.WriteString(f.Value)
		body.WriteByte(SOH)
	}
	var out bytes.Buffer
	out.WriteString("8=")
	out.WriteString(beginString)
	out.WriteByte(SOH)
	out.WriteString("9=")
	out.WriteString(strconv.Itoa(body.Len()))
	out.WriteByte(SOH)
	out.Write(body.Bytes())
	sum := checksum(out.Bytes())
	if corruptChecksum {
		sum = (sum + 1) % 256
	}
	fmt.Fprintf(&out, "10=%03d", sum)
	out.WriteByte(SOH)
	return out.Bytes()
}

// Rebuild re-frames a decoded message's fields under its own BeginString.
func Rebuild(m *Message) []byte {
	return Build(m.Value(8), m.Fields, false)
}

// ---------------------------------------------------------------- decode

// Decoder is an incremental decoder for one byte stream.
type Decoder struct {
	buf []byte
}

// Buffered returns the bytes not yet framed.
func (d *Decoder) Buffered() []byte { return d.buf }

// Reset drops any buffered bytes.
func (d *Decoder) Reset() { d.buf = nil }

// Decode feeds data and returns zero or more complete items in stream order.
func (d *Decoder) Decode(data []byte) []Item {
	d.buf = append(d.buf, data...)
	var out []Item
	for {
		item, consumed := d.next()
		if consumed == 0 {
			break
		}
		d.buf = append([]byte(nil), d.buf[consumed:]...)
		if item != nil {
			out = append(out, item)
		}
	}
	if len(d.buf) > MaxFrameBytes {
		junk := d.buf
		d.buf = nil
		out = append(out, &DiscardedFrame{Raw: junk, Reason: "Buffer overflow without a complete frame"})
	}
	return out
}

// DecodeOne decodes a single complete frame; it is a convenience for stored or
// fixture messages.
func DecodeOne(raw []byte) (*Message, error) {
	var d Decoder
	items := d.Decode(raw)
	if len(items) != 1 || len(d.buf) != 0 {
		return nil, fmt.Errorf("expected exactly one frame, got %d item(s) and %d leftover byte(s)", len(items), len(d.buf))
	}
	switch it := items[0].(type) {
	case *Message:
		return it, nil
	case *DiscardedFrame:
		return nil, fmt.Errorf("discarded: %s", it.Reason)
	}
	return nil, fmt.Errorf("unexpected item")
}

// next returns (item, consumed); consumed == 0 means "need more bytes".
func (d *Decoder) next() (Item, int) {
	buf := d.buf
	if len(buf) == 0 {
		return nil, 0
	}

	// 1. Line up on a BeginString.
	start := bytes.Index(buf, frameMarker)
	if start == -1 {
		// Keep whatever might still be the head of a BeginString.
		if len(buf) > len(frameMarker) {
			keep := len(frameMarker) - 1
			junk := buf[:len(buf)-keep]
			if len(junk) > 0 {
				return &DiscardedFrame{Raw: clone(junk), Reason: "Leading bytes before BeginString (8)"}, len(junk)
			}
		}
		return nil, 0
	}
	if start > 0 {
		return &DiscardedFrame{Raw: clone(buf[:start]), Reason: "Leading bytes before BeginString (8)"}, start
	}

	// 2. BodyLength must be the second field.
	beginEnd := bytes.IndexByte(buf, SOH)
	if beginEnd == -1 {
		return nil, 0
	}
	lenStart := beginEnd + 1
	if len(buf) < lenStart+2 {
		return nil, 0
	}
	if !bytes.HasPrefix(buf[lenStart:], []byte("9=")) {
		return d.discardToNextFrame("BodyLength (9) is not the second field")
	}
	rel := bytes.IndexByte(buf[lenStart:], SOH)
	if rel == -1 {
		return nil, 0
	}
	lenEnd := lenStart + rel
	bodyLength, err := strconv.Atoi(string(buf[lenStart+2 : lenEnd]))
	if err != nil {
		return d.discardToNextFrame("BodyLength (9) is not a number")
	}
	if bodyLength < 0 || bodyLength > MaxFrameBytes {
		return d.discardToNextFrame(fmt.Sprintf("BodyLength (9) out of range: %d", bodyLength))
	}

	bodyStart := lenEnd + 1
	bodyEnd := bodyStart + bodyLength
	// The CheckSum field is exactly "10=NNN<SOH>": 7 bytes.
	frameEnd := bodyEnd + 7
	trailer := []byte("\x0110=")
	if len(buf) < frameEnd {
		// Might just be a short read -- unless the trailer has clearly already
		// gone by, which means BodyLength lied.
		limit := bodyEnd
		if limit > len(buf) {
			limit = len(buf)
		}
		early := indexFrom(buf[:limit], trailer, bodyStart)
		if early == -1 {
			return nil, 0
		}
		actual := early + 1 - bodyStart
		return d.discardBadBodyLength(bodyLength, actual, early+1+7)
	}

	if !bytes.Equal(buf[bodyEnd:bodyEnd+3], []byte("10=")) || buf[frameEnd-1] != SOH {
		found := indexFrom(buf, trailer, bodyStart)
		if found == -1 {
			return d.discardToNextFrame(fmt.Sprintf("Bad BodyLength (9): declared %d, no CheckSum found", bodyLength))
		}
		actual := found + 1 - bodyStart
		return d.discardBadBodyLength(bodyLength, actual, found+1+7)
	}

	frame := clone(buf[:frameEnd])
	declared := string(frame[bodyEnd+3 : frameEnd-1])
	computed := checksum(frame[:bodyEnd])
	declaredValue, err := strconv.Atoi(declared)
	if err != nil {
		return &DiscardedFrame{Raw: frame, Reason: fmt.Sprintf("Bad CheckSum (10): %q is not a number", declared)}, frameEnd
	}
	if declaredValue != computed {
		return &DiscardedFrame{Raw: frame, Reason: fmt.Sprintf("Bad CheckSum (10): declared %s, computed %03d", declared, computed)}, frameEnd
	}
	return parse(frame), frameEnd
}

func (d *Decoder) discardBadBodyLength(declared, actual, frameEnd int) (Item, int) {
	if len(d.buf) < frameEnd {
		return nil, 0
	}
	return &DiscardedFrame{
		Raw:    clone(d.buf[:frameEnd]),
		Reason: fmt.Sprintf("Bad BodyLength (9): declared %d, computed %d", declared, actual),
	}, frameEnd
}

func (d *Decoder) discardToNextFrame(reason string) (Item, int) {
	nxt := indexFrom(d.buf, frameMarker, 1)
	if nxt == -1 {
		if len(d.buf) > MaxFrameBytes {
			return &DiscardedFrame{Raw: clone(d.buf), Reason: reason}, len(d.buf)
		}
		return nil, 0
	}
	return &DiscardedFrame{Raw: clone(d.buf[:nxt]), Reason: reason}, nxt
}

func indexFrom(buf, sep []byte, from int) int {
	if from > len(buf) {
		return -1
	}
	i := bytes.Index(buf[from:], sep)
	if i == -1 {
		return -1
	}
	return from + i
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// parse splits a framing-valid frame into ordered fields. A chunk without '='
// or with a non-numeric tag is skipped; framing has already been verified.
func parse(frame []byte) *Message {
	msg := &Message{Raw: frame}
	for _, chunk := range bytes.Split(frame, []byte{SOH}) {
		if len(chunk) == 0 {
			continue
		}
		eq := bytes.IndexByte(chunk, '=')
		if eq <= 0 {
			continue
		}
		tag, err := strconv.Atoi(string(chunk[:eq]))
		if err != nil {
			continue
		}
		msg.Fields = append(msg.Fields, Field{Tag: tag, Value: string(chunk[eq+1:])})
	}
	return msg
}
