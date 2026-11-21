package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/source"
)

// busy serialises long-running operations (scan, record).
var busy sync.Mutex

// ScanRequest is the POST /api/scan body.
type ScanRequest struct {
	StartHz uint32  `json:"start_hz"`
	StopHz  uint32  `json:"stop_hz"`
	DwellMS int     `json:"dwell_ms"` // per step, default 500
	MinSNR  float64 `json:"min_snr"`  // default 10
	Max     int     `json:"max"`      // max peaks returned, default 50
}

// ScanStep is the floor seen at one center frequency.
type ScanStep struct {
	CenterHz uint32  `json:"center_hz"`
	FloorDB  float64 `json:"floor_db"`
}

// ScanResult is the POST /api/scan response.
type ScanResult struct {
	StartHz   uint32     `json:"start_hz"`
	StopHz    uint32     `json:"stop_hz"`
	Steps     []ScanStep `json:"steps"`
	Peaks     []dsp.Peak `json:"peaks"`
	DurationS float64    `json:"duration_s"`
}

func scanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var sr ScanRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&sr); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		res, err := scan(d, sr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// scan steps the center across [start, stop], holding max over each dwell,
// and returns the strongest peaks. The original tuning is restored.
func scan(d Deps, sr ScanRequest) (*ScanResult, error) {
	if sr.StopHz <= sr.StartHz {
		return nil, errors.New("stop_hz must be above start_hz")
	}
	if sr.DwellMS == 0 {
		sr.DwellMS = 500
	}
	if sr.DwellMS < 100 || sr.DwellMS > 5000 {
		return nil, errors.New("dwell_ms must be 100..5000")
	}
	if sr.MinSNR == 0 {
		sr.MinSNR = 10
	}
	if sr.Max == 0 {
		sr.Max = 50
	}
	if d.Decoders.Status().Running {
		return nil, errors.New("stop the decoder before scanning")
	}
	if !busy.TryLock() {
		return nil, errors.New("another scan or recording is running")
	}
	defer busy.Unlock()

	st := d.Radio.Status()
	step := uint32(float64(st.SampleRate) * 0.8)
	steps := int(math.Ceil(float64(sr.StopHz-sr.StartHz) / float64(step)))
	if steps > 400 {
		return nil, fmt.Errorf("%d steps: narrow the range (max 400 steps of %.1f MHz)", steps, float64(step)/1e6)
	}
	orig := st.CenterHz
	defer d.Radio.Tune(radio.TuneRequest{FreqHz: &orig})

	t0 := time.Now()
	res := &ScanResult{StartHz: sr.StartHz, StopHz: sr.StopHz}
	var all []dsp.Peak
	frames := max(1, sr.DwellMS*st.FPS/1000)
	for c := sr.StartHz + step/2; c-step/2 < sr.StopHz; c += step {
		center := c
		if _, err := d.Radio.Tune(radio.TuneRequest{FreqHz: &center}); err != nil {
			return nil, err
		}
		time.Sleep(time.Duration(sr.DwellMS)*time.Millisecond + 100*time.Millisecond)
		db, start, bin, _, _, _, _, err := d.Hub.aggregateN(frames, "max")
		if err != nil {
			return nil, err
		}
		floor := dsp.Floor(db)
		res.Steps = append(res.Steps, ScanStep{CenterHz: center, FloorDB: math.Round(floor*10) / 10})
		for _, p := range dsp.FindPeaks(db, start, bin, 20, sr.MinSNR, 5, 0.1) {
			if p.FreqHz >= float64(sr.StartHz) && p.FreqHz <= float64(sr.StopHz) {
				all = append(all, p)
			}
		}
	}
	// merge duplicates from overlapping steps (within 5 kHz), keep the strongest
	sort.Slice(all, func(i, j int) bool { return all[i].SNR > all[j].SNR })
	for _, p := range all {
		dup := false
		for _, q := range res.Peaks {
			if math.Abs(p.FreqHz-q.FreqHz) < 5000 {
				dup = true
				break
			}
		}
		if !dup {
			res.Peaks = append(res.Peaks, p)
		}
		if len(res.Peaks) >= sr.Max {
			break
		}
	}
	if res.Peaks == nil {
		res.Peaks = []dsp.Peak{}
	}
	res.DurationS = math.Round(time.Since(t0).Seconds()*10) / 10
	return res, nil
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
		if !busy.TryLock() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another scan or recording is running"})
			return
		}
		defer busy.Unlock()
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
