package transcribe

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/wardviaene/sweep/internal/radio"
)

const (
	preRollS    = 0.3
	maxCaptureS = 180
)

// Tee is a radio.AudioSink that forwards to the next sink (the /ws/audio hub)
// and, while transcription is on, keeps a short pre-roll of the listening VFO
// audio and records it for the active captures. With transcription off and no
// capture it adds one atomic load per audio block.
type Tee struct {
	next radio.AudioSink
	on   atomic.Bool // enabled or capturing

	mu      sync.Mutex
	enabled bool
	rate    int
	pre     []int16
	caps    map[*Capture]struct{}
}

// NewTee wraps next (may be nil).
func NewTee(next radio.AudioSink) *Tee {
	return &Tee{next: next, caps: map[*Capture]struct{}{}}
}

// SetEnabled turns the pre-roll ring on or off.
func (t *Tee) SetEnabled(on bool) {
	t.mu.Lock()
	t.enabled = on
	if !on {
		t.pre = nil
	}
	t.refreshLocked()
	t.mu.Unlock()
}

func (t *Tee) refreshLocked() { t.on.Store(t.enabled || len(t.caps) > 0) }

// AudioMeta implements radio.AudioSink.
func (t *Tee) AudioMeta(rate int, mode string) {
	if t.next != nil {
		t.next.AudioMeta(rate, mode)
	}
	t.mu.Lock()
	if rate != t.rate {
		t.rate = rate
		t.pre = nil
		for c := range t.caps {
			if c.rate != rate {
				c.broken = true
			}
		}
	}
	t.mu.Unlock()
}

// Audio implements radio.AudioSink.
func (t *Tee) Audio(pcm []int16) {
	if t.next != nil {
		t.next.Audio(pcm)
	}
	if !t.on.Load() {
		return
	}
	t.mu.Lock()
	for c := range t.caps {
		if c.broken || len(c.pcm) >= maxCaptureS*c.rate {
			continue
		}
		c.pcm = append(c.pcm, pcm...)
	}
	if t.enabled && t.rate > 0 {
		keep := int(preRollS * float64(t.rate))
		t.pre = append(t.pre, pcm...)
		if len(t.pre) > 4*keep {
			t.pre = append(t.pre[:0], t.pre[len(t.pre)-keep:]...)
		}
	}
	t.mu.Unlock()
}

// Level implements radio.AudioSink.
func (t *Tee) Level(db float64, open bool) {
	if t.next != nil {
		t.next.Level(db, open)
	}
}

// Capture is audio being recorded from the listening VFO.
type Capture struct {
	t      *Tee
	rate   int
	begin  time.Time
	preN   int
	pcm    []int16
	broken bool
}

// Begin starts a capture that includes the last 0.3 s of audio. It returns nil
// while the listening VFO is off (no audio rate yet).
func (t *Tee) Begin() *Capture {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rate <= 0 {
		return nil
	}
	keep := min(int(preRollS*float64(t.rate)), len(t.pre))
	c := &Capture{t: t, rate: t.rate, begin: time.Now(), preN: keep}
	c.pcm = append(make([]int16, 0, t.rate*4), t.pre[len(t.pre)-keep:]...)
	t.caps[c] = struct{}{}
	t.refreshLocked()
	return c
}

// End stops the capture and returns the audio from 0.3 s before Begin up to
// end (so the scanner's hang time is excluded).
func (c *Capture) End(end time.Time) (pcm []int16, rate int, ok bool) {
	t := c.t
	t.mu.Lock()
	delete(t.caps, c)
	t.refreshLocked()
	broken := c.broken
	t.mu.Unlock()
	if broken || c.rate <= 0 {
		return nil, 0, false
	}
	n := c.preN + int(end.Sub(c.begin).Seconds()*float64(c.rate))
	n = min(n, len(c.pcm))
	if n <= 0 {
		return nil, 0, false
	}
	return c.pcm[:n], c.rate, true
}

// Cancel stops the capture without a result.
func (c *Capture) Cancel() {
	c.t.mu.Lock()
	delete(c.t.caps, c)
	c.t.refreshLocked()
	c.t.mu.Unlock()
}
