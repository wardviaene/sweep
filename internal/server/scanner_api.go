package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wardviaene/sweep/internal/scanner"
)

// autopilotBusy answers 409 and reports true while an autopilot pass owns the radio.
func autopilotBusy(w http.ResponseWriter, d Deps) bool {
	if d.Autopilot != nil && d.Autopilot.Active() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "autopilot pass in progress"})
		return true
	}
	return false
}

// scannerBusy answers 409 and reports true while the scanner (or an autopilot
// pass) owns the radio.
func scannerBusy(w http.ResponseWriter, d Deps) bool {
	if autopilotBusy(w, d) {
		return true
	}
	if d.Scanner != nil && d.Scanner.Active() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "scanner running"})
		return true
	}
	return false
}

func scannerRoutes(mux *http.ServeMux, d Deps) {
	if d.Scanner == nil {
		return
	}
	sc := d.Scanner
	mux.HandleFunc("POST /api/scanner", func(w http.ResponseWriter, req *http.Request) {
		var sr scanner.Request
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16384)).Decode(&sr); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if autopilotBusy(w, d) {
			return
		}
		st, err := sc.Start(sr)
		switch {
		case errors.Is(err, scanner.ErrBusy):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusAccepted, st)
		}
	})
	mux.HandleFunc("GET /api/scanner", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, sc.Snapshot())
	})
	mux.HandleFunc("POST /api/scanner/stop", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, sc.Stop())
	})
	mux.HandleFunc("POST /api/scanner/lockout", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			FreqHz uint32 `json:"freq_hz"`
			Remove bool   `json:"remove"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		st, err := sc.Lockout(body.FreqHz, body.Remove)
		switch {
		case errors.Is(err, scanner.ErrNotRunning):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusOK, st)
		}
	})
}
