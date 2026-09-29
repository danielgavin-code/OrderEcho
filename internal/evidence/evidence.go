// Package evidence writes the machine-readable JSONL record of a run.
//
// The schema is the emulator's, field for field, so its tools read ours:
//
//	{"ts", "run_id", "kind": in|out|discarded|event, "session", "seq",
//	 "msg_type", "raw" (| delimited), "fields" [[tag, value], ...],
//	 "detail", "order", "injected"}
//
// One file per run (data/evidence/<run_id>.jsonl), flushed after every line.
package evidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
)

// Kinds.
const (
	KindIn        = "in"
	KindOut       = "out"
	KindDiscarded = "discarded"
	KindEvent     = "event"
)

// MakeRunID formats a run id: YYYYMMDD-HHMMSS in UTC.
func MakeRunID(t time.Time) string { return t.UTC().Format("20060102-150405") }

// Timestamp formats the ts field: ISO 8601 UTC with milliseconds and Z.
func Timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Record is one evidence line. Field order matches the emulator.
type Record struct {
	TS       string      `json:"ts"`
	RunID    string      `json:"run_id"`
	Kind     string      `json:"kind"`
	Session  string      `json:"session"`
	Seq      *int        `json:"seq"`
	MsgType  *string     `json:"msg_type"`
	Raw      *string     `json:"raw"`
	Fields   [][2]string `json:"fields"`
	Detail   *string     `json:"detail"`
	Order    any         `json:"order"`
	Injected bool        `json:"injected"`
}

// Writer appends records for one run. It never returns errors to callers: a
// failure is reported once on stderr and the run carries on.
type Writer struct {
	mu     sync.Mutex
	clock  clock.Clock
	RunID  string
	Path   string
	file   *os.File
	warned bool
}

// Open creates (or appends to) dir/<runID>.jsonl.
func Open(dir, runID string, clk clock.Clock) *Writer {
	w := &Writer{clock: clk, RunID: runID, Path: filepath.Join(dir, runID+".jsonl")}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.warn(fmt.Sprintf("evidence: cannot create %s: %v", dir, err))
		return w
	}
	f, err := os.OpenFile(w.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		w.warn(fmt.Sprintf("evidence: cannot open %s: %v", w.Path, err))
		return w
	}
	w.file = f
	return w
}

func (w *Writer) warn(msg string) {
	if !w.warned {
		fmt.Fprintln(os.Stderr, msg)
		w.warned = true
	}
}

func strp(s string) *string { return &s }

func (w *Writer) write(r Record) Record {
	r.TS = Timestamp(w.clock.Now())
	r.RunID = w.RunID
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return r
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		w.warn(fmt.Sprintf("evidence: encode failed: %v", err))
		return r
	}
	if _, err := w.file.Write(buf.Bytes()); err != nil {
		w.warn(fmt.Sprintf("evidence: write failed: %v", err))
	}
	return r
}

// Message records an inbound or outbound message.
func (w *Writer) Message(kind, session string, seq int, msg *codec.Message, detail string, injected bool) Record {
	r := Record{Kind: kind, Session: session, Injected: injected}
	if seq > 0 {
		r.Seq = &seq
	} else if n, ok := msg.SeqNum(); ok {
		r.Seq = &n
	}
	if mt, ok := msg.Get(35); ok {
		r.MsgType = strp(mt)
	}
	r.Raw = strp(codec.ToPipe(msg.Raw))
	r.Fields = msg.Pairs()
	if detail != "" {
		r.Detail = strp(detail)
	}
	return w.write(r)
}

// Discarded records a frame that failed framing validation.
func (w *Writer) Discarded(session string, frame *codec.DiscardedFrame) Record {
	return w.write(Record{Kind: KindDiscarded, Session: session,
		Raw: strp(codec.ToPipe(frame.Raw)), Detail: strp(frame.Reason)})
}

// Event records a non-message event. The event name leads the detail, as in
// the emulator ("<event>: <detail>").
func (w *Writer) Event(session, event, detail string, injected bool) Record {
	return w.EventWithOrder(session, event, detail, injected, nil)
}

// EventWithOrder records an event whose "order" field holds an order
// snapshot (nil writes null).
func (w *Writer) EventWithOrder(session, event, detail string, injected bool, order any) Record {
	text := event
	if detail != "" {
		text = event + ": " + detail
	}
	return w.write(Record{Kind: KindEvent, Session: session, Detail: strp(text), Injected: injected, Order: order})
}

// Close closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
