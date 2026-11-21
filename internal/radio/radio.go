// Package radio owns the IQ source, the tuning state and the spectrum pipeline.
package radio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/rtltcp"
	"github.com/wardviaene/sweep/internal/source"
)

// Source is an IQ stream that can be tuned.
type Source interface {
	io.Reader
	SetFreq(hz uint32) error
	SetSampleRate(sps uint32) error
	SetGain(db *float64) error
	Close() error
}

// Gain tables per tuner (dB), as reported by librtlsdr.
var gainTables = map[string][]float64{
	"R820T": {0.0, 0.9, 1.4, 2.7, 3.7, 7.7, 8.7, 12.5, 14.4, 15.7, 16.6, 19.7, 20.7, 22.9, 25.4,
		28.0, 29.7, 32.8, 33.8, 36.4, 37.2, 38.6, 40.2, 42.1, 43.4, 43.9, 44.5, 48.0, 49.6},
}

func init() { gainTables["R828D"] = gainTables["R820T"] }

// Config holds startup settings.
type Config struct {
	Synth      bool      // use the synthetic source instead of rtl_tcp
	SynthFM    []SynthFM // extra FM signals for the synthetic source
	File       string    // play back a cu8 recording instead of the dongle
	RtlTcpBin  string    // path to rtl_tcp
	RtlTcpPort int
	CenterHz   uint32
	SampleRate uint32
	GainDB     *float64 // nil = auto
	FFTSize    int
	FPS        int
	DirectQ    uint32 // direct-sampling branch for HF: 1 = I, 2 = Q
	MinDB      float64
	MaxDB      float64
}

// SynthFM is a WAV file FM-modulated onto a frequency by the synthetic source.
type SynthFM struct {
	FreqHz float64
	PCM    []int16
	Rate   float64
}

// Meta describes the spectrum frames.
type Meta struct {
	Type       string  `json:"type"`
	CenterHz   uint32  `json:"center_hz"`
	SampleRate uint32  `json:"sample_rate"`
	Bins       int     `json:"bins"`
	MinDB      float64 `json:"min_db"`
	MaxDB      float64 `json:"max_db"`
	FPS        int     `json:"fps"`
}

// Status is the GET /api/status payload.
type Status struct {
	Device     string    `json:"device"`
	Tuner      string    `json:"tuner"`
	Gains      []float64 `json:"gains_db"`
	CenterHz   uint32    `json:"center_hz"`
	SampleRate uint32    `json:"sample_rate"`
	GainDB     *float64  `json:"gain_db"`
	FFTSize    int       `json:"fft_size"`
	FPS        int       `json:"fps"`
	Mode       string    `json:"mode"`
	Direct     bool      `json:"direct_sampling"` // HF via direct sampling (below 24 MHz)
	Demod      DemodInfo `json:"demod"`
	Decoder    any       `json:"decoder"`
	Connected  bool      `json:"connected"`
	Error      string    `json:"error,omitempty"`
}

// TuneRequest is a tuning change. Nil fields are unchanged. When SetGain is
// true, GainDB is applied (nil = automatic gain).
type TuneRequest struct {
	FreqHz     *uint32
	SampleRate *uint32
	SetGain    bool
	GainDB     *float64
}

// UnmarshalJSON decodes {"freq_hz","sample_rate","gain_db"}, where "gain_db": null
// means auto gain and an absent "gain_db" leaves the gain unchanged.
func (t *TuneRequest) UnmarshalJSON(b []byte) error {
	var raw struct {
		FreqHz     *uint32         `json:"freq_hz"`
		SampleRate *uint32         `json:"sample_rate"`
		GainDB     json.RawMessage `json:"gain_db"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	t.FreqHz, t.SampleRate = raw.FreqHz, raw.SampleRate
	if raw.GainDB != nil {
		t.SetGain = true
		if string(raw.GainDB) != "null" {
			var g float64
			if err := json.Unmarshal(raw.GainDB, &g); err != nil {
				return fmt.Errorf("gain_db: %w", err)
			}
			t.GainDB = &g
		}
	}
	return nil
}

// Sink receives spectrum output.
type Sink interface {
	Meta(Meta)
	Frame([]byte)
}

// Radio is the single owner of the IQ source.
type Radio struct {
	cfg   Config
	sink  Sink
	audio AudioSink

	mu      sync.Mutex
	src     Source
	st      Status
	spec    *dsp.Spectrum
	skip    int // bytes to discard after a retune
	lastErr string
	frames  chan []float64 // completed spectra, paced out by emit
	pool    sync.Pool

	demod     *dsp.Demod // listening VFO, nil when off
	dm        DemodInfo
	audioBuf  []int16
	lastLevel time.Time
	taps      map[int]*tap // IQ / audio consumers (decoders)
	nextTap   int
	decoderFn func() any // status of the decoder manager, set by SetDecoderStatus
}

// New creates a radio; call Run to start it.
func New(cfg Config, sink Sink, audio AudioSink) *Radio {
	if cfg.DirectQ == 0 {
		cfg.DirectQ = 2
	}
	return &Radio{
		cfg:    cfg,
		sink:   sink,
		audio:  audio,
		dm:     DemodInfo{Mode: "off"},
		taps:   map[int]*tap{},
		spec:   dsp.NewSpectrum(cfg.FFTSize),
		frames: make(chan []float64, 4),
		st: Status{
			CenterHz:   cfg.CenterHz,
			Direct:     cfg.CenterHz < hfLimit,
			SampleRate: cfg.SampleRate,
			GainDB:     cfg.GainDB,
			FFTSize:    cfg.FFTSize,
			FPS:        cfg.FPS,
			Mode:       "spectrum",
		},
	}
}

// Meta returns the current frame description.
func (r *Radio) Meta() Meta {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metaLocked()
}

func (r *Radio) metaLocked() Meta {
	return Meta{Type: "meta", CenterHz: r.st.CenterHz, SampleRate: r.st.SampleRate,
		Bins: r.cfg.FFTSize, MinDB: r.cfg.MinDB, MaxDB: r.cfg.MaxDB, FPS: r.cfg.FPS}
}

// Status returns a snapshot of the radio state.
func (r *Radio) Status() Status {
	r.mu.Lock()
	s := r.st
	s.Error = r.lastErr
	s.Demod = r.dm
	fn := r.decoderFn
	r.mu.Unlock()
	if fn != nil {
		s.Decoder = fn()
	}
	return s
}

// Tune applies a tuning request.
func (r *Radio) Tune(req TuneRequest) (Status, error) {
	r.mu.Lock()
	if r.cfg.File != "" && (req.FreqHz != nil || req.SampleRate != nil) {
		r.mu.Unlock()
		return Status{}, fmt.Errorf("playing a recording: tuning is disabled")
	}
	if req.FreqHz != nil {
		if f := *req.FreqHz; f < 500_000 || f > 1_766_000_000 {
			r.mu.Unlock()
			return Status{}, fmt.Errorf("freq_hz %d out of range (0.5-24 MHz via direct sampling, 24-1766 MHz via the tuner)", f)
		}
	}
	if req.SampleRate != nil {
		sr := *req.SampleRate
		if !(sr > 225_000 && sr <= 300_000) && !(sr > 900_000 && sr <= 3_200_000) {
			r.mu.Unlock()
			return Status{}, fmt.Errorf("sample_rate %d invalid (225-300 kS/s or 0.9-3.2 MS/s; 2.4 MS/s is the reliable max)", sr)
		}
	}
	if req.GainDB != nil {
		g := snapGain(r.st.Gains, *req.GainDB)
		req.GainDB = &g
	}
	src := r.src
	if src != nil {
		var err error
		if req.SampleRate != nil {
			err = errors.Join(err, src.SetSampleRate(*req.SampleRate))
		}
		if req.FreqHz != nil {
			if direct := *req.FreqHz < hfLimit; direct != r.st.Direct {
				mode := uint32(0)
				if direct {
					mode = r.cfg.DirectQ
				}
				if ds, ok := src.(interface{ SetDirectSampling(uint32) error }); ok {
					err = errors.Join(err, ds.SetDirectSampling(mode))
				}
			}
			err = errors.Join(err, src.SetFreq(*req.FreqHz))
		}
		if req.SetGain {
			err = errors.Join(err, src.SetGain(req.GainDB))
		}
		if err != nil {
			r.mu.Unlock()
			return Status{}, err
		}
	}
	if req.FreqHz != nil {
		r.st.CenterHz = *req.FreqHz
		r.st.Direct = *req.FreqHz < hfLimit
	}
	if req.SampleRate != nil {
		r.st.SampleRate = *req.SampleRate
	}
	r.retuneDemodLocked(req.SampleRate != nil)
	if req.SetGain {
		r.st.GainDB = req.GainDB
	}
	r.spec.Reset()
	for len(r.frames) > 0 {
		r.pool.Put(<-r.frames)
	}
	r.skip = int(r.st.SampleRate) / 10 // drop ~50 ms of samples (2 bytes each) from before the retune
	meta := r.metaLocked()
	r.mu.Unlock()
	r.sink.Meta(meta)
	return r.Status(), nil
}

func snapGain(table []float64, g float64) float64 {
	if len(table) == 0 {
		return g
	}
	best := table[0]
	for _, v := range table {
		if math.Abs(v-g) < math.Abs(best-g) {
			best = v
		}
	}
	return best
}

// Run opens the source and streams until ctx is cancelled, reconnecting on failure.
func (r *Radio) Run(ctx context.Context) {
	go r.emit(ctx)
	backoff := time.Second
	for ctx.Err() == nil {
		err := r.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("radio: %v; retrying in %s", err, backoff)
		r.mu.Lock()
		r.st.Connected = false
		r.lastErr = err.Error()
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

func (r *Radio) runOnce(ctx context.Context) error {
	r.mu.Lock()
	center, sr := r.st.CenterHz, r.st.SampleRate
	r.mu.Unlock()

	var src Source
	var device, tuner string
	var proc *rtltcp.Process
	if r.cfg.File != "" {
		f, err := source.OpenFile(r.cfg.File, sr)
		if err != nil {
			return err
		}
		src = f
		device, tuner = "recording: "+filepath.Base(r.cfg.File), "R820T"
	} else if r.cfg.Synth {
		syn := source.NewSynth(center, sr)
		for _, f := range r.cfg.SynthFM {
			syn.AddFM(f.FreqHz, f.PCM, f.Rate, 3000, 2)
		}
		src = syn
		device, tuner = "synthetic", "R820T"
	} else {
		p, c, err := rtltcp.Start(ctx, r.cfg.RtlTcpBin, r.cfg.RtlTcpPort, center, sr)
		if err != nil {
			return err
		}
		proc = p
		defer p.Stop()
		src = c
		device, tuner = p.Device(), c.Header.TunerName()
	}
	defer src.Close()

	r.mu.Lock()
	// A Tune may have landed while the source was starting: apply the current state.
	if r.st.Direct {
		if ds, ok := src.(interface{ SetDirectSampling(uint32) error }); ok {
			_ = ds.SetDirectSampling(r.cfg.DirectQ)
		}
		_ = src.SetFreq(r.st.CenterHz)
	} else if r.st.CenterHz != center {
		_ = src.SetFreq(r.st.CenterHz)
	}
	if r.st.SampleRate != sr {
		_ = src.SetSampleRate(r.st.SampleRate)
	}
	if err := src.SetGain(r.st.GainDB); err != nil {
		r.mu.Unlock()
		return err
	}
	r.src = src
	r.st.Device, r.st.Tuner, r.st.Gains = device, tuner, gainTables[tuner]
	r.st.Connected = true
	r.lastErr = ""
	r.spec.Reset()
	meta := r.metaLocked()
	r.mu.Unlock()
	r.sink.Meta(meta)
	log.Printf("radio: connected to %s (%s)", device, tuner)

	defer func() {
		r.mu.Lock()
		r.src = nil
		r.mu.Unlock()
	}()

	// Closing the source unblocks Read when ctx ends or rtl_tcp dies.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		var procDone <-chan struct{}
		if proc != nil {
			procDone = proc.Done()
		}
		select {
		case <-ctx.Done():
		case <-procDone:
		case <-stop:
			return
		}
		src.Close()
	}()

	buf := make([]byte, 1<<16)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			r.mu.Lock()
			b := buf[:n]
			if r.skip > 0 {
				d := min(r.skip, len(b))
				r.skip -= d
				b = b[d:]
			}
			r.processLocked(b)
			// rtl_tcp delivers IQ in ~64 ms bursts, so frames are cut by sample
			// count (one frame's worth of FFTs) and emit paces them evenly.
			block := 2 * r.cfg.FFTSize
			for len(b) > 0 {
				c := min(block, len(b))
				r.spec.Write(b[:c])
				b = b[c:]
				if r.spec.Count() >= r.fftsPerFrameLocked() {
					r.pushFrameLocked()
				}
			}
			r.mu.Unlock()
		}
		if err != nil {
			return fmt.Errorf("read IQ: %w", err)
		}
	}
}

func (r *Radio) pushFrameLocked() {
	db, _ := r.pool.Get().([]float64)
	if db == nil {
		db = make([]float64, r.cfg.FFTSize)
	}
	r.spec.Take(db)
	select {
	case r.frames <- db:
	default: // consumer behind: drop
		r.pool.Put(db)
	}
}

func (r *Radio) fftsPerFrameLocked() int {
	return max(1, int(r.st.SampleRate)/(r.cfg.FFTSize*r.cfg.FPS))
}

// emit sends at most one quantized frame per tick.
func (r *Radio) emit(ctx context.Context) {
	t := time.NewTicker(time.Second / time.Duration(r.cfg.FPS))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var db []float64
		select {
		case db = <-r.frames:
		default:
			continue
		}
		out := make([]byte, len(db))
		dsp.Quantize(out, db, r.cfg.MinDB, r.cfg.MaxDB)
		r.pool.Put(db)
		r.sink.Frame(out)
	}
}
