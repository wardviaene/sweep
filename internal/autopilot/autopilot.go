// Package autopilot handles satellite passes by itself: it plans upcoming
// passes from the metadata datasets, takes the radio at AOS-30 s, decodes or
// records the downlink with Doppler correction and gives the radio back at
// LOS+10 s. See docs/autopilot.md.
package autopilot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/meta"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/scan"
)

// Errors returned by Run.
var (
	ErrBusy       = errors.New("radio busy")
	ErrNotVisible = errors.New("satellite is not above the horizon")
	ErrNoDownlink = errors.New("satellite has no downlink sdrd can use")
	ErrNoSource   = errors.New("no satellite data (set the receiver location and fetch the metadata)")
)

// busyError is a skip reason that matches ErrBusy.
type busyError string

func (e busyError) Error() string        { return string(e) }
func (e busyError) Is(target error) bool { return target == ErrBusy }

// Radio is the part of *radio.Radio the autopilot drives.
type Radio interface {
	Status() radio.Status
	Tune(radio.TuneRequest) (radio.Status, error)
	SetDemod(radio.DemodRequest) (radio.Status, error)
	AddAudioTap(freqHz uint32, mode dsp.Mode, bw float64, raw bool) (id int, pcm <-chan []int16, rate int)
	AddIQTap() (id int, iq <-chan []byte)
	RemoveTap(id int)
	SetTapFreq(id int, freqHz uint32) error
}

// Decoders is the part of *decoder.Manager the autopilot drives.
type Decoders interface {
	Start(name string, opt decoder.Options) error
	Stop()
	Current() (name string, opt decoder.Options, ok bool)
	Status() decoder.Status
	RetuneChannel(hz uint32) error
	Events() *decoder.Events
}

// Source provides passes and live look angles; *meta.DB implements it.
type Source interface {
	Passes(hours int, minEl float64) ([]meta.Pass, bool)
	Overhead() ([]meta.OverheadSat, bool)
}

// Deps are what the autopilot needs.
type Deps struct {
	Radio    Radio
	Decoders Decoders
	Source   Source
	Config   *config.Store
	// DataDir holds passes/<dir>/ with the recordings and pass.json.
	DataDir string

	// StopOthers stops a running survey and scanner (used with preempt). Optional.
	StopOthers func()
	// BusyWhy names what holds the radio, for skip reasons. Optional.
	BusyWhy func() string
	// DatasetsOK reports whether the satellite datasets are loaded. Optional.
	DatasetsOK func() bool

	// Now is the clock (default time.Now). Timers use real time.
	Now func() time.Time
	// Tick is the Doppler and scheduling interval (default 1 s).
	Tick time.Duration
	// PlanEvery is how often the plan is recomputed (default 1 min).
	PlanEvery time.Duration
	// Lead and Tail are the time before AOS and after LOS the radio is held (30 s, 10 s).
	Lead, Tail time.Duration
}

// Autopilot is the pass scheduler. Create it with New and start it with Start.
type Autopilot struct {
	d Deps

	mu      sync.Mutex
	plan    []Planned
	history []Entry
	handled map[string]bool
	cur     *curPass
	started bool

	kick   chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates an autopilot and reads the pass history from disk.
func New(d Deps) *Autopilot {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Tick <= 0 {
		d.Tick = time.Second
	}
	if d.PlanEvery <= 0 {
		d.PlanEvery = time.Minute
	}
	if d.Lead == 0 {
		d.Lead = 30 * time.Second
	}
	if d.Tail == 0 {
		d.Tail = 10 * time.Second
	}
	a := &Autopilot{d: d, handled: map[string]bool{}, kick: make(chan struct{}, 1)}
	a.history = loadHistory(a.passesDir(), historyLen)
	return a
}

func (a *Autopilot) passesDir() string { return filepath.Join(a.d.DataDir, "passes") }

// Start runs the scheduler until Close.
func (a *Autopilot) Start() {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return
	}
	a.started = true
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.loop(ctx)
	}()
}

// Close stops the scheduler and ends a running pass.
func (a *Autopilot) Close() {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.Stop()
	a.wg.Wait()
}

// Kick makes the scheduler recompute its plan now (after a settings change).
func (a *Autopilot) Kick() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// Active reports whether the autopilot owns the radio (AOS-30 s to LOS+10 s).
func (a *Autopilot) Active() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur != nil
}

func (a *Autopilot) now() time.Time { return a.d.Now() }

func (a *Autopilot) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	log.Printf("autopilot: %s", line)
	if ev := a.d.Decoders.Events(); ev != nil {
		ev.Publish("autopilot", "log", map[string]string{"line": line})
	}
}

type cfgKey struct {
	AP  config.Autopilot
	Rcv *config.Receiver
}

func settingsKey(c config.Config) cfgKey {
	return cfgKey{c.AutopilotSettings(), c.Receiver}
}

func (a *Autopilot) loop(ctx context.Context) {
	tk := time.NewTicker(a.d.Tick)
	defer tk.Stop()
	var last cfgKey
	var lastPlan time.Time
	first := true
	for {
		cfg := a.d.Config.Get()
		ap := cfg.AutopilotSettings()
		if k := settingsKey(cfg); first || !reflect.DeepEqual(k, last) || a.now().Sub(lastPlan) >= a.d.PlanEvery {
			last, first, lastPlan = k, false, a.now()
			a.replan(ap)
		}
		if ap.Enabled {
			a.startDue(ap)
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		case <-a.kick:
			first = true
		}
	}
}

// replan recomputes the plan for the next 12 h.
func (a *Autopilot) replan(ap config.Autopilot) {
	var plan []Planned
	if a.d.Source != nil {
		if passes, ok := a.d.Source.Passes(12, ap.MinEl()); ok {
			plan = buildPlan(passes, ap, a.d.Lead, a.d.Tail)
		}
	}
	a.mu.Lock()
	a.plan = plan
	a.mu.Unlock()
}

// startDue starts the first planned pass whose window has begun and logs the
// planned passes that are skipped.
func (a *Autopilot) startDue(ap config.Autopilot) {
	now := a.now().Unix()
	lead := int64(a.d.Lead / time.Second)
	a.mu.Lock()
	plan := append([]Planned(nil), a.plan...)
	a.mu.Unlock()
	for _, pl := range plan {
		p := pl.Pass
		key := passKey(p)
		if now < p.AOS-lead || now >= p.LOS {
			continue
		}
		a.mu.Lock()
		done := a.handled[key]
		a.handled[key] = true
		a.mu.Unlock()
		if done {
			continue
		}
		if !pl.WillRun {
			a.logf("skip %s (max elevation %.0f°): %s", p.Name, p.MaxElevDeg, pl.Reason)
			continue
		}
		if reason := a.begin(pl, ap, false); reason != "" {
			a.logf("skip %s (max elevation %.0f°): %s", p.Name, p.MaxElevDeg, reason)
			a.mu.Lock()
			for j := range a.plan {
				if passKey(a.plan[j].Pass) == key {
					a.plan[j].WillRun, a.plan[j].Reason = false, reason
				}
			}
			a.mu.Unlock()
		}
	}
}

// acquire takes the radio for a pass. It returns a skip reason when it
// cannot. The caller holds scan.Busy on success.
func (a *Autopilot) acquire(ap config.Autopilot) string {
	if a.Active() {
		return "autopilot is handling another pass"
	}
	if !scan.Busy.TryLock() {
		if !ap.Preempt {
			why := "survey, scanner, scan or recording running"
			if a.d.BusyWhy != nil {
				if w := a.d.BusyWhy(); w != "" {
					why = w + " running"
				}
			}
			return "radio busy: " + why
		}
		if a.d.StopOthers != nil {
			a.d.StopOthers()
		}
		ok := false
		for i := 0; i < 100 && !ok; i++ { // a scan or recording is short
			if ok = scan.Busy.TryLock(); !ok {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if !ok {
			return "radio busy: scan or recording did not finish"
		}
	}
	if st := a.d.Decoders.Status(); st.Running && !ap.Preempt {
		scan.Busy.Unlock()
		return "radio busy: decoder " + st.Name + " running"
	}
	return ""
}

// begin acquires the radio and starts handling the pass in the background.
// It returns the skip reason, or "" when the pass was started.
func (a *Autopilot) begin(pl Planned, ap config.Autopilot, manual bool) string {
	if reason := a.acquire(ap); reason != "" {
		return reason
	}
	p := pl.Pass
	ctx, cancel := context.WithCancel(context.Background())
	c := &curPass{pl: pl, ap: ap, manual: manual, cancel: cancel, done: make(chan struct{})}
	c.prior.st = a.d.Radio.Status()
	c.prior.decName, c.prior.decOpt, c.prior.decRun = a.d.Decoders.Current()
	a.mu.Lock()
	a.cur = c
	a.handled[passKey(p)] = true
	a.mu.Unlock()
	go func() {
		defer close(c.done)
		a.runPass(ctx, c)
	}()
	return ""
}

// Run handles the current pass of satellite norad right now, whatever its
// elevation. It is for trying things out.
func (a *Autopilot) Run(norad int) (Current, error) {
	if a.d.Source == nil {
		return Current{}, ErrNoSource
	}
	now := a.now()
	var found *meta.Pass
	if passes, ok := a.d.Source.Passes(1, 0); ok {
		for _, p := range passes {
			if p.Norad == norad && p.AOS <= now.Unix() && p.LOS > now.Unix() {
				p := p
				found = &p
				break
			}
		}
	}
	if found == nil {
		sats, _ := a.d.Source.Overhead()
		for _, s := range sats {
			if s.Norad == norad {
				found = &meta.Pass{Norad: s.Norad, Name: s.Name, AOS: now.Unix(), LOS: now.Add(10 * time.Minute).Unix(),
					MaxElevDeg: s.ElevationD, MaxTime: now.Unix(), Downlinks: s.Downlinks}
			}
		}
	}
	if found == nil {
		return Current{}, ErrNotVisible
	}
	ch, ok := choose(*found)
	if !ok {
		return Current{}, ErrNoDownlink
	}
	ap := a.d.Config.Get().AutopilotSettings()
	if reason := a.begin(Planned{Pass: *found, Choice: ch, WillRun: true}, ap, true); reason != "" {
		return Current{}, busyError(reason)
	}
	st := a.Snapshot()
	if st.Current == nil {
		return Current{}, ErrBusy
	}
	return *st.Current, nil
}

// Stop ends the current pass early (history state "stopped") and waits for the
// radio to be restored.
func (a *Autopilot) Stop() {
	a.mu.Lock()
	c := a.cur
	a.mu.Unlock()
	if c == nil {
		return
	}
	c.cancel()
	select {
	case <-c.done:
	case <-time.After(30 * time.Second):
	}
}

// Current is the state API's "current" object.
type Current struct {
	Norad      int     `json:"norad"`
	Name       string  `json:"name"`
	AOS        int64   `json:"aos"`
	LOS        int64   `json:"los"`
	MaxElev    float64 `json:"max_elevation_deg"`
	DownlinkHz int64   `json:"downlink_hz"`
	Decoder    string  `json:"decoder"`
	DopplerHz  int     `json:"doppler_hz"`
	ElevDeg    float64 `json:"elevation_deg"`
	AzDeg      float64 `json:"azimuth_deg"`
	Packets    int     `json:"packets"`
	Images     int     `json:"images"`
	Dir        string  `json:"dir"`
}

// Next is one upcoming pass of the state API.
type Next struct {
	Norad      int     `json:"norad"`
	Name       string  `json:"name"`
	AOS        int64   `json:"aos"`
	LOS        int64   `json:"los"`
	MaxElev    float64 `json:"max_elevation_deg"`
	DownlinkHz int64   `json:"downlink_hz"`
	Decoder    string  `json:"decoder"`
	Baud       int     `json:"baud,omitempty"`
	WillRun    bool    `json:"will_run"`
	Reason     string  `json:"reason"`
}

// State is the GET /api/autopilot payload.
type State struct {
	Enabled      bool     `json:"enabled"`
	MinElevation float64  `json:"min_elevation"`
	Satellites   []int    `json:"satellites"`
	Preempt      bool     `json:"preempt"`
	Listen       bool     `json:"listen"`
	RecordIQ     bool     `json:"record_iq"`
	ReceiverSet  bool     `json:"receiver_set"`
	DatasetsOK   bool     `json:"datasets_ok"`
	Current      *Current `json:"current"`
	Next         []Next   `json:"next"`
	History      []Entry  `json:"history"`
}

// Snapshot returns the settings, the current pass, the next 12 h and the history.
func (a *Autopilot) Snapshot() State {
	cfg := a.d.Config.Get()
	ap := cfg.AutopilotSettings()
	st := State{Enabled: ap.Enabled, MinElevation: ap.MinEl(), Satellites: ap.Satellites, Preempt: ap.Preempt,
		Listen: ap.ListenOn(), RecordIQ: ap.RecordIQ, ReceiverSet: cfg.Receiver != nil, DatasetsOK: true}
	if a.d.DatasetsOK != nil {
		st.DatasetsOK = a.d.DatasetsOK()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur != nil {
		c := a.cur.current()
		st.Current = &c
	}
	st.Next = []Next{}
	now := a.now().Unix()
	for _, pl := range a.plan {
		if pl.Pass.LOS <= now {
			continue
		}
		p := pl.Pass
		reason := pl.Reason
		if a.cur != nil && passKey(a.cur.pl.Pass) == passKey(p) {
			continue // shown as current
		}
		st.Next = append(st.Next, Next{Norad: p.Norad, Name: p.Name, AOS: p.AOS, LOS: p.LOS, MaxElev: p.MaxElevDeg,
			DownlinkHz: pl.Choice.DownlinkHz, Decoder: pl.Choice.Decoder(), Baud: pl.Choice.Baud, WillRun: pl.WillRun, Reason: reason})
	}
	st.History = append([]Entry{}, a.history...)
	return st
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
