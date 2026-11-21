package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/wardviaene/sweep/internal/autopilot"
)

func autopilotRoutes(mux *http.ServeMux, d Deps) {
	if d.Autopilot == nil {
		return
	}
	ap := d.Autopilot
	mux.HandleFunc("GET /api/autopilot", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, ap.Snapshot())
	})
	mux.HandleFunc("PUT /api/autopilot", func(w http.ResponseWriter, req *http.Request) {
		// Merge: only the fields present in the body change.
		var patch map[string]json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		c := d.Config.Get()
		s := c.AutopilotSettings()
		var err error
		for k, v := range patch {
			switch k {
			case "enabled":
				err = errors.Join(err, json.Unmarshal(v, &s.Enabled))
			case "min_elevation":
				var f float64
				if e := json.Unmarshal(v, &f); e != nil {
					err = errors.Join(err, fmt.Errorf("min_elevation: %w", e))
				} else {
					s.MinElevation = &f
				}
			case "satellites":
				s.Satellites = nil
				err = errors.Join(err, json.Unmarshal(v, &s.Satellites))
			case "preempt":
				err = errors.Join(err, json.Unmarshal(v, &s.Preempt))
			case "listen":
				var b bool
				if e := json.Unmarshal(v, &b); e != nil {
					err = errors.Join(err, fmt.Errorf("listen: %w", e))
				} else {
					s.Listen = &b
				}
			case "record_iq":
				err = errors.Join(err, json.Unmarshal(v, &s.RecordIQ))
			}
		}
		if err == nil {
			c.Autopilot = &s
			err = d.Config.Set(c)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ap.Kick()
		writeJSON(w, http.StatusOK, ap.Snapshot())
	})
	mux.HandleFunc("POST /api/autopilot/run", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Norad int `json:"norad"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&body); err != nil || body.Norad <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "norad is required"})
			return
		}
		cur, err := ap.Run(body.Norad)
		switch {
		case errors.Is(err, autopilot.ErrBusy):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusAccepted, cur)
		}
	})
	mux.HandleFunc("POST /api/autopilot/stop", func(w http.ResponseWriter, _ *http.Request) {
		ap.Stop()
		writeJSON(w, http.StatusOK, ap.Snapshot())
	})
}
