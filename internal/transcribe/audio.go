// Package transcribe turns voice transmissions into text with a local
// whisper.cpp server, see docs/transcription.md. Nothing leaves the machine.
package transcribe

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
)

const (
	// WhisperRate is the sample rate whisper expects.
	WhisperRate = 16000
	// MinClipS is the shortest clip that is transcribed.
	MinClipS = 0.5
	// SilenceDB is the RMS (dBFS, after band-pass) below which a clip counts as silent.
	SilenceDB = -60.0

	bandLo = 300.0
	bandHi = 3400.0
)

// biquad is one second-order section (direct form I, RBJ cookbook).
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func newBiquad(highpass bool, fc, rate, q float64) *biquad {
	w := 2 * math.Pi * fc / rate
	cw, sw := math.Cos(w), math.Sin(w)
	alpha := sw / (2 * q)
	a0 := 1 + alpha
	b := &biquad{a1: -2 * cw / a0, a2: (1 - alpha) / a0}
	if highpass {
		b.b0, b.b1, b.b2 = (1+cw)/2/a0, -(1+cw)/a0, (1+cw)/2/a0
	} else {
		b.b0, b.b1, b.b2 = (1-cw)/2/a0, (1-cw)/a0, (1-cw)/2/a0
	}
	return b
}

func (b *biquad) step(x float64) float64 {
	y := b.b0*x + b.b1*b.x1 + b.b2*b.x2 - b.a1*b.y1 - b.a2*b.y2
	b.x2, b.x1, b.y2, b.y1 = b.x1, x, b.y1, y
	return y
}

// BandPass applies a 4th-order Butterworth 300-3400 Hz band-pass (voice band).
func BandPass(pcm []int16, rate float64) []float64 {
	out := make([]float64, len(pcm))
	hi := bandHi
	if hi > 0.45*rate {
		hi = 0.45 * rate
	}
	// Butterworth 4th order = two sections with these Q values.
	qs := []float64{0.5412, 1.3066}
	var chain []*biquad
	for _, q := range qs {
		chain = append(chain, newBiquad(true, bandLo, rate, q))
	}
	for _, q := range qs {
		chain = append(chain, newBiquad(false, hi, rate, q))
	}
	for i, v := range pcm {
		x := float64(v) / 32768
		for _, s := range chain {
			x = s.step(x)
		}
		out[i] = x
	}
	return out
}

// Resample converts x from rate in to out with linear interpolation. The input
// must already be band-limited below out/2 (BandPass is).
func Resample(x []float64, in, out float64) []float64 {
	if len(x) == 0 || in <= 0 {
		return nil
	}
	if in == out {
		return append([]float64(nil), x...)
	}
	n := int(float64(len(x)) * out / in)
	y := make([]float64, n)
	step := in / out
	for i := range y {
		p := float64(i) * step
		j := int(p)
		f := p - float64(j)
		a := x[j]
		b := a
		if j+1 < len(x) {
			b = x[j+1]
		}
		y[i] = a + (b-a)*f
	}
	return y
}

// RMSdB is the RMS level of x (full scale 1.0) in dB; -200 for silence.
func RMSdB(x []float64) float64 {
	if len(x) == 0 {
		return -200
	}
	var s float64
	for _, v := range x {
		s += v * v
	}
	r := math.Sqrt(s / float64(len(x)))
	if r < 1e-10 {
		return -200
	}
	return 20 * math.Log10(r)
}

// Normalize scales x towards a -20 dBFS RMS with at most 20 dB of gain and
// never past 0.95 of full scale, and returns 16-bit samples.
func Normalize(x []float64) []int16 {
	var peak float64
	for _, v := range x {
		peak = math.Max(peak, math.Abs(v))
	}
	gain := 1.0
	if r := RMSdB(x); r > -200 {
		gain = math.Pow(10, (-20-r)/20)
	}
	gain = math.Min(gain, 10)
	if peak > 0 {
		gain = math.Min(gain, 0.95/peak)
	}
	out := make([]int16, len(x))
	for i, v := range x {
		out[i] = int16(math.Max(-32768, math.Min(32767, math.Round(v*gain*32767))))
	}
	return out
}

// Prepare runs the clip pipeline: band-pass, resample to 16 kHz, normalize.
// silentDB is the band-passed RMS level, to judge near-silent clips.
func Prepare(pcm []int16, rate float64) (out []int16, rmsDB float64) {
	bp := BandPass(pcm, rate)
	rmsDB = RMSdB(bp)
	return Normalize(Resample(bp, rate, WhisperRate)), rmsDB
}

// WAV encodes 16-bit mono PCM as a WAV file.
func WAV(pcm []int16, rate int) []byte {
	b := make([]byte, 44+2*len(pcm))
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(2*rate))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(pcm)))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}

// WriteWAV writes pcm to path (creating its directory) atomically.
func WriteWAV(path string, pcm []int16, rate int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, WAV(pcm, rate), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
