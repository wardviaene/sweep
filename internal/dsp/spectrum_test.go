package dsp

import (
	"math"
	"testing"
)

// A full-scale-ish tone at +fs/4 must peak at bin 3n/4 after fftshift.
func TestToneBin(t *testing.T) {
	const n = 1024
	s := NewSpectrum(n)
	iq := make([]byte, 2*n*4)
	for i := 0; i < len(iq)/2; i++ {
		ph := 2 * math.Pi * 0.25 * float64(i)
		iq[2*i] = byte(127.5 + 100*math.Cos(ph))
		iq[2*i+1] = byte(127.5 + 100*math.Sin(ph))
	}
	// feed in odd-sized chunks to exercise the partial-block path
	for off := 0; off < len(iq); off += 777 {
		end := off + 777
		if end > len(iq) {
			end = len(iq)
		}
		s.Write(iq[off:end])
	}
	if s.Count() != 4 {
		t.Fatalf("count = %d", s.Count())
	}
	db := make([]float64, n)
	if !s.Take(db) {
		t.Fatal("no data")
	}
	peak := 0
	for i := range db {
		if db[i] > db[peak] {
			peak = i
		}
	}
	if peak != 3*n/4 {
		t.Fatalf("peak at bin %d, want %d", peak, 3*n/4)
	}
	// amplitude 100/127.5 ≈ -2.1 dBFS
	if db[peak] < -4 || db[peak] > 0 {
		t.Fatalf("peak level %.1f dB", db[peak])
	}
}

func TestQuantize(t *testing.T) {
	out := make([]byte, 3)
	Quantize(out, []float64{-200, -50, 10}, -100, 0)
	if out[0] != 0 || out[2] != 255 || out[1] < 126 || out[1] > 129 {
		t.Fatalf("got %v", out)
	}
}
