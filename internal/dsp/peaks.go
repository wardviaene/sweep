package dsp

import (
	"math"
	"slices"
)

// Peak is a local maximum in a spectrum.
type Peak struct {
	FreqHz float64 `json:"freq_hz"`
	DB     float64 `json:"db"`
	SNR    float64 `json:"snr_db"`
	Spur   bool    `json:"spur,omitempty"` // near a multiple of the RTL2832 28.8 MHz crystal
}

// CrystalHz is the RTL2832U reference; its harmonics show up as internal spurs.
const CrystalHz = 28.8e6

// Floor returns the median of db, a robust noise-floor estimate.
func Floor(db []float64) float64 {
	s := slices.Clone(db)
	slices.Sort(s)
	return s[len(s)/2]
}

// FindPeaks returns up to n peaks at least minSNR dB above the median floor,
// strongest first. Peaks closer than sep bins are merged. Bins within edge
// fraction of either end (filter roll-off) are ignored.
// Bin i is at startHz + i*binHz.
func FindPeaks(db []float64, startHz, binHz float64, n int, minSNR float64, sep int, edge float64) []Peak {
	floor := Floor(db)
	lo := int(float64(len(db)) * edge)
	hi := len(db) - lo
	type cand struct {
		i int
		v float64
	}
	var cs []cand
	for i := max(lo, 1); i < min(hi, len(db)-1); i++ {
		if db[i] >= floor+minSNR && db[i] >= db[i-1] && db[i] > db[i+1] {
			cs = append(cs, cand{i, db[i]})
		}
	}
	slices.SortFunc(cs, func(a, b cand) int {
		if a.v > b.v {
			return -1
		}
		if a.v < b.v {
			return 1
		}
		return 0
	})
	var taken []int
	var out []Peak
	for _, c := range cs {
		if len(out) >= n {
			break
		}
		near := false
		for _, t := range taken {
			if abs(t-c.i) < sep {
				near = true
				break
			}
		}
		if near {
			continue
		}
		taken = append(taken, c.i)
		f := startHz + float64(c.i)*binHz
		k := math.Round(f / CrystalHz)
		out = append(out, Peak{
			FreqHz: math.Round(f),
			DB:     round1(c.v),
			SNR:    round1(c.v - floor),
			Spur:   k > 0 && math.Abs(f-k*CrystalHz) <= 2*binHz,
		})
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
