// Package scanner watches a frequency range and points the listening VFO at
// carriers that newly appear (voice transmissions), see docs/scanner.md.
//
// Detection works on the full-resolution spectrum frames of the radio: a slow
// per-bin baseline per window, bins well above it are active, an active peak
// on a channel frequency starts a transmission. The scanner owns the radio like
// a survey (scan.Busy) and restores tuning, VFO and decoder when it stops.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/scan"
	"github.com/wardviaene/sweep/internal/survey"
)

// ErrBusy means a survey, scan or recording already owns the radio.
var ErrBusy = errors.New("another scan, recording or survey is running")

// ErrNotRunning is returned by Lockout when no scanner runs.
var ErrNotRunning = errors.New("scanner not running")

const (
	scanRate      = 2_400_000
	maxRecent     = 100
	maxChannels   = 50
	closedSquelch = 10.0 // dB: above any level the 8-bit IQ can reach, so the VFO stays silent
	customBand    = "custom"
)

// Request is the POST /api/scanner body. Zero fields take defaults.
type Request struct {
	Band        string   `json:"band"`
	Region      string   `json:"region"`
	LoHz        uint32   `json:"lo_hz"`
	HiHz        uint32   `json:"hi_hz"`
	Mode        string   `json:"mode"`
	BandwidthHz float64  `json:"bandwidth_hz"`
	StepHz      uint32   `json:"step_hz"`
	MarginDB    float64  `json:"margin_db"`
	HangS       float64  `json:"hang_s"`
	DwellMS     int      `json:"dwell_ms"`
	MaxTxS      float64  `json:"max_tx_s"`
	LockoutHz   []uint32 `json:"lockout_hz"`
	PriorityHz  []uint32 `json:"priority_hz"`
}

// params is a validated request with defaults applied.
type params struct {
	band       string // catalog id or customBand
	lo, hi     uint32
	mode       string
	bw         float64
	step       uint32
	margin     float64
	hang       time.Duration
	dwell      time.Duration
	maxTx      time.Duration
	priorityHz []uint32
}

// Window is the span currently watched.
type Window struct {
	CenterHz uint32 `json:"center_hz"`
	LoHz     uint32 `json:"lo_hz"`
	HiHz     uint32 `json:"hi_hz"`
	Index    int    `json:"index"`
	Count    int    `json:"count"`
}

// Active is the transmission being played.
type Active struct {
	FreqHz  uint32  `json:"freq_hz"`
	Label   string  `json:"label"`
	Since   float64 `json:"since"`
	LevelDB float64 `json:"level_db"`
}

// Tx is a finished transmission.
type Tx struct {
	FreqHz    uint32  `json:"freq_hz"`
	Label     string  `json:"label"`
	Start     float64 `json:"start"`
	DurationS float64 `json:"duration_s"`
	PeakDB    float64 `json:"peak_db"`
}

// Channel sums up the transmissions of one channel in this run.
type Channel struct {
	FreqHz uint32  `json:"freq_hz"`
	Label  string  `json:"label"`
	Count  int     `json:"count"`
	TotalS float64 `json:"total_s"`
	Last   float64 `json:"last"`
}

// State is the GET /api/scanner payload.
type State struct {
	State       string    `json:"state"` // idle | watching | locked | stopped
	Band        string    `json:"band,omitempty"`
	LoHz        uint32    `json:"lo_hz,omitempty"`
	HiHz        uint32    `json:"hi_hz,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	BandwidthHz float64   `json:"bandwidth_hz,omitempty"`
	StepHz      uint32    `json:"step_hz,omitempty"`
	MarginDB    float64   `json:"margin_db,omitempty"`
	HangS       float64   `json:"hang_s,omitempty"`
	DwellMS     int       `json:"dwell_ms,omitempty"`
	MaxTxS      float64   `json:"max_tx_s,omitempty"`
	Window      *Window   `json:"window,omitempty"`
	Active      *Active   `json:"active,omitempty"`
	Recent      []Tx      `json:"recent"`
	Channels    []Channel `json:"channels"`
	LockoutHz   []uint32  `json:"lockout_hz"`
	PriorityHz  []uint32  `json:"priority_hz"`
	Started     float64   `json:"started,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// Deps are what the scanner drives.
type Deps struct {
	Radio    *radio.Radio
	Decoders *decoder.Manager
	Config   *config.Store
	// Label names a channel frequency (catalog channel or satellite); optional.
	Label func(freqHz float64) string
	// Clip starts recording the VFO audio of a transmission for transcription;
	// the returned function ends it at the given time. Optional (may return nil).
	Clip func(freqHz uint32, label, mode string, start time.Time) func(end time.Time)
	// ClipIQ is Clip for a transmission that began before the VFO was pointed at
	// it: start is the back-dated start and bw the channel bandwidth, so the clip
	// can be demodulated from the radio's buffered IQ. It takes precedence over Clip.
	ClipIQ func(freqHz uint32, label, mode string, bw float64, start time.Time) func(end time.Time)
	// Buffering is called with true while the scanner runs and false after, so
	// the IQ history is only kept then. Optional.
	Buffering func(on bool)
}

// Scanner owns at most one scanner run at a time.
type Scanner struct {
	d Deps

	ctl sync.Mutex // serialises Start and Stop

	mu       sync.Mutex
	running  bool
	st       State
	p        params
	lockouts []uint32
	chans    map[uint32]*Channel
	cancel   context.CancelFunc
	done     chan struct{}
	pri      prior
}

// New creates an idle scanner.
func New(d Deps) *Scanner {
	return &Scanner{d: d, st: State{State: "idle"}}
}

// Active reports whether the scanner owns the radio.
func (s *Scanner) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Snapshot returns a copy of the state.
func (s *Scanner) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Scanner) snapshotLocked() State {
	st := s.st
	if st.Window != nil {
		w := *st.Window
		st.Window = &w
	}
	if st.Active != nil {
		a := *st.Active
		st.Active = &a
	}
	st.Recent = append([]Tx{}, st.Recent...)
	st.LockoutHz = append([]uint32{}, s.lockouts...)
	st.PriorityHz = append([]uint32{}, st.PriorityHz...)
	cs := make([]Channel, 0, len(s.chans))
	for _, c := range s.chans {
		cs = append(cs, *c)
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Count != cs[j].Count {
			return cs[i].Count > cs[j].Count
		}
		return cs[i].TotalS > cs[j].TotalS
	})
	if len(cs) > maxChannels {
		cs = cs[:maxChannels]
	}
	st.Channels = cs
	return st
}

func now() float64 { return float64(time.Now().UnixMilli()) / 1e3 }

func round(v float64, dec int) float64 {
	k := math.Pow(10, float64(dec))
	return math.Round(v*k) / k
}

// resolve validates a request and fills in defaults.
func resolve(req Request) (params, error) {
	var p params
	cat := survey.LoadCatalog()
	p.band = customBand
	lo, hi := float64(req.LoHz), float64(req.HiHz)
	var bandMode string
	if req.Band != "" {
		var b *survey.Band
		for i := range cat.Bands {
			if cat.Bands[i].ID == req.Band {
				b = &cat.Bands[i]
			}
		}
		if b == nil {
			return p, fmt.Errorf("unknown band %q (see GET /api/bands)", req.Band)
		}
		p.band = b.ID
		region := req.Region
		if region == "" {
			region = cat.DefaultRegion
		}
		blo, bhi := b.LoMHz, b.HiMHz
		if e, ok := b.Edges[region]; ok && len(e) == 2 {
			blo, bhi = e[0], e[1]
		}
		if req.LoHz == 0 {
			lo = blo * 1e6
		}
		if req.HiHz == 0 {
			hi = bhi * 1e6
		}
		bandMode = b.Mode
	}
	if lo == 0 || hi == 0 {
		return p, errors.New("give a band or lo_hz and hi_hz")
	}
	if lo < 24e6 || hi > 1766e6 {
		return p, errors.New("range must be within 24-1766 MHz")
	}
	if hi <= lo {
		return p, errors.New("hi_hz must be above lo_hz")
	}
	if hi-lo > 30e6 {
		return p, errors.New("range is at most 30 MHz wide")
	}
	p.lo, p.hi = uint32(math.Round(lo)), uint32(math.Round(hi))
	p.mode = req.Mode
	if p.mode == "" {
		p.mode = bandMode
	}
	switch p.mode {
	case "nfm", "am", "usb", "lsb":
	case "":
		return p, errors.New("mode is required (nfm, am, usb or lsb)")
	default:
		return p, fmt.Errorf("mode %q: want nfm, am, usb or lsb", p.mode)
	}
	p.step = req.StepHz
	if p.step == 0 {
		switch {
		case p.band == "pmr446":
			p.step = 6250
		case p.mode == "am":
			p.step = 25000
		case p.mode == "nfm":
			p.step = 12500
		default:
			p.step = 1000
		}
	}
	if p.step < 100 || p.step > 1_000_000 {
		return p, errors.New("step_hz must be 100..1000000")
	}
	p.bw = req.BandwidthHz
	if p.bw == 0 {
		p.bw = dsp.DefaultBandwidth(dsp.Mode(p.mode))
	}
	if p.bw < 500 || p.bw > 200000 {
		return p, errors.New("bandwidth_hz must be 500..200000")
	}
	p.margin = req.MarginDB
	if p.margin == 0 {
		p.margin = 10
	}
	if p.margin < 3 || p.margin > 40 {
		return p, errors.New("margin_db must be 3..40")
	}
	hang := req.HangS
	if hang == 0 {
		hang = 2
	}
	if hang < 0.2 || hang > 60 {
		return p, errors.New("hang_s must be 0.2..60")
	}
	p.hang = time.Duration(hang * float64(time.Second))
	dwell := req.DwellMS
	if dwell == 0 {
		dwell = 1500
	}
	if dwell < 200 || dwell > 30000 {
		return p, errors.New("dwell_ms must be 200..30000")
	}
	p.dwell = time.Duration(dwell) * time.Millisecond
	maxTx := req.MaxTxS
	if maxTx == 0 {
		maxTx = 120
	}
	if maxTx < 5 || maxTx > 3600 {
		return p, errors.New("max_tx_s must be 5..3600")
	}
	p.maxTx = time.Duration(maxTx * float64(time.Second))
	p.priorityHz = append([]uint32(nil), req.PriorityHz...)
	return p, nil
}

// Start starts the scanner, or reconfigures a running one (keeping the radio
// state saved by the first start).
func (s *Scanner) Start(req Request) (State, error) {
	p, err := resolve(req)
	if err != nil {
		return State{}, err
	}
	s.ctl.Lock()
	defer s.ctl.Unlock()

	s.mu.Lock()
	running, cancel, done := s.running, s.cancel, s.done
	s.mu.Unlock()
	if running {
		cancel()
		<-done
	} else {
		if !scan.Busy.TryLock() {
			return State{}, ErrBusy
		}
		d := s.d
		pr := prior{st: d.Radio.Status()}
		pr.decName, pr.decOpt, pr.decRun = d.Decoders.Current()
		d.Decoders.Stop()
		s.mu.Lock()
		s.pri = pr
		s.mu.Unlock()
	}

	// lockouts: the request's plus the remembered ones of this band
	lock := append([]uint32(nil), req.LockoutHz...)
	if s.d.Config != nil {
		lock = append(lock, s.d.Config.Get().ScannerLockouts[p.band]...)
	}
	slices.Sort(lock)
	lock = slices.Compact(lock)

	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	s.mu.Lock()
	started := s.st.Started
	if !running {
		started = now()
	}
	s.running, s.p, s.cancel, s.done = true, p, cancel, done
	s.lockouts = lock
	s.chans = map[uint32]*Channel{}
	s.st = State{State: "watching", Band: p.band, LoHz: p.lo, HiHz: p.hi, Mode: p.mode, BandwidthHz: p.bw,
		StepHz: p.step, MarginDB: p.margin, HangS: p.hang.Seconds(), DwellMS: int(p.dwell / time.Millisecond),
		MaxTxS: p.maxTx.Seconds(), Recent: []Tx{}, PriorityHz: p.priorityHz, Started: started}
	if s.st.Band == customBand {
		s.st.Band = ""
	}
	s.mu.Unlock()
	go s.loop(ctx, p, done)
	return s.Snapshot(), nil
}

// Stop ends the scanner, restores the radio and returns the final state.
func (s *Scanner) Stop() State {
	s.ctl.Lock()
	defer s.ctl.Unlock()
	s.mu.Lock()
	if !s.running {
		defer s.mu.Unlock()
		return s.snapshotLocked()
	}
	cancel, done, pr := s.cancel, s.done, s.pri
	s.mu.Unlock()
	cancel()
	<-done
	err := s.restore(pr)
	s.mu.Lock()
	s.running = false
	s.st.State = "stopped"
	s.st.Active = nil
	if err != nil {
		s.st.Error = err.Error()
	}
	st := s.snapshotLocked()
	s.mu.Unlock()
	scan.Busy.Unlock()
	return st
}

// Lockout adds (or removes) a locked-out channel on the running scanner and
// remembers it for the band in the config.
func (s *Scanner) Lockout(freqHz uint32, remove bool) (State, error) {
	if freqHz < 1_000_000 {
		return State{}, errors.New("freq_hz is required")
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return State{}, ErrNotRunning
	}
	if remove {
		s.lockouts = slices.DeleteFunc(s.lockouts, func(f uint32) bool { return f == freqHz })
	} else if !slices.Contains(s.lockouts, freqHz) {
		s.lockouts = append(s.lockouts, freqHz)
		slices.Sort(s.lockouts)
	}
	band := s.p.band
	list := append([]uint32(nil), s.lockouts...)
	s.mu.Unlock()
	if s.d.Config != nil {
		c := s.d.Config.Get()
		// the config holds the remembered list of the band: the running list is its superset
		if c.ScannerLockouts == nil {
			c.ScannerLockouts = map[string][]uint32{}
		}
		if len(list) == 0 {
			delete(c.ScannerLockouts, band)
		} else {
			c.ScannerLockouts[band] = list
		}
		if len(c.ScannerLockouts) == 0 {
			c.ScannerLockouts = nil
		}
		if err := s.d.Config.Set(c); err != nil {
			return s.Snapshot(), fmt.Errorf("lockout applied but not saved: %w", err)
		}
	}
	return s.Snapshot(), nil
}

func (s *Scanner) isLockedOut(ch uint32, step uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lockouts {
		if math.Abs(float64(l)-float64(ch)) <= float64(step)/2 {
			return true
		}
	}
	return false
}

// prior is the radio state a scanner restores.
type prior struct {
	st      radio.Status
	decName string
	decOpt  decoder.Options
	decRun  bool
}

// restore puts back the center, rate, gain, VFO and decoder from before the scanner.
func (s *Scanner) restore(p prior) error {
	d := s.d
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
	name := "none"
	if p.decRun {
		name = p.decName
	}
	if err := d.Decoders.Start(name, p.decOpt); err != nil {
		errs = append(errs, fmt.Errorf("restart %s: %w", name, err))
	}
	err := errors.Join(errs...)
	if err != nil {
		log.Printf("scanner: %v", err)
	}
	return err
}
