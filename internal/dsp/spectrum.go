// Package dsp turns cu8 IQ samples into power spectra.
package dsp

import (
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Spectrum accumulates windowed FFT power over cu8 IQ input.
// Not safe for concurrent use.
type Spectrum struct {
	n       int
	win     []float64
	norm    float64 // 1 / (sum of window)^2: a full-scale complex tone reads 0 dB
	fft     *fourier.CmplxFFT
	in, out []complex128
	acc     []float64
	count   int
	pending []byte // leftover bytes of an incomplete block
	lut     [256]float64
}

// NewSpectrum creates an accumulator for FFT size n (a power of two is fastest).
func NewSpectrum(n int) *Spectrum {
	s := &Spectrum{
		n:   n,
		win: blackmanHarris(n),
		fft: fourier.NewCmplxFFT(n),
		in:  make([]complex128, n),
		out: make([]complex128, n),
		acc: make([]float64, n),
	}
	var sum float64
	for _, w := range s.win {
		sum += w
	}
	s.norm = 1 / (sum * sum)
	for i := range s.lut {
		s.lut[i] = (float64(i) - 127.5) / 127.5
	}
	return s
}

// Size returns the FFT size.
func (s *Spectrum) Size() int { return s.n }

// Write consumes interleaved cu8 I/Q bytes, running one FFT per n samples.
func (s *Spectrum) Write(iq []byte) {
	block := 2 * s.n
	if len(s.pending) > 0 {
		need := block - len(s.pending)
		if len(iq) < need {
			s.pending = append(s.pending, iq...)
			return
		}
		s.pending = append(s.pending, iq[:need]...)
		s.block(s.pending)
		s.pending = s.pending[:0]
		iq = iq[need:]
	}
	for len(iq) >= block {
		s.block(iq[:block])
		iq = iq[block:]
	}
	s.pending = append(s.pending, iq...)
}

func (s *Spectrum) block(b []byte) {
	for i := 0; i < s.n; i++ {
		w := s.win[i]
		s.in[i] = complex(s.lut[b[2*i]]*w, s.lut[b[2*i+1]]*w)
	}
	s.fft.Coefficients(s.out, s.in)
	for i, c := range s.out {
		re, im := real(c), imag(c)
		s.acc[i] += re*re + im*im
	}
	s.count++
}

// Count is the number of FFTs accumulated since the last Take or Reset.
func (s *Spectrum) Count() int { return s.count }

// Reset drops accumulated power and any partial block.
func (s *Spectrum) Reset() {
	for i := range s.acc {
		s.acc[i] = 0
	}
	s.count = 0
	s.pending = s.pending[:0]
}

// Take writes the averaged power in dB into dst (len n), ordered from lowest
// to highest frequency (DC in the middle), masks the DC bin, and resets.
// It returns false if nothing was accumulated.
func (s *Spectrum) Take(dst []float64) bool {
	if s.count == 0 {
		return false
	}
	half := s.n / 2
	scale := s.norm / float64(s.count)
	for i := 0; i < s.n; i++ {
		// fftshift: output index i holds FFT bin (i + n/2) mod n
		p := s.acc[(i+half)%s.n] * scale
		dst[i] = 10 * math.Log10(p+1e-20)
	}
	// The RTL2832 has a DC offset spike at the center; replace it with its neighbours.
	if s.n >= 4 {
		dst[half] = (dst[half-2] + dst[half+2]) / 2
		dst[half-1] = dst[half-2]
		dst[half+1] = dst[half+2]
	}
	for i := range s.acc {
		s.acc[i] = 0
	}
	s.count = 0
	return true
}

// Quantize maps dB values onto 0..255 between minDB and maxDB.
func Quantize(dst []byte, db []float64, minDB, maxDB float64) {
	k := 255 / (maxDB - minDB)
	for i, v := range db {
		q := (v - minDB) * k
		switch {
		case q <= 0:
			dst[i] = 0
		case q >= 255:
			dst[i] = 255
		default:
			dst[i] = byte(q + 0.5)
		}
	}
}

func blackmanHarris(n int) []float64 {
	w := make([]float64, n)
	const a0, a1, a2, a3 = 0.35875, 0.48829, 0.14128, 0.01168
	for i := range w {
		x := 2 * math.Pi * float64(i) / float64(n-1)
		w[i] = a0 - a1*math.Cos(x) + a2*math.Cos(2*x) - a3*math.Cos(3*x)
	}
	return w
}
