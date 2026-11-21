// Package scan sweeps the radio across a frequency range and lists the carriers
// it finds. It backs POST /api/scan and the survey's scan steps.
package scan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
)

// Busy serialises long-running radio operations (scan, record, survey).
var Busy sync.Mutex

// Spectrum aggregates the most recent spectrum frames (agg is "avg" or "max").
type Spectrum interface {
	Aggregate(frames int, agg string) (db []float64, startHz, binHz float64, err error)
}

// Request is the POST /api/scan body.
type Request struct {
	StartHz uint32  `json:"start_hz"`
	StopHz  uint32  `json:"stop_hz"`
	DwellMS int     `json:"dwell_ms"` // per step, default 500
	MinSNR  float64 `json:"min_snr"`  // default 10
	Max     int     `json:"max"`      // max peaks returned, default 50
}

// Step is the floor seen at one center frequency.
type Step struct {
	CenterHz uint32  `json:"center_hz"`
	FloorDB  float64 `json:"floor_db"`
}

// Result is the POST /api/scan response.
type Result struct {
	StartHz   uint32     `json:"start_hz"`
	StopHz    uint32     `json:"stop_hz"`
	Steps     []Step     `json:"steps"`
	Peaks     []dsp.Peak `json:"peaks"`
	DurationS float64    `json:"duration_s"`
}

// StepHz is the span one scan step covers at the given sample rate.
func StepHz(sampleRate uint32) uint32 { return uint32(float64(sampleRate) * 0.8) }

// Sweep steps the center across [start, stop], holding max over each dwell,
// and returns the strongest peaks. The original center frequency is restored.
// The caller must hold Busy and make sure no decoder is running.
func Sweep(ctx context.Context, r *radio.Radio, spec Spectrum, sr Request) (*Result, error) {
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
	st := r.Status()
	step := StepHz(st.SampleRate)
	steps := int(math.Ceil(float64(sr.StopHz-sr.StartHz) / float64(step)))
	if steps > 400 {
		return nil, fmt.Errorf("%d steps: narrow the range (max 400 steps of %.1f MHz)", steps, float64(step)/1e6)
	}
	orig := st.CenterHz
	defer r.Tune(radio.TuneRequest{FreqHz: &orig})

	t0 := time.Now()
	res := &Result{StartHz: sr.StartHz, StopHz: sr.StopHz}
	var all []dsp.Peak
	frames := max(1, sr.DwellMS*st.FPS/1000)
	for c := sr.StartHz + step/2; c-step/2 < sr.StopHz; c += step {
		center := c
		if _, err := r.Tune(radio.TuneRequest{FreqHz: &center}); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(sr.DwellMS)*time.Millisecond + 100*time.Millisecond):
		}
		db, start, bin, err := spec.Aggregate(frames, "max")
		if err != nil {
			return nil, err
		}
		floor := dsp.Floor(db)
		res.Steps = append(res.Steps, Step{CenterHz: center, FloorDB: math.Round(floor*10) / 10})
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
