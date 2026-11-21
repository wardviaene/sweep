package transcribe

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
)

// MaxQueue is the most clips waiting; the oldest are dropped first.
const MaxQueue = 50

// Options configure a Transcriber.
type Options struct {
	Dir     string // clips directory (<data dir>/clips)
	DataDir string // POST /api/transcribe {"path"} reads WAV files below this directory
	Model   string // path of the ggml model
	Bin     string // whisper-server binary
	Mode    string // "auto" (on when the model exists), "on" or "off": the default when the config says nothing
	Threads int
	Config  *config.Store
	Events  *decoder.Events
	Tee     *Tee
	// IQ is the radio's IQ history: scanner clips are demodulated from it, from
	// before the lock; optional (without it clips come from the Tee).
	IQ IQSource
	// VFO reports the listening VFO (frequency, mode) for manual recordings; optional.
	VFO func() (freqHz uint32, mode string)
	// ServerURL replaces the whisper-server child process (tests).
	ServerURL func(ctx context.Context) (string, error)
}

// Clip is a finished voice recording from the listening VFO.
type Clip struct {
	Source string // scanner | survey | manual
	FreqHz uint32
	Label  string
	Mode   string
	Start  time.Time
	PCM    []int16
	Rate   float64
}

// Result is the outcome of one clip.
type Result struct {
	Entry   *Entry `json:"entry,omitempty"`
	Skipped string `json:"skipped,omitempty"` // why nothing was transcribed
	Err     error  `json:"-"`
}

type job struct {
	c    Clip
	pcm  []int16 // 16 kHz
	done chan Result
}

// Status is the GET /api/transcribe payload.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Model     string `json:"model"`
	Server    string `json:"server"`
	Queue     int    `json:"queue"`
	LastError string `json:"last_error"`
}

// Transcriber queues clips and runs them through whisper-server one at a time.
type Transcriber struct {
	o     Options
	store *Store
	proc  *process
	model string

	mu       sync.Mutex
	queue    []*job
	busy     bool
	lastErr  string
	scanning bool  // a scanner runs: keep the IQ history
	override *bool // SetEnabled, takes precedence over the config and flag
	wake     chan struct{}
	stopped  chan struct{} // closed when Run has returned
}

// New opens the transcript store. Call Run to start the worker.
func New(o Options) (*Transcriber, error) {
	if o.Bin == "" {
		o.Bin = "whisper-server"
	}
	st, err := OpenStore(o.Dir)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(o.Model), "ggml-"), ".bin")
	t := &Transcriber{o: o, store: st, model: name, wake: make(chan struct{}, 1), stopped: make(chan struct{}),
		proc: &process{bin: o.Bin, model: o.Model, threads: o.Threads}}
	t.syncTee()
	return t, nil
}

// DataDir is the directory transcription may read WAV files from.
func (t *Transcriber) DataDir() string { return t.o.DataDir }

// Dir is the clips directory.
func (t *Transcriber) Dir() string { return t.o.Dir }

// Store returns the transcript store.
func (t *Transcriber) Store() *Store { return t.store }

func (t *Transcriber) modelOK() bool {
	if t.o.ServerURL != nil {
		return true
	}
	_, err := os.Stat(t.o.Model)
	return err == nil
}

// Enabled reports whether new clips are captured.
func (t *Transcriber) Enabled() bool {
	t.mu.Lock()
	ov := t.override
	t.mu.Unlock()
	if ov != nil {
		return *ov && t.modelOK()
	}
	if t.o.Config != nil {
		if c := t.o.Config.Get().Transcribe; c != nil && c.Enabled != nil {
			return *c.Enabled && t.modelOK()
		}
	}
	switch t.o.Mode {
	case "on":
		return t.modelOK()
	case "off":
		return false
	}
	return t.modelOK()
}

func (t *Transcriber) syncTee() {
	en := t.Enabled()
	if t.o.Tee != nil {
		t.o.Tee.SetEnabled(en)
	}
	if t.o.IQ != nil {
		t.mu.Lock()
		sc := t.scanning
		t.mu.Unlock()
		t.o.IQ.SetIQRing(en && sc)
	}
}

// SetEnabled stores the setting in the config and applies it.
func (t *Transcriber) SetEnabled(on bool) error {
	t.mu.Lock()
	t.override = &on
	t.mu.Unlock()
	if t.o.Config != nil {
		c := t.o.Config.Get()
		c.Transcribe = &config.Transcribe{Enabled: &on}
		if err := t.o.Config.Set(c); err != nil {
			return err
		}
	}
	t.syncTee()
	if !on {
		t.dropQueue("transcription turned off")
	}
	return nil
}

// Status reports the state for GET /api/transcribe.
func (t *Transcriber) Status() Status {
	srv, perr := t.proc.status()
	if t.o.ServerURL != nil {
		srv = "running"
	}
	t.mu.Lock()
	q := len(t.queue)
	if t.busy {
		q++
	}
	le := t.lastErr
	t.mu.Unlock()
	if le == "" {
		le = perr
	}
	en := t.Enabled()
	if le == "" && !t.modelOK() {
		le = fmt.Sprintf("model %s not found (run make whisper-model)", t.o.Model)
	}
	return Status{Enabled: en, Model: t.model, Server: srv, Queue: q, LastError: le}
}

// Run keeps the tee in step with the setting and processes the queue until
// ctx ends, then stops whisper-server.
func (t *Transcriber) Run(ctx context.Context) {
	defer close(t.stopped)
	defer t.proc.stop()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.syncTee()
		case <-t.wake:
			for {
				j := t.next()
				if j == nil {
					break
				}
				j.done <- t.process(ctx, j)
				t.mu.Lock()
				t.busy = false
				t.mu.Unlock()
			}
		}
	}
}

func (t *Transcriber) next() *job {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.queue) == 0 {
		return nil
	}
	j := t.queue[0]
	t.queue = t.queue[1:]
	t.busy = true
	return j
}

func (t *Transcriber) dropQueue(why string) {
	t.mu.Lock()
	q := t.queue
	t.queue = nil
	t.mu.Unlock()
	for _, j := range q {
		j.done <- Result{Skipped: why}
	}
}

// Begin starts capturing the listening VFO for a transmission. The returned
// function ends the capture at the given time (so hang time is excluded) and
// queues the clip. It is nil when transcription is off or the VFO is off.
func (t *Transcriber) Begin(source string, freqHz uint32, label, mode string, start time.Time) func(end time.Time) {
	return t.begin(source, freqHz, label, mode, 0, start, false)
}

// SetScanning tells the transcriber a scanner is running, which keeps the radio's
// IQ history on (while transcription is enabled) so clips start before the lock.
func (t *Transcriber) SetScanning(on bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.scanning = on
	t.mu.Unlock()
	t.syncTee()
}

// BeginIQ is Begin for a transmission that started at start, before the VFO was
// on it: with the IQ history on, the clip is demodulated from the buffered IQ
// (mode, bandwidth bw, 0 = default) from 0.3 s before start. Without it the clip
// is the VFO audio from now on.
func (t *Transcriber) BeginIQ(source string, freqHz uint32, label, mode string, bw float64, start time.Time) func(end time.Time) {
	return t.begin(source, freqHz, label, mode, bw, start, true)
}

func (t *Transcriber) begin(source string, freqHz uint32, label, mode string, bw float64, start time.Time, useIQ bool) func(end time.Time) {
	if t == nil || !t.Enabled() {
		return nil
	}
	if useIQ && t.o.IQ != nil {
		if c := beginIQ(t.o.IQ, freqHz, mode, bw, start); c != nil {
			return func(end time.Time) {
				pcm, rate, ok := c.End(end)
				if !ok {
					return
				}
				t.Submit(Clip{Source: source, FreqHz: freqHz, Label: label, Mode: mode, Start: start, PCM: pcm, Rate: float64(rate)})
			}
		}
	}
	if t.o.Tee == nil {
		return nil
	}
	c := t.o.Tee.Begin()
	if c == nil {
		return nil
	}
	return func(end time.Time) {
		pcm, rate, ok := c.End(end)
		if !ok {
			return
		}
		t.Submit(Clip{Source: source, FreqHz: freqHz, Label: label, Mode: mode, Start: start, PCM: pcm, Rate: float64(rate)})
	}
}

// Submit prepares a clip (band-pass, 16 kHz, normalize) and queues it. The
// returned channel receives the result (buffered, never blocks the worker).
func (t *Transcriber) Submit(c Clip) <-chan Result {
	done := make(chan Result, 1)
	if dur := float64(len(c.PCM)) / c.Rate; c.Rate <= 0 || dur < MinClipS {
		done <- Result{Skipped: "shorter than 0.5 s"}
		return done
	}
	pcm, rms := Prepare(c.PCM, c.Rate)
	if rms < SilenceDB {
		done <- Result{Skipped: "near-silent audio"}
		return done
	}
	j := &job{c: c, pcm: pcm, done: done}
	t.mu.Lock()
	t.queue = append(t.queue, j)
	var dropped *job
	if len(t.queue) > MaxQueue {
		dropped, t.queue = t.queue[0], t.queue[1:]
	}
	t.mu.Unlock()
	if dropped != nil {
		log.Printf("transcribe: queue full, dropped the oldest clip (%.1f MHz)", float64(dropped.c.FreqHz)/1e6)
		dropped.done <- Result{Skipped: "queue full"}
	}
	select {
	case t.wake <- struct{}{}:
	default:
	}
	return done
}

func (t *Transcriber) setErr(err error) {
	t.mu.Lock()
	if err == nil {
		t.lastErr = ""
	} else {
		t.lastErr = err.Error()
	}
	t.mu.Unlock()
}

func (t *Transcriber) serverURL(ctx context.Context) (string, error) {
	if t.o.ServerURL != nil {
		return t.o.ServerURL(ctx)
	}
	return t.proc.ensure(ctx)
}

func (t *Transcriber) process(ctx context.Context, j *job) Result {
	url, err := t.serverURL(ctx)
	if err != nil {
		t.setErr(err)
		log.Printf("transcribe: %v", err)
		return Result{Err: err}
	}
	dur := float64(len(j.pcm)) / WhisperRate
	_, prompt := PromptFor(j.c.FreqHz)
	t0 := time.Now()
	text, err := (&Client{URL: url}).Transcribe(ctx, WAV(j.pcm, WhisperRate), prompt)
	lat := time.Since(t0).Seconds()
	if err != nil {
		if ctx.Err() != nil {
			return Result{Err: err}
		}
		t.setErr(err)
		log.Printf("transcribe: %v", err)
		return Result{Err: err}
	}
	t.setErr(nil)
	clean, empty := Filter(text, dur)
	id := t.newID(j.c)
	if err := WriteWAV(ClipPath(t.o.Dir, id), j.pcm, WhisperRate); err != nil {
		t.setErr(err)
		return Result{Err: err}
	}
	e := Entry{ID: id, Source: j.c.Source, FreqHz: j.c.FreqHz, Label: j.c.Label, Mode: j.c.Mode,
		Start: float64(j.c.Start.UnixMilli()) / 1e3, DurationS: math.Round(dur*10) / 10, Text: clean, Empty: empty,
		Model: t.model, LatencyS: math.Round(lat*100) / 100, URL: URLFor(id)}
	t.store.Add(e)
	if t.o.Events != nil {
		t.o.Events.Publish("transcribe", "message", e)
	}
	return Result{Entry: &e}
}

// newID is <YYYYmmdd>-<HHMMSS>_<freq>, with -n appended when it is taken.
func (t *Transcriber) newID(c Clip) string {
	base := c.Start.Local().Format("20060102-150405") + "_" + strconv.FormatUint(uint64(c.FreqHz), 10)
	id := base
	for n := 2; ; n++ {
		if _, err := os.Stat(ClipPath(t.o.Dir, id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
		id = base + "-" + strconv.Itoa(n)
	}
}

// Record captures the current VFO audio for seconds (1-60), transcribes it
// and waits for the result.
func (t *Transcriber) Record(ctx context.Context, seconds float64) (Result, error) {
	if !t.Enabled() {
		return Result{}, errors.New("transcription is off")
	}
	if t.o.Tee == nil {
		return Result{}, errors.New("no audio source")
	}
	c := t.o.Tee.Begin()
	if c == nil {
		return Result{}, errors.New("listening VFO is off (turn it on with POST /api/demod)")
	}
	start := time.Now().Add(-time.Duration(preRollS * float64(time.Second)))
	select {
	case <-time.After(time.Duration(seconds * float64(time.Second))):
	case <-ctx.Done():
		c.Cancel()
		return Result{}, ctx.Err()
	}
	pcm, rate, ok := c.End(time.Now())
	if !ok {
		return Result{}, errors.New("the audio changed while recording")
	}
	var freq uint32
	var mode string
	if t.o.VFO != nil {
		freq, mode = t.o.VFO()
	}
	return t.wait(ctx, t.Submit(Clip{Source: "manual", FreqHz: freq, Mode: mode, Start: start, PCM: pcm, Rate: float64(rate)}))
}

// TranscribeFile transcribes a 16-bit WAV that is already on disk.
func (t *Transcriber) TranscribeFile(ctx context.Context, pcm []int16, rate float64, start time.Time) (Result, error) {
	if !t.Enabled() {
		return Result{}, errors.New("transcription is off")
	}
	return t.wait(ctx, t.Submit(Clip{Source: "manual", Start: start, PCM: pcm, Rate: rate}))
}

func (t *Transcriber) wait(ctx context.Context, ch <-chan Result) (Result, error) {
	select {
	case r := <-ch:
		return r, r.Err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// ClipIQFunc adapts BeginIQ for the scanner. A nil Transcriber gives a nil func.
func (t *Transcriber) ClipIQFunc(source string) func(freqHz uint32, label, mode string, bw float64, start time.Time) func(end time.Time) {
	if t == nil {
		return nil
	}
	return func(freqHz uint32, label, mode string, bw float64, start time.Time) func(time.Time) {
		return t.BeginIQ(source, freqHz, label, mode, bw, start)
	}
}

// ClipFunc adapts Begin for the scanner and survey hooks. A nil Transcriber
// gives a nil func.
func (t *Transcriber) ClipFunc(source string) func(freqHz uint32, label, mode string, start time.Time) func(end time.Time) {
	if t == nil {
		return nil
	}
	return func(freqHz uint32, label, mode string, start time.Time) func(time.Time) {
		return t.Begin(source, freqHz, label, mode, start)
	}
}

// Close waits (up to 5 s) for Run to stop whisper-server after its context ended.
func (t *Transcriber) Close() {
	if t == nil {
		return
	}
	select {
	case <-t.stopped:
	case <-time.After(5 * time.Second):
	}
}
