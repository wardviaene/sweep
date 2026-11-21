package radio

import (
	"math"
	"time"
)

const (
	iqRingSeconds  = 2.0        // history kept for pre-roll
	iqRingMaxBytes = 10_000_000 // cap (cu8): about 2 s at 2.4 MS/s
)

// iqRing is a ring buffer of the most recent raw cu8 IQ with its timing: the
// wall-clock arrival time of the newest block maps a sample position to a time.
// It is reset by every retune, so it only holds IQ of the current center and rate.
type iqRing struct {
	buf    []byte
	n      int64 // bytes written in total
	valid  int64 // first byte index belonging to the current tuning
	rate   float64
	center uint32
	lastT  time.Time // arrival of the newest block (its last byte)
}

func newIQRing(rate uint32) *iqRing {
	size := int(math.Min(iqRingSeconds*float64(rate)*2, iqRingMaxBytes))
	return &iqRing{buf: make([]byte, size&^1)}
}

func (g *iqRing) reset(rate, center uint32) {
	g.valid = g.n
	g.rate, g.center = float64(rate), center
	if want := int(math.Min(iqRingSeconds*float64(rate)*2, iqRingMaxBytes)) &^ 1; want != len(g.buf) {
		g.buf = make([]byte, want)
		g.n, g.valid = 0, 0
	}
}

func (g *iqRing) write(b []byte, now time.Time) {
	size := int64(len(g.buf))
	if size == 0 {
		return
	}
	if int64(len(b)) > size {
		g.n += int64(len(b)) - size
		b = b[int64(len(b))-size:]
	}
	p := int(g.n % size)
	c := copy(g.buf[p:], b)
	copy(g.buf, b[c:])
	g.n += int64(len(b))
	g.lastT = now
}

// timeOf is the estimated wall-clock time of byte index i.
func (g *iqRing) timeOf(i int64) time.Time {
	return g.lastT.Add(-time.Duration(float64(g.n-i) / 2 / g.rate * float64(time.Second)))
}

// since copies the IQ from time t (or the oldest sample of the current tuning
// when t is earlier) up to now, and returns the time of its first sample.
func (g *iqRing) since(t time.Time) (iq []byte, start time.Time) {
	size := int64(len(g.buf))
	oldest := max(g.valid, g.n-size)
	i := g.n - 2*int64(math.Round(g.lastT.Sub(t).Seconds()*g.rate))
	i = max(min(i, g.n), oldest)
	i += (g.n - i) & 1 // keep I/Q pairs whole
	iq = make([]byte, 0, g.n-i)
	for j := i; j < g.n; {
		p := int(j % size)
		c := int(min(g.n-j, size-int64(p)))
		iq = append(iq, g.buf[p:p+c]...)
		j += int64(c)
	}
	return iq, g.timeOf(i)
}

// SetIQRing turns the raw IQ history on or off. It is only allocated while on
// (the scanner with transcription), about 2 s of cu8 and at most 10 MB.
func (r *Radio) SetIQRing(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !on {
		r.ring = nil
		return
	}
	if r.ring == nil {
		r.ring = newIQRing(r.st.SampleRate)
		r.ring.reset(r.st.SampleRate, r.st.CenterHz)
	}
}

// IQHistory is the IQ of the current tuning from a point in the past, and the
// live IQ continuing exactly where it ends.
type IQHistory struct {
	IQ         []byte        // cu8 history, oldest first
	Start      time.Time     // estimated time of the first history sample
	CenterHz   uint32        // tuning of the history and of the live IQ
	SampleRate uint32        //
	Live       <-chan []byte // IQ after the history; closed on retune or RemoveTap
	Tap        int           // tap id for RemoveTap
}

// IQHistory returns the buffered IQ from time since (clamped to the oldest IQ
// since the last retune) and subscribes to the live IQ in one step, so the two
// join without a gap or overlap. ok is false when the IQ ring is off or empty.
// The live channel is closed by a retune (the IQ would change meaning).
func (r *Radio) IQHistory(since time.Time) (h IQHistory, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ring == nil || r.ring.n <= r.ring.valid {
		return h, false
	}
	h.IQ, h.Start = r.ring.since(since)
	h.CenterHz, h.SampleRate = r.ring.center, uint32(r.ring.rate)
	r.nextTap++
	t := &tap{iq: make(chan []byte, 256), history: true}
	r.taps[r.nextTap] = t
	h.Live, h.Tap = t.iq, r.nextTap
	return h, true
}

// retuneRingLocked drops history taps and restarts the ring for the new tuning.
func (r *Radio) retuneRingLocked() {
	for id, t := range r.taps {
		if t.history {
			delete(r.taps, id)
			close(t.iq)
		}
	}
	if r.ring != nil {
		r.ring.reset(r.st.SampleRate, r.st.CenterHz)
	}
}
