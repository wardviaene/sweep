package transcribe

import (
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
)

// IQSource is the radio's raw IQ history (radio.Radio).
type IQSource interface {
	SetIQRing(on bool)
	IQHistory(since time.Time) (radio.IQHistory, bool)
	RemoveTap(id int)
}

// iqCapture builds a clip from IQ: a private demodulator at the channel runs
// over the buffered IQ from before the lock and then keeps running over the
// live IQ, so the audio from before the VFO was pointed (the callsign) is there
// and the join is gapless (the history and the live tap are taken atomically).
type iqCapture struct {
	src   IQSource
	tap   int
	rate  int
	start time.Time // time of the first audio sample
	done  chan struct{}

	mu  sync.Mutex
	pcm []int16
}

// beginIQ starts a capture from t0-preRollS. It returns nil when the IQ ring is
// off or holds nothing of the current tuning.
func beginIQ(src IQSource, freqHz uint32, mode string, bw float64, t0 time.Time) *iqCapture {
	if src == nil || !dsp.ValidMode(mode) || mode == "off" {
		return nil
	}
	h, ok := src.IQHistory(t0.Add(-time.Duration(preRollS * float64(time.Second))))
	if !ok {
		return nil
	}
	d := dsp.NewDemod(dsp.Mode(mode), float64(h.SampleRate), float64(freqHz)-float64(h.CenterHz), bw)
	c := &iqCapture{src: src, tap: h.Tap, rate: int(d.Rate()), start: h.Start, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		c.add(d.Process(h.IQ, nil))
		for b := range h.Live {
			c.add(d.Process(b, nil))
		}
	}()
	return c
}

func (c *iqCapture) add(p []int16) {
	c.mu.Lock()
	if len(c.pcm) < maxCaptureS*c.rate {
		c.pcm = append(c.pcm, p...)
	}
	c.mu.Unlock()
}

// End stops the capture and returns the audio from the start of the pre-roll
// up to end (so the scanner's hang time is excluded).
func (c *iqCapture) End(end time.Time) (pcm []int16, rate int, ok bool) {
	c.src.RemoveTap(c.tap)
	<-c.done
	n := int(end.Sub(c.start).Seconds() * float64(c.rate))
	c.mu.Lock()
	defer c.mu.Unlock()
	n = min(n, len(c.pcm))
	if n <= 0 {
		return nil, 0, false
	}
	return c.pcm[:n], c.rate, true
}

// Cancel stops the capture without a result.
func (c *iqCapture) Cancel() {
	c.src.RemoveTap(c.tap)
	<-c.done
}
