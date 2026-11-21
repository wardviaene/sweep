package dsp

import (
	"math"
	"testing"
)

func TestAudioRate(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want float64
	}{{2048000, 0}, {2400000, 0}, {1024000, 0}, {1400000, 0}, {1800000, 0}} {
		out, d1, d2 := AudioRate(c.in)
		if out < 24000 || out > 50000 || out != math.Trunc(out) {
			t.Errorf("%v: out %v", c.in, out)
		}
		if float64(d1*d2)*out != c.in {
			t.Errorf("%v: d1 %d d2 %d out %v", c.in, d1, d2, out)
		}
		if c.in/float64(d1) > 400000 || c.in/float64(d1) < 100000 {
			t.Errorf("%v: stage-1 rate %v", c.in, c.in/float64(d1))
		}
	}
}

// synthIQ builds cu8 IQ from a function of time.
func synthIQ(rate float64, n int, f func(t float64) complex128) []byte {
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		z := f(float64(i) / rate)
		b[2*i] = byte(math.Round(127.5 + 120*real(z)))
		b[2*i+1] = byte(math.Round(127.5 + 120*imag(z)))
	}
	return b
}

// toneFreq estimates the dominant audio frequency by zero crossings.
func toneFreq(a []int16, rate float64) float64 {
	a = a[len(a)/4:] // skip filter warm-up
	z := 0
	for i := 1; i < len(a); i++ {
		if (a[i-1] < 0) != (a[i] < 0) {
			z++
		}
	}
	return float64(z) / 2 / (float64(len(a)) / rate)
}

func rms(a []int16) float64 {
	a = a[len(a)/4:]
	var s float64
	for _, v := range a {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(len(a)))
}

const rate = 2048000.0

func TestNFM(t *testing.T) {
	// channel at +300 kHz, 1 kHz tone, 3 kHz deviation
	iq := synthIQ(rate, int(rate/2), func(t float64) complex128 {
		ph := 2*math.Pi*300e3*t + 3000/1000.0*math.Sin(2*math.Pi*1000*t)
		return complex(math.Cos(ph), math.Sin(ph))
	})
	d := NewDemod(ModeNFM, rate, 300e3, 0)
	a := d.Process(iq, nil)
	if got := toneFreq(a, d.Rate()); math.Abs(got-1000) > 30 {
		t.Fatalf("tone %v Hz", got)
	}
	if r := rms(a); r < 3000 {
		t.Fatalf("rms %v too low", r)
	}
	// a station 25 kHz away must be strongly rejected
	d2 := NewDemod(ModeNFM, rate, 325e3, 0)
	d2.Process(iq, nil)
	if lv := d2.Level(); lv > d.Level()-40 {
		t.Fatalf("adjacent channel level %v vs %v", lv, d.LevelD)
	}
}

func TestAM(t *testing.T) {
	iq := synthIQ(rate, int(rate/2), func(t float64) complex128 {
		a := 0.5 * (1 + 0.6*math.Sin(2*math.Pi*700*t))
		ph := 2 * math.Pi * -150e3 * t
		return complex(a*math.Cos(ph), a*math.Sin(ph))
	})
	d := NewDemod(ModeAM, rate, -150e3, 0)
	a := d.Process(iq, nil)
	if got := toneFreq(a, d.Rate()); math.Abs(got-700) > 25 {
		t.Fatalf("tone %v Hz", got)
	}
}

func TestSSB(t *testing.T) {
	// a carrier 1.2 kHz above the dial: USB hears 1.2 kHz, LSB hears ~nothing
	dial := 200e3
	iq := synthIQ(rate, int(rate/2), func(t float64) complex128 {
		ph := 2 * math.Pi * (dial + 1200) * t
		return complex(0.5*math.Cos(ph), 0.5*math.Sin(ph))
	})
	u := NewDemod(ModeUSB, rate, dial, 0)
	a := u.Process(iq, nil)
	if got := toneFreq(a, u.Rate()); math.Abs(got-1200) > 25 {
		t.Fatalf("usb tone %v Hz", got)
	}
	l := NewDemod(ModeLSB, rate, dial, 0)
	l.Process(iq, nil)
	if l.Level() > u.Level()-30 {
		t.Fatalf("lsb leaks: %v vs %v dB", l.LevelD, u.LevelD)
	}
}

func BenchmarkNFM(b *testing.B) {
	iq := synthIQ(rate, int(rate/10), func(t float64) complex128 { return complex(math.Cos(t*1e6), math.Sin(t*1e6)) })
	d := NewDemod(ModeNFM, rate, 300e3, 0)
	out := make([]int16, 0, 8192)
	b.SetBytes(int64(len(iq)))
	for i := 0; i < b.N; i++ {
		out = d.Process(iq, out[:0])
	}
}
