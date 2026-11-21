package scanner

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
)

const (
	seedTime    = 500 * time.Millisecond
	tauUp       = 20 * time.Second // baseline rises slowly ...
	tauDown     = 3 * time.Second  // ... and falls quickly (see seed notes in docs)
	dcExclude   = 10_000.0         // Hz around the window center
	debounce    = 2                // consecutive active frames before a transmission starts
	settle      = 2                // frames to discard after a retune
	activeDwell = 60 * time.Second // a window with activity this recently gets double dwell
	enbw        = 2.0              // equivalent noise bandwidth of the Blackman-Harris window, in bins
)

type win struct {
	center   uint32
	lo, hi   float64 // watched span (Hz)
	base     []float64
	seedMin  []float64
	seedT0   time.Time
	seeded   bool
	active   []time.Time // when each bin became active (zero = not active)
	consec   []uint8
	lastT    time.Time
	activity time.Time // last transmission in this window
	i0, i1   int       // bin range inside [lo, hi]
}

type hold struct {
	ch       uint32  // channel the VFO is on
	f0       float64 // frequency of the detected carrier bin
	start    time.Time
	lastAct  time.Time
	peak     float64
	label    string
	priority bool
	b0, b1   int             // bins of the carrier (frozen baseline, activity check)
	endClip  func(time.Time) // ends the audio capture for transcription, may be nil
}

type engine struct {
	s      *Scanner
	p      params
	ctx    context.Context
	sr     float64
	wins   []*win
	cur    int
	enter  time.Time
	settle int
	h      *hold // non-nil while locked
}

// loop runs until ctx ends. It does not restore the radio.
func (s *Scanner) loop(ctx context.Context, p params, done chan struct{}) {
	defer close(done)
	frames, cancel := s.d.Radio.SubscribeSpectrum()
	defer cancel()
	e := &engine{s: s, p: p, ctx: ctx, sr: float64(s.d.Radio.Status().SampleRate)}
	e.plan()
	if !e.goTo(0, true) {
		return
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if e.h != nil {
				e.end(time.Now(), false)
			}
			return
		case f, ok := <-frames:
			if !ok {
				return
			}
			e.frame(f, time.Now())
		case t := <-tick.C:
			e.tick(t)
		}
	}
}

// plan splits the range into windows of at most 80% of the sample rate.
func (e *engine) plan() {
	lo, hi := float64(e.p.lo), float64(e.p.hi)
	span := 0.8 * e.sr
	n := max(1, int(math.Ceil((hi-lo)/span-1e-9)))
	width := (hi - lo) / float64(n)
	for i := 0; i < n; i++ {
		a := lo + width*float64(i)
		b := a + width
		c := (a + b) / 2
		if n == 1 && width <= 0.4*e.sr-20_000 {
			c = b + 15_000 // keep the DC spike out of a narrow range
		}
		e.wins = append(e.wins, &win{center: uint32(math.Round(c)), lo: a, hi: b})
	}
}

// goTo tunes to window i and (first time) sets up the muted VFO.
func (e *engine) goTo(i int, first bool) bool {
	w := e.wins[i]
	d := e.s.d
	for {
		c := w.center
		if _, err := d.Radio.Tune(radio.TuneRequest{FreqHz: &c}); err == nil {
			break
		} else {
			log.Printf("scanner: tune %d: %v", c, err)
			e.s.setErr(err.Error())
		}
		select {
		case <-e.ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	e.s.setErr("")
	if first {
		mode, bw, f := e.p.mode, e.p.bw, w.center
		closed := closedSquelch
		if _, err := d.Radio.SetDemod(radio.DemodRequest{Mode: &mode, BandwidthHz: &bw, FreqHz: &f, SetSquelch: true, SquelchDB: &closed}); err != nil {
			log.Printf("scanner: vfo: %v", err)
		}
	}
	e.cur, e.settle, e.enter = i, settle, time.Now()
	w.lastT = time.Time{}
	e.s.mu.Lock()
	e.s.st.Window = &Window{CenterHz: w.center, LoHz: uint32(w.lo), HiHz: uint32(w.hi), Index: i, Count: len(e.wins)}
	e.s.mu.Unlock()
	return true
}

func (s *Scanner) setErr(msg string) {
	s.mu.Lock()
	s.st.Error = msg
	s.mu.Unlock()
}

func (e *engine) mute() {
	closed := closedSquelch
	if _, err := e.s.d.Radio.SetDemod(radio.DemodRequest{SetSquelch: true, SquelchDB: &closed}); err != nil {
		log.Printf("scanner: mute: %v", err)
	}
}

// tick handles timeouts when no frame arrives and window cycling.
func (e *engine) tick(t time.Time) {
	if e.h != nil {
		if t.Sub(e.h.lastAct) >= e.p.hang {
			e.end(t, true)
		}
		return
	}
	e.advance(t)
}

// advance moves to the next window after the dwell (longer where there was recent activity).
func (e *engine) advance(t time.Time) {
	if len(e.wins) < 2 || e.h != nil {
		return
	}
	w := e.wins[e.cur]
	if !w.seeded {
		return
	}
	dwell := e.p.dwell
	if !w.activity.IsZero() && t.Sub(w.activity) < activeDwell {
		dwell *= 2
	}
	if t.Sub(e.enter) >= dwell {
		e.goTo((e.cur+1)%len(e.wins), false)
	}
}

func (e *engine) frame(f radio.SpecFrame, t time.Time) {
	w := e.wins[e.cur]
	if f.CenterHz != w.center || float64(f.SampleRate) != e.sr || len(f.DB) < 16 {
		return // from before the retune
	}
	if e.settle > 0 {
		e.settle--
		return
	}
	n := len(f.DB)
	binHz := e.sr / float64(n)
	start := float64(w.center) - e.sr/2
	if w.base == nil {
		w.base = make([]float64, n)
		w.seedMin = make([]float64, n)
		for i := range w.seedMin {
			w.seedMin[i] = math.Inf(1)
		}
		w.active = make([]time.Time, n)
		w.consec = make([]uint8, n)
		w.i0 = max(1, int(math.Ceil((w.lo-start)/binHz)))
		w.i1 = min(n-2, int(math.Floor((w.hi-start)/binHz)))
		w.seedT0 = t
	}
	if !w.seeded {
		// Seed with the per-bin minimum of the first 500 ms: a transmission that is
		// already on when the window is first visited does not become baseline.
		for i, v := range f.DB {
			w.seedMin[i] = math.Min(w.seedMin[i], v)
		}
		if t.Sub(w.seedT0) >= seedTime {
			for i := range w.base {
				w.base[i] = w.seedMin[i] + 1 // the minimum of noisy frames sits about 1 dB low
			}
			w.seeded = true
			w.lastT = t
		}
		return
	}
	dt := t.Sub(w.lastT)
	if w.lastT.IsZero() || dt > time.Second {
		dt = 50 * time.Millisecond
	}
	w.lastT = t
	e.track(w, f.DB, t, dt)
	if e.h != nil {
		e.holdCheck(w, f.DB, start, binHz, t)
		return
	}
	if c, ok := e.pick(w, f.DB, start, binHz, false); ok {
		e.lock(w, c, f.DB, start, binHz, t)
		return
	}
	e.advance(t)
}

// track updates the baseline and the per-bin active state (not for the held carrier).
func (e *engine) track(w *win, db []float64, t time.Time, dt time.Duration) {
	aUp := 1 - math.Exp(-dt.Seconds()/tauUp.Seconds())
	aDown := 1 - math.Exp(-dt.Seconds()/tauDown.Seconds())
	for i := w.i0; i <= w.i1; i++ {
		if e.h != nil && i >= e.h.b0 && i <= e.h.b1 {
			continue
		}
		v := db[i]
		if v >= w.base[i]+e.p.margin {
			if w.active[i].IsZero() {
				w.active[i] = t
			}
			if w.consec[i] < 255 {
				w.consec[i]++
			}
			if t.Sub(w.active[i]) > e.p.maxTx { // continuous, not a transmission: baseline
				w.base[i], w.active[i], w.consec[i] = v, time.Time{}, 0
			}
			continue
		}
		w.active[i], w.consec[i] = time.Time{}, 0
		if v > w.base[i] {
			w.base[i] += aUp * (v - w.base[i])
		} else {
			w.base[i] += aDown * (v - w.base[i])
		}
	}
}

type cand struct {
	ch     uint32
	f0     float64
	i      int
	v      float64
	over   float64 // dB above baseline
	since  time.Time
	prio   bool
	b0, b1 int
}

// pick returns the best new transmission start: priority channels first, then the
// strongest over its baseline. With onlyPriority, other channels are not considered.
func (e *engine) pick(w *win, db []float64, start, binHz float64, onlyPriority bool) (cand, bool) {
	var best cand
	found := false
	center := float64(w.center)
	for i := w.i0; i <= w.i1; i++ {
		if w.consec[i] < debounce {
			continue
		}
		b0 := i
		mx := i
		for i <= w.i1 && w.consec[i] >= debounce {
			if db[i] > db[mx] {
				mx = i
			}
			i++
		}
		b1 := i - 1
		f := start + float64(mx)*binHz
		if math.Abs(f-center) < dcExclude {
			continue
		}
		if k := math.Round(f / dsp.CrystalHz); k > 0 && math.Abs(f-k*dsp.CrystalHz) <= 2*binHz {
			continue
		}
		step := float64(e.p.step)
		ch := math.Round(f/step) * step
		if lim := 0.4*e.sr - 1000; math.Abs(ch-center) >= lim { // keep the VFO inside the span
			ch -= math.Copysign(step, ch-center)
		}
		if ch < float64(e.p.lo)-step/2 || ch > float64(e.p.hi)+step/2 || ch < 1 {
			continue
		}
		c := cand{ch: uint32(ch), f0: f, i: mx, v: db[mx], over: db[mx] - w.base[mx], since: w.active[mx], b0: b0, b1: b1}
		if e.s.isLockedOut(c.ch, e.p.step) {
			continue
		}
		for _, p := range e.p.priorityHz {
			if math.Abs(float64(p)-ch) <= step/2 {
				c.prio = true
			}
		}
		if onlyPriority && (!c.prio || (e.h != nil && e.h.ch == c.ch)) {
			continue
		}
		if !found || (c.prio && !best.prio) || (c.prio == best.prio && c.over > best.over) {
			best, found = c, true
		}
	}
	return best, found
}

// holdBins is the bin range of a carrier at f0: the channel bandwidth, at least a few bins.
func (e *engine) holdBins(w *win, f0, start, binHz float64) (int, int) {
	hw := math.Max(e.p.bw/2, 2*binHz)
	i0 := int(math.Floor((f0 - hw - start) / binHz))
	i1 := int(math.Ceil((f0 + hw - start) / binHz))
	return max(i0, w.i0), min(i1, w.i1)
}

// lock points the VFO at the candidate and starts holding it.
func (e *engine) lock(w *win, c cand, db []float64, start, binHz float64, t time.Time) {
	b0, b1 := e.holdBins(w, c.f0, start, binHz)
	// Squelch in the channel-level domain: baseline power summed over the channel
	// bandwidth (noise power per bin is 1/ENBW of the bin width) plus margin/2,
	// but at least 3 dB below the carrier that triggered, so a weak start still opens it.
	var sum float64
	for i := b0; i <= b1; i++ {
		sum += math.Pow(10, w.base[i]/10)
	}
	sq := 10*math.Log10(sum/enbw) + e.p.margin/2
	sq = math.Min(sq, c.v-3)
	ch, mode, bw := c.ch, e.p.mode, e.p.bw
	if _, err := e.s.d.Radio.SetDemod(radio.DemodRequest{Mode: &mode, FreqHz: &ch, BandwidthHz: &bw, SetSquelch: true, SquelchDB: &sq}); err != nil {
		log.Printf("scanner: vfo %d: %v", ch, err)
	}
	var label string
	if e.s.d.Label != nil {
		label = e.s.d.Label(float64(ch))
	}
	since := c.since
	if since.IsZero() {
		since = t
	}
	e.h = &hold{ch: ch, f0: c.f0, start: since, lastAct: t, peak: c.v, label: label, priority: c.prio, b0: b0, b1: b1}
	if e.s.d.Clip != nil {
		e.h.endClip = e.s.d.Clip(ch, label, mode, since)
	}
	for i := b0; i <= b1; i++ {
		w.consec[i], w.active[i] = 0, time.Time{}
	}
	w.activity = t
	e.s.mu.Lock()
	e.s.st.State = "locked"
	e.s.st.Active = &Active{FreqHz: ch, Label: label, Since: float64(since.UnixMilli()) / 1e3, LevelDB: round(c.v, 1)}
	e.s.mu.Unlock()
}

// holdCheck keeps the lock while the carrier is there; ends it after hang_s of quiet.
func (e *engine) holdCheck(w *win, db []float64, start, binHz float64, t time.Time) {
	h := e.h
	mx, over := math.Inf(-1), math.Inf(-1)
	for i := h.b0; i <= h.b1; i++ {
		mx = math.Max(mx, db[i])
		over = math.Max(over, db[i]-w.base[i])
	}
	if over >= e.p.margin/2 {
		h.lastAct = t
		h.peak = math.Max(h.peak, mx)
		e.s.mu.Lock()
		if e.s.st.Active != nil {
			e.s.st.Active.LevelDB = round(mx, 1)
		}
		e.s.mu.Unlock()
		if t.Sub(h.start) > e.p.maxTx { // a continuous carrier: log it, fold it into the baseline
			for i := h.b0; i <= h.b1; i++ {
				w.base[i] = db[i]
			}
			e.end(t, true)
			return
		}
	} else if t.Sub(h.lastAct) >= e.p.hang {
		e.end(t, true)
		return
	}
	if !h.priority && len(e.p.priorityHz) > 0 {
		if c, ok := e.pick(w, db, start, binHz, true); ok {
			e.end(t, true)
			e.lock(w, c, db, start, binHz, t)
		}
	}
}

// end finishes the held transmission: records it, publishes the event and mutes the VFO.
func (e *engine) end(t time.Time, mute bool) {
	h := e.h
	e.h = nil
	if h.endClip != nil {
		h.endClip(h.lastAct) // the hang time is not part of the clip
	}
	dur := h.lastAct.Sub(h.start).Seconds()
	tx := Tx{FreqHz: h.ch, Label: h.label, Start: float64(h.start.UnixMilli()) / 1e3, DurationS: round(math.Max(dur, 0), 2), PeakDB: round(h.peak, 1)}
	s := e.s
	s.mu.Lock()
	s.st.Recent = append([]Tx{tx}, s.st.Recent...)
	if len(s.st.Recent) > maxRecent {
		s.st.Recent = s.st.Recent[:maxRecent]
	}
	c := s.chans[h.ch]
	if c == nil {
		c = &Channel{FreqHz: h.ch, Label: h.label}
		s.chans[h.ch] = c
	}
	c.Count++
	c.TotalS = round(c.TotalS+tx.DurationS, 2)
	c.Last = tx.Start + tx.DurationS
	s.st.State = "watching"
	s.st.Active = nil
	s.mu.Unlock()
	if mute {
		e.mute()
	}
	s.d.Decoders.Events().Publish("scanner", "message", map[string]any{
		"freq_hz": tx.FreqHz, "label": tx.Label, "start": tx.Start, "duration_s": tx.DurationS,
		"peak_db": tx.PeakDB, "mode": e.p.mode})
	e.enter = t // dwell again before moving on: replies often follow
	e.wins[e.cur].activity = t
}
