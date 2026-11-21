package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wardviaene/sweep/internal/scan"
	"github.com/wardviaene/sweep/internal/source"
)

func scanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var sr scan.Request
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&sr); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !scan.Busy.TryLock() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "another scan, recording or survey is running"})
			return
		}
		defer scan.Busy.Unlock()
		if d.Decoders.Status().Running {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stop the decoder before scanning"})
			return
		}
		res, err := scan.Sweep(req.Context(), d.Radio, d.Hub, sr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		labelPeaks(d.Meta, res.Peaks)
		writeJSON(w, http.StatusOK, res)
	}
}

// RecordRequest is the POST /api/record body.
type RecordRequest struct {
	Seconds float64 `json:"seconds"` // 0.1..60
}

func recordHandler(d Deps, dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var rr RecordRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&rr); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if rr.Seconds < 0.1 || rr.Seconds > 60 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "seconds must be 0.1..60"})
			return
		}
		if !scan.Busy.TryLock() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another scan, recording or survey is running"})
			return
		}
		defer scan.Busy.Unlock()
		st := d.Radio.Status()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		path := filepath.Join(dir, source.RecordingName(time.Now(), st.CenterHz, st.SampleRate))
		f, err := os.Create(path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer f.Close()
		want := int64(rr.Seconds*float64(st.SampleRate)) * 2
		id, iq := d.Radio.AddIQTap()
		defer d.Radio.RemoveTap(id)
		var n int64
		timeout := time.After(time.Duration(rr.Seconds*float64(time.Second)) + 5*time.Second)
	loop:
		for n < want {
			select {
			case b, ok := <-iq:
				if !ok {
					break loop
				}
				b = b[:min(int64(len(b)), want-n)]
				if _, err := f.Write(b); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				n += int64(len(b))
			case <-timeout:
				break loop
			}
		}
		abs, _ := filepath.Abs(path)
		writeJSON(w, http.StatusOK, map[string]any{"path": abs, "bytes": n, "seconds": float64(n/2) / float64(st.SampleRate),
			"center_hz": st.CenterHz, "sample_rate": st.SampleRate})
	}
}
