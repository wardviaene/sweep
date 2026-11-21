package server

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/wardviaene/sweep/internal/dsp"
)

// Snapshot is the /api/spectrum payload: recent frames aggregated, in dB.
type Snapshot struct {
	CenterHz   uint32    `json:"center_hz"`
	SampleRate uint32    `json:"sample_rate"`
	StartHz    float64   `json:"start_hz"` // frequency of db[0]
	BinHz      float64   `json:"bin_hz"`
	Frames     int       `json:"frames"`
	Agg        string    `json:"agg"`
	FloorDB    float64   `json:"floor_db"`
	DB         []float64 `json:"db"`
}

// PeaksResult is the /api/peaks payload.
type PeaksResult struct {
	CenterHz   uint32     `json:"center_hz"`
	SampleRate uint32     `json:"sample_rate"`
	Frames     int        `json:"frames"`
	Agg        string     `json:"agg"`
	FloorDB    float64    `json:"floor_db"`
	Peaks      []dsp.Peak `json:"peaks"`
}

func qint(req *http.Request, key string, def, lo, hi int) (int, error) {
	v := req.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be an integer in %d..%d", key, lo, hi)
	}
	return n, nil
}

func qfloat(req *http.Request, key string, def float64) (float64, error) {
	v := req.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	return f, nil
}

// aggregate returns full-resolution dB over the last n frames (avg or max).
func (h *Hub) aggregate(req *http.Request) (db []float64, start, bin float64, frames int, agg string, center, rate uint32, err error) {
	frames, err = qint(req, "frames", 20, 1, histLen)
	if err != nil {
		return
	}
	agg = req.URL.Query().Get("agg")
	if agg == "" {
		agg = "avg"
	}
	if agg != "avg" && agg != "max" {
		err = errors.New("agg must be avg or max")
		return
	}
	h.mu.Lock()
	m := h.m
	hist := h.hist
	if len(hist) > frames {
		hist = hist[len(hist)-frames:]
	}
	hist = append([][]byte(nil), hist...)
	h.mu.Unlock()
	if len(hist) == 0 {
		err = errors.New("no spectrum data yet")
		return
	}
	frames = len(hist)
	k := (m.MaxDB - m.MinDB) / 255
	db = make([]float64, m.Bins)
	for i := range db {
		acc := math.Inf(-1)
		if agg == "avg" {
			acc = 0
		}
		for _, f := range hist {
			v := m.MinDB + float64(f[i])*k
			if agg == "avg" {
				acc += v / float64(len(hist))
			} else if v > acc {
				acc = v
			}
		}
		db[i] = acc
	}
	bin = float64(m.SampleRate) / float64(m.Bins)
	start = float64(m.CenterHz) - float64(m.SampleRate)/2
	return db, start, bin, frames, agg, m.CenterHz, m.SampleRate, nil
}

func (h *Hub) snapshot(req *http.Request) (*Snapshot, error) {
	db, start, bin, frames, agg, center, rate, err := h.aggregate(req)
	if err != nil {
		return nil, err
	}
	bins, err := qint(req, "bins", 256, 16, len(db))
	if err != nil {
		return nil, err
	}
	floor := dsp.Floor(db)
	// max-pool down to the requested bin count so narrow carriers survive
	out := make([]float64, bins)
	per := float64(len(db)) / float64(bins)
	for i := range out {
		lo, hi := int(float64(i)*per), int(float64(i+1)*per)
		mx := math.Inf(-1)
		for _, v := range db[lo:max(hi, lo+1)] {
			mx = max(mx, v)
		}
		out[i] = math.Round(mx*10) / 10
	}
	return &Snapshot{CenterHz: center, SampleRate: rate, StartHz: start + per*bin/2 - bin/2,
		BinHz: per * bin, Frames: frames, Agg: agg, FloorDB: math.Round(floor*10) / 10, DB: out}, nil
}

func (h *Hub) peaks(req *http.Request) (*PeaksResult, error) {
	if req.URL.Query().Get("agg") == "" {
		q := req.URL.Query()
		q.Set("agg", "max") // catch bursty signals by default
		req.URL.RawQuery = q.Encode()
	}
	db, start, bin, frames, agg, center, rate, err := h.aggregate(req)
	if err != nil {
		return nil, err
	}
	n, err := qint(req, "n", 10, 1, 100)
	if err != nil {
		return nil, err
	}
	minSNR, err := qfloat(req, "min_snr", 10)
	if err != nil {
		return nil, err
	}
	ps := dsp.FindPeaks(db, start, bin, n, minSNR, 5, 0.05)
	if ps == nil {
		ps = []dsp.Peak{}
	}
	return &PeaksResult{CenterHz: center, SampleRate: rate, Frames: frames, Agg: agg,
		FloorDB: math.Round(dsp.Floor(db)*10) / 10, Peaks: ps}, nil
}
