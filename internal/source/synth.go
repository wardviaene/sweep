// Package source provides IQ sources: rtl_tcp (real hardware) and a synthetic generator.
package source

import (
	"context"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// Tone is a synthetic carrier at an absolute frequency.
type Tone struct {
	FreqHz float64
	Amp    float64 // linear, full scale = 1
}

// Synth generates cu8 IQ in real time: noise plus fixed carriers at absolute
// frequencies, so retuning moves them across the spectrum like a real radio.
type Synth struct {
	mu         sync.Mutex
	centerHz   float64
	sampleRate float64
	tones      []Tone
	phase      []float64
	noise      float64
	rng        *rand.Rand

	buf     []byte
	start   time.Time
	emitted int64
}

// NewSynth creates a generator with a few default carriers around 433.92 MHz.
func NewSynth(centerHz, sampleRate uint32) *Synth {
	s := &Synth{
		centerHz:   float64(centerHz),
		sampleRate: float64(sampleRate),
		noise:      0.02,
		rng:        rand.New(rand.NewPCG(1, 2)),
		tones: []Tone{
			{FreqHz: 433.92e6, Amp: 0.3},
			{FreqHz: 434.30e6, Amp: 0.05},
			{FreqHz: 433.40e6, Amp: 0.01},
		},
	}
	s.phase = make([]float64, len(s.tones))
	return s
}

// Read fills p with cu8 IQ, paced to the sample rate.
func (s *Synth) Read(p []byte) (int, error) {
	n := len(p) &^ 1
	if n > 32768 {
		n = 32768
	}
	s.mu.Lock()
	if s.start.IsZero() {
		s.start = time.Now()
	}
	// pace: wait until wall clock has caught up with the samples emitted
	due := s.start.Add(time.Duration(float64(s.emitted) / s.sampleRate * float64(time.Second)))
	s.mu.Unlock()
	if d := time.Until(due); d > 0 {
		time.Sleep(d)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < n; i += 2 {
		var re, im float64
		for k, t := range s.tones {
			off := t.FreqHz - s.centerHz
			if math.Abs(off) >= s.sampleRate/2 {
				continue
			}
			s.phase[k] += 2 * math.Pi * off / s.sampleRate
			re += t.Amp * math.Cos(s.phase[k])
			im += t.Amp * math.Sin(s.phase[k])
		}
		re += s.rng.NormFloat64() * s.noise
		im += s.rng.NormFloat64() * s.noise
		p[i] = toU8(re)
		p[i+1] = toU8(im)
	}
	for k := range s.phase {
		s.phase[k] = math.Mod(s.phase[k], 2*math.Pi)
	}
	s.emitted += int64(n / 2)
	return n, nil
}

func toU8(v float64) byte {
	x := v*127.5 + 127.5
	if x < 0 {
		return 0
	}
	if x > 255 {
		return 255
	}
	return byte(x + 0.5)
}

// SetFreq retunes the synthetic center frequency.
func (s *Synth) SetFreq(hz uint32) error {
	s.mu.Lock()
	s.centerHz = float64(hz)
	s.mu.Unlock()
	return nil
}

// SetSampleRate changes the sample rate.
func (s *Synth) SetSampleRate(sps uint32) error {
	s.mu.Lock()
	s.sampleRate = float64(sps)
	s.start = time.Time{}
	s.emitted = 0
	s.mu.Unlock()
	return nil
}

// SetGain scales the noise floor a little so gain changes are visible.
func (s *Synth) SetGain(db *float64) error {
	s.mu.Lock()
	s.noise = 0.02
	if db != nil {
		s.noise = 0.005 * math.Pow(10, *db/40)
	}
	s.mu.Unlock()
	return nil
}

// Close is a no-op.
func (s *Synth) Close() error { return nil }

// Wait blocks until ctx is done (the synth never fails).
func (s *Synth) Wait(ctx context.Context) { <-ctx.Done() }
