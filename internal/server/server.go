// Package server exposes the radio over REST and WebSocket.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/wardviaene/sweep/internal/radio"
)

// Hub fans spectrum frames out to WebSocket clients. Slow clients drop frames.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	meta    []byte
}

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
	h.meta = b
	h.mu.Unlock()
	h.send(wsMsg{websocket.MessageText, b}, true)
}

// Frame broadcasts one binary spectrum frame.
func (h *Hub) Frame(b []byte) { h.send(wsMsg{websocket.MessageBinary, b}, false) }

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
func Handler(r *radio.Radio, hub *Hub, static fs.FS) http.Handler {
	mux := http.NewServeMux()
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
	mux.HandleFunc("GET /ws/spectrum", func(w http.ResponseWriter, req *http.Request) {
		serveSpectrum(w, req, hub)
	})
	if static != nil {
		mux.Handle("/", http.FileServerFS(static))
	}
	return mux
}

func serveSpectrum(w http.ResponseWriter, req *http.Request, hub *Hub) {
	// The Vite dev proxy keeps the browser's Origin (localhost:5173), so allow local origins.
	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*"},
	})
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
