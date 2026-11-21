// Package server exposes the radio over REST and WebSocket.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/wardviaene/sweep/data"
	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/radio"
)

// Hub fans spectrum frames out to WebSocket clients. Slow clients drop frames.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	meta    []byte
	m       radio.Meta
	hist    [][]byte // recent frames, oldest first, for /api/spectrum and /api/peaks
}

const histLen = 40

type client struct {
	ch chan wsMsg
}

type wsMsg struct {
	typ  websocket.MessageType
	data []byte
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{clients: map[*client]struct{}{}} }

// Meta broadcasts a meta frame and remembers it for new clients.
func (h *Hub) Meta(m radio.Meta) {
	b, _ := json.Marshal(m)
	h.mu.Lock()
	if h.m.CenterHz != m.CenterHz || h.m.SampleRate != m.SampleRate || h.m.Bins != m.Bins {
		h.hist = nil
	}
	h.meta, h.m = b, m
	h.mu.Unlock()
	h.send(wsMsg{websocket.MessageText, b}, true)
}

// Frame broadcasts one binary spectrum frame.
func (h *Hub) Frame(b []byte) {
	h.mu.Lock()
	if len(h.hist) >= histLen {
		h.hist = append(h.hist[:0], h.hist[1:]...)
	}
	h.hist = append(h.hist, b)
	h.mu.Unlock()
	h.send(wsMsg{websocket.MessageBinary, b}, false)
}

func (h *Hub) send(m wsMsg, important bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.ch <- m:
		default:
			if important {
				// make room for meta: drop the oldest queued frame
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

func (h *Hub) add() (*client, []byte) {
	c := &client{ch: make(chan wsMsg, 8)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	meta := h.meta
	h.mu.Unlock()
	return c, meta
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Handler returns the HTTP handler. static may be nil.
// Deps are the components the HTTP API exposes.
type Deps struct {
	Radio     *radio.Radio
	Hub       *Hub
	Audio     *AudioHub
	Decoders  *decoder.Manager
	Events    *decoder.Events
	Config    *config.Store
	Static    fs.FS  // built web UI, may be nil
	RecordDir string // where POST /api/record writes cu8 files
}

// Handler returns the HTTP handler.
func Handler(d Deps) http.Handler {
	r, hub, audio := d.Radio, d.Hub, d.Audio
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/decoder", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Decoder string `json:"decoder"`
			decoder.Options
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := d.Decoders.Start(body.Decoder, body.Options); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, r.Status())
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, d.Config.Get())
	})
	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, req *http.Request) {
		// Merge: only the fields present in the body change.
		var patch map[string]json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		c := d.Config.Get()
		var err error
		if v, ok := patch["receiver"]; ok {
			c.Receiver = nil
			err = errors.Join(err, json.Unmarshal(v, &c.Receiver))
		}
		if v, ok := patch["meter_ids"]; ok {
			c.MeterIDs = nil
			err = errors.Join(err, json.Unmarshal(v, &c.MeterIDs))
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := d.Config.Set(c); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, d.Config.Get())
	})
	mux.HandleFunc("POST /api/scan", scanHandler(d))
	mux.HandleFunc("POST /api/record", recordHandler(d, d.RecordDir))
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, req *http.Request) {
		since, _ := strconv.ParseInt(req.URL.Query().Get("since"), 10, 64)
		limit, err := qint(req, "limit", 200, 1, 1000)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, d.Events.Since(since, limit))
	})
	mux.HandleFunc("GET /ws/events", func(w http.ResponseWriter, req *http.Request) {
		conn, err := accept(w, req)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := conn.CloseRead(req.Context())
		ch, cancel := d.Events.Subscribe(100)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-ch:
				if err := write(ctx, conn, wsMsg{websocket.MessageText, b}); err != nil {
					return
				}
			}
		}
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, r.Status())
	})
	mux.HandleFunc("POST /api/tune", func(w http.ResponseWriter, req *http.Request) {
		var t radio.TuneRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&t); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		st, err := r.Tune(t)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("POST /api/demod", func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 4096))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		d, err := decodeDemod(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		st, err := r.SetDemod(d)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("GET /ws/audio", audio.serve)
	mux.HandleFunc("GET /api/bands", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data.Bands)
	})
	mux.HandleFunc("GET /api/spectrum", func(w http.ResponseWriter, req *http.Request) {
		snap, err := hub.snapshot(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snap)
	})
	mux.HandleFunc("GET /api/peaks", func(w http.ResponseWriter, req *http.Request) {
		res, err := hub.peaks(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /ws/spectrum", func(w http.ResponseWriter, req *http.Request) {
		serveSpectrum(w, req, hub)
	})
	if d.Static != nil {
		mux.Handle("/", http.FileServerFS(d.Static))
	}
	return mux
}

func serveSpectrum(w http.ResponseWriter, req *http.Request, hub *Hub) {
	conn, err := accept(w, req)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := conn.CloseRead(req.Context()) // we ignore client messages for now

	c, meta := hub.add()
	defer hub.remove(c)
	if meta != nil {
		if err := write(ctx, conn, wsMsg{websocket.MessageText, meta}); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.ch:
			if err := write(ctx, conn, m); err != nil {
				log.Printf("ws: %v", err)
				return
			}
		}
	}
}

// accept upgrades to a WebSocket. The Vite dev proxy keeps the browser's
// Origin (localhost:5173), so local origins are allowed.
func accept(w http.ResponseWriter, req *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, req, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*"},
	})
}

func write(ctx context.Context, conn *websocket.Conn, m wsMsg) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(ctx, m.typ, m.data)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
