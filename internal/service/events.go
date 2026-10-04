package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// A5 §5: live updates over Server-Sent Events (GET /api/v1/events).
//
// Every event gets an increasing id. The hub keeps the last ringEvents
// events, so a client that reconnects with Last-Event-ID (EventSource does
// this on its own) gets what it missed, in order; if what it missed is gone,
// it gets a "reset" event first (reload your view). Each client has a
// bounded buffer: when a slow client falls behind, the oldest events are
// dropped and the client is told how many with a "dropped" event.

// Event types.
const (
	EvSession = "session" // a session's state changed
	EvMessage = "message" // a FIX message went in or out (or a frame was discarded)
	EvOrder   = "order"   // an order report, with its check status
	EvCert    = "cert"    // cert run progress
	EvDropped = "dropped" // this client missed events (its buffer overflowed)
	EvReset   = "reset"   // the client's Last-Event-ID is older than the hub remembers
)

// Event is one SSE event.
type Event struct {
	ID   uint64          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

const (
	ringEvents       = 2000
	defaultClientBuf = 512
	pingEvery        = 15 * time.Second
)

type hub struct {
	mu     sync.Mutex
	next   uint64
	ring   []Event
	subs   map[*subscriber]struct{}
	bufMax int
}

type subscriber struct {
	mu      sync.Mutex
	buf     []Event
	max     int
	dropped int
	wake    chan struct{}
}

func newHub() *hub { return &hub{subs: map[*subscriber]struct{}{}, bufMax: defaultClientBuf} }

func (s *subscriber) push(ev Event) {
	s.mu.Lock()
	if len(s.buf) >= s.max {
		s.buf = s.buf[1:]
		s.dropped++
	}
	s.buf = append(s.buf, ev)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *subscriber) take() ([]Event, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evs, d := s.buf, s.dropped
	s.buf, s.dropped = nil, 0
	return evs, d
}

// publish sends an event to every client. It never blocks (callers include
// the FIX session's callbacks).
func (h *hub) publish(typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.next++
	ev := Event{ID: h.next, Type: typ, Data: raw}
	h.ring = append(h.ring, ev)
	if len(h.ring) > ringEvents {
		h.ring = append([]Event(nil), h.ring[len(h.ring)-ringEvents:]...)
	}
	subs := make([]*subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	for _, s := range subs {
		s.push(ev)
	}
}

// subscribe registers a client; after > 0 replays what it missed.
func (h *hub) subscribe(after uint64) (*subscriber, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &subscriber{max: h.bufMax, wake: make(chan struct{}, 1)}
	reset := false
	if after > 0 && after < h.next {
		if len(h.ring) == 0 || h.ring[0].ID > after+1 {
			reset = true
		} else {
			for _, ev := range h.ring {
				if ev.ID > after {
					s.buf = append(s.buf, ev)
				}
			}
			if len(s.buf) > s.max {
				s.dropped = len(s.buf) - s.max
				s.buf = s.buf[len(s.buf)-s.max:]
			}
		}
	}
	h.subs[s] = struct{}{}
	return s, reset
}

func (h *hub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

func (h *hub) lastID() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.next
}

func writeSSE(w http.ResponseWriter, id uint64, typ string, data []byte) error {
	var err error
	if id > 0 {
		_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, typ, data)
	} else {
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, data)
	}
	return err
}

// serveEvents is GET /api/v1/events.
func (s *Service) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, apiErr(500, "no_streaming", "", "streaming not supported"))
		return
	}
	var after uint64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		after, _ = strconv.ParseUint(v, 10, 64)
	} else if v := r.URL.Query().Get("after"); v != "" {
		after, _ = strconv.ParseUint(v, 10, 64)
	}
	sub, reset := s.events.subscribe(after)
	defer s.events.unsubscribe(sub)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: 2000\n: OrderEcho %s events, last id %d\n\n", s.cfg.Path, s.events.lastID())
	if reset {
		writeSSE(w, 0, EvReset, []byte(`{"reason":"events since your last id are no longer kept; reload"}`))
	}
	fl.Flush()
	ctx := r.Context()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		evs, dropped := sub.take()
		if dropped > 0 {
			b, _ := json.Marshal(map[string]any{"dropped": dropped, "reason": "this client fell behind; the oldest events were dropped"})
			if writeSSE(w, 0, EvDropped, b) != nil {
				return
			}
		}
		for _, ev := range evs {
			if writeSSE(w, ev.ID, ev.Type, ev.Data) != nil {
				return
			}
		}
		if len(evs) > 0 || dropped > 0 {
			fl.Flush()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-sub.wake:
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// watchSessions publishes a session event whenever a session's state (or
// its cert-run ownership) changes. Polling keeps it off the FIX session's
// locks.
func (s *Service) watchSessions(ctx context.Context) {
	last := map[string]string{}
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		for _, id := range s.order {
			info := s.sessionInfo(s.sessions[id])
			key := info.State + "|" + info.CertRun
			if last[id] != key {
				last[id] = key
				s.events.publish(EvSession, info)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
