// Package decoder runs external decoders (rtl_433, dump1090, direwolf) on the
// radio's IQ or demodulated audio and publishes their output as events.
package decoder

import (
	"encoding/json"
	"sync"
	"time"
)

// Event is one decoded item. See docs/protocol.md.
type Event struct {
	ID      int64   `json:"id"`
	TS      float64 `json:"ts"`
	Decoder string  `json:"decoder"`
	Kind    string  `json:"kind"` // message | snapshot | log
	Data    any     `json:"data"`
}

const ringSize = 1000

// Events is a ring buffer of recent events with live subscribers.
type Events struct {
	mu   sync.Mutex
	ring []Event
	next int64
	subs map[chan []byte]struct{}
}

// NewEvents creates an empty event store.
func NewEvents() *Events { return &Events{subs: map[chan []byte]struct{}{}} }

// Publish stores an event and sends it to subscribers (dropping for slow ones).
func (e *Events) Publish(decoder, kind string, data any) Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.next++
	ev := Event{ID: e.next, TS: float64(time.Now().UnixMicro()) / 1e6, Decoder: decoder, Kind: kind, Data: data}
	if len(e.ring) >= ringSize {
		e.ring = append(e.ring[:0], e.ring[1:]...)
	}
	e.ring = append(e.ring, ev)
	b, _ := json.Marshal(ev)
	for ch := range e.subs {
		select {
		case ch <- b:
		default:
		}
	}
	return ev
}

// Since returns up to limit events with ID > since, oldest first.
func (e *Events) Since(since int64, limit int) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []Event{}
	for _, ev := range e.ring {
		if ev.ID > since {
			out = append(out, ev)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Subscribe returns a channel of JSON-encoded events, preloaded with the last
// `replay` events, and a cancel function.
func (e *Events) Subscribe(replay int) (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	e.mu.Lock()
	start := max(0, len(e.ring)-replay)
	for _, ev := range e.ring[start:] {
		b, _ := json.Marshal(ev)
		ch <- b
	}
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		delete(e.subs, ch)
		e.mu.Unlock()
	}
}

// Count returns the number of events published so far.
func (e *Events) Count() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.next
}
