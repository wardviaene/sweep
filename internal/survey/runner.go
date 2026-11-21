package survey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/scan"
)

// ErrBusy means a survey, scan or recording already owns the radio.
var ErrBusy = errors.New("another scan, recording or survey is running")

// Request is the POST /api/survey body.
type Request struct {
	Antenna     string   `json:"antenna"`
	Region      string   `json:"region"`
	Seconds     int      `json:"seconds"` // per decoder step, 5..120, default 20
	IncludeFair bool     `json:"include_fair"`
	Steps       []string `json:"steps"`
	// Listen plays the strongest carrier of each voice band after its scan (default true).
	Listen *bool `json:"listen"`
	// ListenSeconds is the hold per band, 2..30, default 8.
	ListenSeconds int `json:"listen_seconds"`
}

// State is the GET /api/survey payload.
type State struct {
	ID         int     `json:"id"`
	State      string  `json:"state"` // idle | running | done | stopped | error
	Antenna    string  `json:"antenna,omitempty"`
	Region     string  `json:"region,omitempty"`
	Started    float64 `json:"started,omitempty"`
	ElapsedS   float64 `json:"elapsed_s"`
	EstimatedS int     `json:"estimated_s"`
	Error      string  `json:"error,omitempty"`
	Steps      []Step  `json:"steps"`
	// Listen and ListenSeconds echo the request; Listening is set during a listen hold.
	Listen        bool       `json:"listen"`
	ListenSeconds int        `json:"listen_seconds,omitempty"`
	Listening     *Listening `json:"listening,omitempty"`
}

// Deps are what a survey drives.
type Deps struct {
	Radio    *radio.Radio
	Spectrum scan.Spectrum
	Decoders *decoder.Manager
	Config   *config.Store
	// Label names a carrier frequency (catalog channel or visible satellite); optional.
	Label func(freqHz float64) string
}

const scanDwellMS = 500

// Runner owns at most one survey at a time and keeps the last one readable.
type Runner struct {
	d Deps

	mu     sync.Mutex
	st     State
	t0     time.Time
	end    time.Time
	cancel context.CancelFunc
	done   chan struct{}
	peaks  map[string][]dsp.Peak // band -> carriers found by its scan, strongest first
}

// New creates an idle runner.
func New(d Deps) *Runner {
	return &Runner{d: d, st: State{State: "idle", Steps: []Step{}}}
}

// Active reports whether a survey is running.
func (r *Runner) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.State == "running"
}

// Snapshot returns a copy of the current state.
func (r *Runner) Snapshot() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.st
	s.Steps = append([]Step(nil), s.Steps...)
	if !r.t0.IsZero() {
		end := r.end
		if s.State == "running" {
			end = time.Now()
		}
		s.ElapsedS = round1(end.Sub(r.t0).Seconds())
	}
	return s
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// Start validates the request, builds the plan and runs it in the background.
func (r *Runner) Start(req Request) (State, error) {
	cat := LoadCatalog()
	if req.Antenna == "" {
		if a := r.d.Config.Get().Antenna; a != nil {
			req.Antenna = *a
		} else {
			req.Antenna = "none"
		}
	}
	if req.Region == "" {
		req.Region = cat.DefaultRegion
	}
	if req.Seconds == 0 {
		req.Seconds = 20
	}
	if req.Seconds < 5 || req.Seconds > 120 {
		return State{}, errors.New("seconds must be 5..120")
	}
	listen := req.Listen == nil || *req.Listen
	if req.ListenSeconds == 0 {
		req.ListenSeconds = 8
	}
	if req.ListenSeconds < 2 || req.ListenSeconds > 30 {
		return State{}, errors.New("listen_seconds must be 2..30")
	}
	steps, err := buildPlan(cat, req.Antenna, req.Region, req.Seconds, req.IncludeFair, req.Steps)
	if err != nil {
		return State{}, err
	}
	if !scan.Busy.TryLock() {
		return State{}, ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.st = State{ID: r.st.ID + 1, State: "running", Antenna: req.Antenna, Region: req.Region,
		Started: float64(time.Now().UnixMilli()) / 1e3, Steps: steps,
		Listen: listen, ListenSeconds: req.ListenSeconds}
	r.st.EstimatedS = estimate(steps, r.d.Radio.Status().SampleRate, map[bool]int{true: req.ListenSeconds}[listen])
	r.t0, r.end, r.cancel, r.done = time.Now(), time.Time{}, cancel, make(chan struct{})
	r.peaks = map[string][]dsp.Peak{}
	done := r.done
	r.mu.Unlock()
	go func() {
		defer close(done)
		defer scan.Busy.Unlock()
		r.run(ctx)
	}()
	return r.Snapshot(), nil
}

// Stop ends the survey after the current step and returns the final state.
func (r *Runner) Stop() State {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	running := r.st.State == "running"
	r.mu.Unlock()
	if running {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
		}
	}
	return r.Snapshot()
}

// prior is the radio state a survey restores.
type prior struct {
	st      radio.Status
	decName string
	decOpt  decoder.Options
	decRun  bool
}

func (r *Runner) run(ctx context.Context) {
	d := r.d
	p := prior{st: d.Radio.Status()}
	p.decName, p.decOpt, p.decRun = d.Decoders.Current()
	d.Decoders.Stop()
	if r.Snapshot().Listen { // silence instead of the user's old VFO while scanning
		off := "off"
		if _, err := d.Radio.SetDemod(radio.DemodRequest{Mode: &off}); err != nil {
			log.Printf("survey: vfo off: %v", err)
		}
	}

	for i := range r.Snapshot().Steps {
		if ctx.Err() != nil {
			break
		}
		r.runStep(ctx, i, p)
	}
	err := r.restore(p)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.end = time.Now()
	switch {
	case err != nil:
		r.st.State, r.st.Error = "error", err.Error()
	case ctx.Err() != nil:
		r.st.State = "stopped"
	default:
		r.st.State = "done"
	}
}

// update modifies step i under the lock.
func (r *Runner) update(i int, f func(*Step)) Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.st.Steps[i])
	return r.st.Steps[i]
}

func (r *Runner) runStep(ctx context.Context, i int, p prior) {
	s := r.update(i, func(s *Step) {
		if s.State == "pending" {
			s.State = "running"
		}
	})
	t0 := time.Now()
	var err error
	var listenTime time.Duration
	if s.State == "running" {
		if s.Kind == "scan" {
			err = r.scanStep(ctx, i, p)
			if err == nil && r.Snapshot().Listen {
				l0 := time.Now()
				r.listenStep(ctx, i)
				listenTime = time.Since(l0)
			}
		} else {
			err = r.decoderStep(ctx, i)
		}
	}
	s = r.update(i, func(s *Step) {
		switch {
		case s.State == "skipped":
		case err != nil && ctx.Err() != nil && s.Kind == "scan":
			s.State, s.Reason = "skipped", "stopped"
		case err != nil:
			s.State, s.Error = "error", err.Error()
		default:
			s.State = "done"
		}
		if s.State != "skipped" {
			s.Seconds = round1((time.Since(t0) - listenTime).Seconds())
		}
	})
	line := s.Label + ": " + s.Summary
	switch s.State {
	case "skipped":
		line = s.Label + ": skipped, " + s.Reason
	case "error":
		line = s.Label + ": error, " + s.Error
	}
	r.d.Decoders.Events().Publish("survey", "log", map[string]string{"line": line})
}

func (r *Runner) skip(i int, reason string) error {
	r.update(i, func(s *Step) { s.State, s.Reason = "skipped", reason })
	return nil
}

func (r *Runner) scanStep(ctx context.Context, i int, p prior) error {
	d := r.d
	s := r.Snapshot().Steps[i]
	if d.Radio.Status().SampleRate != p.st.SampleRate { // a decoder step may have changed it
		if _, err := d.Radio.Tune(radio.TuneRequest{SampleRate: &p.st.SampleRate}); err != nil {
			return err
		}
	}
	res, err := scan.Sweep(ctx, d.Radio, d.Spectrum, scan.Request{StartHz: s.FreqLoHz, StopHz: s.FreqHiHz, DwellMS: scanDwellMS, MinSNR: 10})
	if err != nil {
		return err
	}
	cs := carriers(res.Peaks)
	r.mu.Lock()
	r.peaks[s.Band] = cs
	r.mu.Unlock()
	if d.Label != nil {
		cs = append([]dsp.Peak(nil), cs...)
		for i := range cs {
			cs[i].Label = d.Label(cs[i].FreqHz)
		}
	}
	found, summary, peaks := scanResult(cs)
	r.update(i, func(s *Step) { s.Found, s.Summary, s.Peaks = &found, summary, peaks })
	return nil
}

// listenStep plays the strongest carrier found by scan step i for the hold time
// and records what was heard. The VFO is switched off again afterwards.
func (r *Runner) listenStep(ctx context.Context, i int) {
	d := r.d
	s := r.Snapshot().Steps[i]
	r.mu.Lock()
	cs := r.peaks[s.Band]
	hold := r.st.ListenSeconds
	r.mu.Unlock()
	if s.mode == "" || len(cs) == 0 {
		return
	}
	best := cs[0]
	for _, c := range cs {
		if c.DB > best.DB {
			best = c
		}
	}
	freq := uint32(math.Round(best.FreqHz/s.grid) * s.grid)
	mode := s.mode
	if _, err := d.Radio.SetDemod(radio.DemodRequest{Mode: &mode, FreqHz: &freq, SetSquelch: true}); err != nil {
		log.Printf("survey: listen %d Hz: %v", freq, err)
		return
	}
	defer func() {
		off := "off"
		if _, err := d.Radio.SetDemod(radio.DemodRequest{Mode: &off}); err != nil {
			log.Printf("survey: vfo off: %v", err)
		}
		r.mu.Lock()
		r.st.Listening = nil
		r.mu.Unlock()
	}()
	var label string
	if d.Label != nil {
		label = d.Label(best.FreqHz)
	}
	t0 := time.Now()
	r.mu.Lock()
	r.st.Listening = &Listening{Step: s.ID, FreqHz: freq, Mode: mode, Label: label, Seconds: hold,
		Started: float64(t0.UnixMilli()) / 1e3}
	r.mu.Unlock()
	var sum float64
	var n int
	sample := func() {
		sum += d.Radio.Status().Demod.LevelDB
		n++
	}
	deadline := time.NewTimer(time.Duration(hold) * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for done := false; !done; {
		select {
		case <-ctx.Done():
			done = true
		case <-deadline.C:
			done = true
		case <-tick.C:
			sample()
		}
	}
	if n == 0 {
		sample()
	}
	r.update(i, func(s *Step) {
		s.Listened = &Listened{FreqHz: freq, Mode: mode, Seconds: round1(time.Since(t0).Seconds()),
			LevelDB: round1(sum / float64(n)), Label: label}
	})
}

func (r *Runner) decoderStep(ctx context.Context, i int) error {
	d := r.d
	s := r.Snapshot().Steps[i]
	opt := s.opt
	if s.Decoder == "dsd" {
		r.mu.Lock()
		cs := r.peaks[s.Band]
		r.mu.Unlock()
		if len(cs) == 0 {
			return r.skip(i, "no carrier found by scan:"+s.Band)
		}
		best := cs[0]
		for _, c := range cs {
			if c.DB > best.DB {
				best = c
			}
		}
		opt.FreqHz = uint32(best.FreqHz + 0.5)
		r.update(i, func(s *Step) {
			s.FreqHz = opt.FreqHz
			s.Label = decoderLabel("dsd", opt.FreqHz, Band{})
		})
	}
	if s.tuneHz != 0 {
		if _, err := d.Radio.Tune(radio.TuneRequest{FreqHz: &s.tuneHz}); err != nil {
			return err
		}
	}
	ch, cancel := d.Decoders.Events().Subscribe(0)
	defer cancel()
	col := newCollector(s.Decoder)
	defer d.Decoders.Stop()
	if err := d.Decoders.Start(s.Decoder, opt); err != nil {
		return err
	}
	if f := d.Decoders.Status().FreqHz; f != 0 {
		r.update(i, func(s *Step) { s.FreqHz = f })
	}
	t0 := time.Now()
	deadline := time.NewTimer(time.Duration(s.secs) * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	publish := func() {
		found, summary, samples := col.result()
		r.update(i, func(s *Step) {
			s.Found, s.Summary, s.Samples, s.Seconds = &found, summary, samples, round1(time.Since(t0).Seconds())
		})
	}
	for {
		select {
		case <-ctx.Done():
		case <-deadline.C:
		case b := <-ch:
			var ev decoder.Event
			if json.Unmarshal(b, &ev) == nil {
				col.add(ev)
			}
			continue
		case <-tick.C:
			if st := d.Decoders.Status(); !st.Running {
				if st.Error != "" {
					return errors.New(st.Error)
				}
				publish()
				return nil
			}
			publish()
			continue
		}
		break
	}
	if st := d.Decoders.Status(); !st.Running && st.Error != "" {
		return errors.New(st.Error)
	}
	d.Decoders.Stop()
	for { // events published just before the stop
		select {
		case b := <-ch:
			var ev decoder.Event
			if json.Unmarshal(b, &ev) == nil {
				col.add(ev)
			}
			continue
		default:
		}
		break
	}
	publish()
	return nil
}

// restore puts back the center, rate, gain, VFO and decoder from before the survey.
func (r *Runner) restore(p prior) error {
	d := r.d
	r.d.Decoders.Stop()
	var errs []error
	if _, err := d.Radio.Tune(radio.TuneRequest{FreqHz: &p.st.CenterHz, SampleRate: &p.st.SampleRate, SetGain: true, GainDB: p.st.GainDB}); err != nil {
		errs = append(errs, fmt.Errorf("restore tuning: %w", err))
	}
	dm := p.st.Demod
	req := radio.DemodRequest{Mode: &dm.Mode, SetSquelch: true, SquelchDB: dm.SquelchDB}
	if dm.Mode != "off" {
		req.FreqHz = &dm.FreqHz
		if dm.BandwidthHz > 0 {
			req.BandwidthHz = &dm.BandwidthHz
		}
	}
	if _, err := d.Radio.SetDemod(req); err != nil {
		errs = append(errs, fmt.Errorf("restore demod: %w", err))
	}
	name := "none" // also forgets the last survey decoder in the status
	if p.decRun {
		name = p.decName
	}
	if err := d.Decoders.Start(name, p.decOpt); err != nil {
		errs = append(errs, fmt.Errorf("restart %s: %w", name, err))
	}
	err := errors.Join(errs...)
	if err != nil {
		log.Printf("survey: %v", err)
	}
	return err
}
