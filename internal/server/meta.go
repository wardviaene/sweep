package server

import (
	"net/http"
	"strings"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/meta"
)

// labelPeaks adds the catalog or satellite label to each peak (docs/metadata.md).
func labelPeaks(m *meta.DB, ps []dsp.Peak) {
	for i := range ps {
		ps[i].Label = m.Label(ps[i].FreqHz)
	}
}

// metaRoutes registers the metadata and satellite endpoints. d.Meta may be nil
// (everything is then simply unknown).
func metaRoutes(mux *http.ServeMux, d Deps) {
	m := d.Meta
	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, m.Status())
	})
	mux.HandleFunc("GET /api/meta/aircraft/{hex}", func(w http.ResponseWriter, req *http.Request) {
		hex := strings.ToLower(strings.TrimSpace(req.PathValue("hex")))
		if _, ok := meta.ParseHex(hex); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hex must be a 6-digit Mode S address"})
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Hex string `json:"hex"`
			meta.AircraftInfo
		}{hex, m.Aircraft(hex, "")})
	})
	mux.HandleFunc("GET /api/meta/callsign/{callsign}", func(w http.ResponseWriter, req *http.Request) {
		cs := strings.TrimSpace(req.PathValue("callsign"))
		if cs == "" || len(cs) > 12 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "callsign must be 1-12 characters"})
			return
		}
		writeJSON(w, http.StatusOK, m.Callsign(cs))
	})
	mux.HandleFunc("GET /api/satellites/match", func(w http.ResponseWriter, req *http.Request) {
		f, err := qfloat(req, "freq_hz", 0)
		if err != nil || f < 1e6 || f > 6e9 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "freq_hz must be a frequency in Hz (1e6..6e9)"})
			return
		}
		tol, err := qfloat(req, "tolerance_hz", meta.DefaultToleranceHz)
		if err != nil || tol < 0 || tol > 1e6 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tolerance_hz must be 0..1000000"})
			return
		}
		writeJSON(w, http.StatusOK, m.Match(f, tol))
	})
	mux.HandleFunc("GET /api/satellites/passes", func(w http.ResponseWriter, req *http.Request) {
		hours, err := qint(req, "hours", 24, 1, 72)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		minEl, err := qfloat(req, "min_elevation", 10)
		if err != nil || minEl < 0 || minEl > 90 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "min_elevation must be 0..90"})
			return
		}
		passes, ok := m.Passes(hours, minEl)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "receiver location not set: PUT /api/config {\"receiver\": {...}}"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"passes": passes})
	})
	mux.HandleFunc("GET /api/satellites/overhead", func(w http.ResponseWriter, _ *http.Request) {
		sats, ok := m.Overhead()
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "receiver location not set: PUT /api/config {\"receiver\": {...}}"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"satellites": sats})
	})
}
