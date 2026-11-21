package dsp

import (
	"math"
	"math/cmplx"
)

// Mode is a demodulation mode.
type Mode string

const (
	ModeOff Mode = "off"
	ModeNFM Mode = "nfm"
	ModeAM  Mode = "am"
	ModeUSB Mode = "usb"
	ModeLSB Mode = "lsb"
)

// DefaultBandwidth returns the default channel bandwidth for a mode in Hz.
func DefaultBandwidth(m Mode) float64 {
	switch m {
	case ModeNFM:
		return 12500
	case ModeAM:
		return 10000
	case ModeUSB, ModeLSB:
		return 2800
	}
	return 0
}

// ValidMode reports whether s is a known mode.
func ValidMode(s string) bool {
	switch Mode(s) {
	case ModeOff, ModeNFM, ModeAM, ModeUSB, ModeLSB:
		return true
	}
	return false
}

// Demod turns cu8 IQ into mono audio for one channel at an offset from the
// tuned center. Two decimating FIR stages: a cheap wide stage 1 to ~250 kS/s,
// then the channel filter at audio rate.
type Demod struct {
	mode     Mode
	inRate   float64
	outRate  float64
	d1, d2   int
	offsetHz float64 // channel center relative to the tuned center
	shiftHz  float64 // SSB: passband center relative to the dial frequency

	lut  [256]float64
	nco  complex128 // input mixer phasor
	step complex128
	n    int // samples since phasor renormalisation

	s1 *decimator
	s2 *decimator

	post     complex128 // SSB re-mix phasor at audio rate
	postStep complex128

	prev    complex128 // NFM discriminator state
	dcX     float64    // DC blocker state
	dcY     float64
	lp      float64 // NFM post-filter state
	lpA     float64
	agcPeak float64

	// Raw skips the NFM audio post-filter, for data modems (AIS 9600 baud).
	Raw bool

	pow    float64 // channel power accumulator for the level meter
	powN   int
	LevelD float64 // last computed channel level, dB
}

// AudioRate picks the decimation for an input rate: an integer output rate
// near 32-48 kHz, split into two stages.
func AudioRate(inRate float64) (out float64, d1, d2 int) {
	best := 0
	for d := int(math.Ceil(inRate / 48000)); d <= int(inRate/24000); d++ {
		if d < 1 {
			continue
		}
		o := inRate / float64(d)
		if o == math.Trunc(o) {
			best = d
			break
		}
	}
	if best == 0 {
		best = int(math.Round(inRate / 32000))
	}
	d1 = 1
	for f := int(inRate / 200000); f >= 2; f-- {
		if best%f == 0 {
			d1 = f
			break
		}
	}
	return inRate / float64(best), d1, best / d1
}

// NewDemod creates a demodulator. offsetHz is the channel (dial) frequency
// minus the tuned center frequency.
func NewDemod(mode Mode, inRate, offsetHz, bandwidthHz float64) *Demod {
	out, d1, d2 := AudioRate(inRate)
	d := &Demod{mode: mode, inRate: inRate, outRate: out, d1: d1, d2: d2}
	for i := range d.lut {
		d.lut[i] = (float64(i) - 127.5) / 127.5
	}
	if bandwidthHz <= 0 {
		bandwidthHz = DefaultBandwidth(mode)
	}
	switch mode {
	case ModeUSB:
		d.shiftHz = bandwidthHz/2 + 150 // passband ~300 Hz .. 300+bw
	case ModeLSB:
		d.shiftHz = -(bandwidthHz/2 + 150)
	}
	d.offsetHz = offsetHz
	d.setMixer()
	mid := inRate / float64(d1)
	// Stage 1: pass the channel (+ SSB shift), reject what would alias onto it.
	pass1 := bandwidthHz/2 + math.Abs(d.shiftHz) + 2000
	stop1 := mid - pass1
	d.s1 = newDecimator(lowpass(inRate, (pass1+stop1)/2, stop1-pass1), d1)
	// Stage 2: the channel filter.
	trans := math.Max(bandwidthHz*0.25, 1000)
	d.s2 = newDecimator(lowpass(mid, bandwidthHz/2+trans/2, trans), d2)
	if d.shiftHz != 0 {
		d.post = 1
		d.postStep = cmplx.Exp(complex(0, 2*math.Pi*d.shiftHz/out))
	}
	// NFM post filter: ~3.4 kHz one-pole low-pass (also acts as de-emphasis).
	d.lpA = 1 - math.Exp(-2*math.Pi*3400/out)
	return d
}

func (d *Demod) setMixer() {
	d.nco = 1
	f := d.offsetHz + d.shiftHz
	d.step = cmplx.Exp(complex(0, -2*math.Pi*f/d.inRate))
}

// SetOffset moves the channel without resetting filters.
func (d *Demod) SetOffset(offsetHz float64) {
	d.offsetHz = offsetHz
	d.setMixer()
}

// Rate is the audio output rate in Hz.
func (d *Demod) Rate() float64 { return d.outRate }

// Mode returns the mode.
func (d *Demod) Mode() Mode { return d.mode }

// Process consumes cu8 IQ and appends int16 audio samples to out.
func (d *Demod) Process(iq []byte, out []int16) []int16 {
	for i := 0; i+1 < len(iq); i += 2 {
		x := complex(d.lut[iq[i]], d.lut[iq[i+1]]) * d.nco
		d.nco *= d.step
		if d.n++; d.n == 4096 {
			d.n = 0
			d.nco /= complex(cmplx.Abs(d.nco), 0)
		}
		y, ok := d.s1.push(x)
		if !ok {
			continue
		}
		z, ok := d.s2.push(y)
		if !ok {
			continue
		}
		d.pow += real(z)*real(z) + imag(z)*imag(z)
		d.powN++
		out = append(out, d.sample(z))
	}
	return out
}

// Level returns the mean channel power in dB since the last call and resets it.
func (d *Demod) Level() float64 {
	if d.powN > 0 {
		d.LevelD = 10 * math.Log10(d.pow/float64(d.powN)+1e-20)
		d.pow, d.powN = 0, 0
	}
	return d.LevelD
}

func (d *Demod) sample(z complex128) int16 {
	var v float64
	switch d.mode {
	case ModeNFM:
		ph := cmplx.Phase(z * cmplx.Conj(d.prev))
		d.prev = z
		// +-5 kHz deviation -> about +-0.5 full scale
		v = ph * d.outRate / (2 * math.Pi * 5000) * 0.5
		if d.Raw {
			v = d.dcBlock(v)
			break
		}
		d.lp += d.lpA * (v - d.lp)
		v = d.dcBlock(d.lp)
	case ModeAM:
		v = d.agc(d.dcBlock(cmplx.Abs(z)))
	case ModeUSB, ModeLSB:
		v = d.agc(real(z * d.post))
		d.post *= d.postStep
		d.post /= complex(cmplx.Abs(d.post), 0)
	default:
		return 0
	}
	v *= 32767
	if v > 32767 {
		v = 32767
	} else if v < -32768 {
		v = -32768
	}
	return int16(v)
}

func (d *Demod) dcBlock(x float64) float64 {
	y := x - d.dcX + 0.999*d.dcY
	d.dcX, d.dcY = x, y
	return y
}

// agc normalises to ~0.3 full scale: fast attack, slow decay.
func (d *Demod) agc(x float64) float64 {
	a := math.Abs(x)
	if a > d.agcPeak {
		d.agcPeak += (a - d.agcPeak) * 0.1
	} else {
		d.agcPeak *= 0.9999
	}
	if d.agcPeak < 1e-6 {
		return 0
	}
	return x * 0.3 / d.agcPeak
}

// decimator is a polyphase-free decimating FIR: it only computes kept outputs.
type decimator struct {
	taps []float64
	buf  []complex128 // doubled ring buffer so the window is contiguous
	pos  int
	d, k int
}

func newDecimator(taps []float64, d int) *decimator {
	n := len(taps)
	return &decimator{taps: taps, buf: make([]complex128, 2*n), d: d}
}

func (f *decimator) push(x complex128) (complex128, bool) {
	n := len(f.taps)
	f.buf[f.pos] = x
	f.buf[f.pos+n] = x
	f.pos++
	if f.pos == n {
		f.pos = 0
	}
	f.k++
	if f.k < f.d {
		return 0, false
	}
	f.k = 0
	var re, im float64
	w := f.buf[f.pos : f.pos+n] // oldest..newest
	for i, t := range f.taps {
		re += real(w[i]) * t
		im += imag(w[i]) * t
	}
	return complex(re, im), true
}

// lowpass designs a Blackman-windowed sinc low-pass with unity DC gain.
// cutoff and transition are in Hz.
func lowpass(rate, cutoff, transition float64) []float64 {
	if transition <= 0 {
		transition = rate / 10
	}
	n := int(5.5*rate/transition) | 1 // Blackman: ~5.5/df taps
	if n < 15 {
		n = 15
	}
	if n > 1023 {
		n = 1023
	}
	fc := cutoff / rate
	taps := make([]float64, n)
	m := float64(n - 1)
	var sum float64
	for i := range taps {
		x := float64(i) - m/2
		s := 2 * fc
		if x != 0 {
			s = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/m) + 0.08*math.Cos(4*math.Pi*float64(i)/m)
		taps[i] = s * w
		sum += taps[i]
	}
	for i := range taps {
		taps[i] /= sum
	}
	return taps
}
