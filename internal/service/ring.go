package service

import (
	"sync"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
)

// ring keeps a session's most recent wire messages, across connections.
type ring struct {
	mu    sync.Mutex
	max   int
	items []wireMsg
}

type wireMsg struct {
	TS  time.Time
	Dir string // in | out
	Msg *codec.Message
}

func newRing(max int) *ring { return &ring{max: max} }

func (r *ring) add(dir string, m *codec.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, wireMsg{TS: time.Now(), Dir: dir, Msg: m})
	if len(r.items) > r.max {
		r.items = append([]wireMsg(nil), r.items[len(r.items)-r.max:]...)
	}
}

// snapshot returns every kept message, oldest first.
func (r *ring) snapshot() []wireMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wireMsg(nil), r.items...)
}
