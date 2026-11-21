package server

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/wardviaene/sweep/internal/radio"
)

// AudioHub fans listening audio out to /ws/audio clients.
type AudioHub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	meta    []byte
}

// NewAudioHub creates an empty audio hub.
func NewAudioHub() *AudioHub {
	return &AudioHub{clients: map[*client]struct{}{}, meta: []byte(`{"type":"audio","rate":0,"mode":"off"}`)}
}

func (h *AudioHub) broadcast(m wsMsg, important bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.ch <- m:
		default:
			if important {
				select {
				case <-c.ch:
				default:
				}
				select {
				case c.ch <- m:
				default:
				}
			}
		}
	}
}

// AudioMeta implements radio.AudioSink.
func (h *AudioHub) AudioMeta(rate int, mode string) {
	b, _ := json.Marshal(map[string]any{"type": "audio", "rate": rate, "mode": mode})
	h.mu.Lock()
	h.meta = b
	h.mu.Unlock()
	h.broadcast(wsMsg{websocket.MessageText, b}, true)
}

// Audio implements radio.AudioSink.
func (h *AudioHub) Audio(pcm []int16) {
	h.mu.Lock()
	n := len(h.clients)
	h.mu.Unlock()
	if n == 0 {
		return
	}
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	h.broadcast(wsMsg{websocket.MessageBinary, b}, false)
}

// Level implements radio.AudioSink.
func (h *AudioHub) Level(db float64, open bool) {
	b := []byte(fmt.Sprintf(`{"type":"level","db":%.1f,"open":%t}`, db, open))
	h.broadcast(wsMsg{websocket.MessageText, b}, false)
}

func (h *AudioHub) serve(w http.ResponseWriter, req *http.Request) {
	conn, err := accept(w, req)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := conn.CloseRead(req.Context())
	c := &client{ch: make(chan wsMsg, 64)} // ~1 s of 20 ms audio frames
	h.mu.Lock()
	h.clients[c] = struct{}{}
	meta := h.meta
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
	}()
	if err := write(ctx, conn, wsMsg{websocket.MessageText, meta}); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.ch:
			if err := write(ctx, conn, m); err != nil {
				return
			}
		}
	}
}

// decodeDemod parses a POST /api/demod body; "squelch_db": null disables squelch.
func decodeDemod(body []byte) (radio.DemodRequest, error) {
	var raw struct {
		Mode        *string         `json:"mode"`
		FreqHz      *uint32         `json:"freq_hz"`
		BandwidthHz *float64        `json:"bandwidth_hz"`
		SquelchDB   json.RawMessage `json:"squelch_db"`
	}
	var req radio.DemodRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return req, err
	}
	req.Mode, req.FreqHz, req.BandwidthHz = raw.Mode, raw.FreqHz, raw.BandwidthHz
	if raw.SquelchDB != nil {
		req.SetSquelch = true
		if string(raw.SquelchDB) != "null" {
			var v float64
			if err := json.Unmarshal(raw.SquelchDB, &v); err != nil {
				return req, fmt.Errorf("squelch_db: %w", err)
			}
			req.SquelchDB = &v
		}
	}
	return req, nil
}
