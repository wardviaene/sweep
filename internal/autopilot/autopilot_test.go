package autopilot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/autopilot"
	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/meta"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/scan"
	"github.com/wardviaene/sweep/internal/server"
	"github.com/wardviaene/sweep/internal/survey"
)

func str(s string) *string { return &s }
func iptr(i int) *int      { return &i }

const sat = 99999

var dlPacket = meta.Downlink{FreqHz: 437_500_000, Mode: "AFSK", Baud: 1200, Decoder: str("packet")}

// fakeSource serves one pass and a Doppler that depends on the time since start.
type fakeSource struct {
	mu      sync.Mutex
	passes  []meta.Pass
	visible bool
	dop     func() int // Doppler of the 437.5 MHz downlink
}

func (s *fakeSource) Passes(hours int, minEl float64) ([]meta.Pass, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]meta.Pass(nil), s.passes...), true
}

func (s *fakeSource) Overhead() ([]meta.OverheadSat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.visible {
		return nil, true
	}
	dl := dlPacket
	dl.DopplerHz = iptr(s.dop())
	return []meta.OverheadSat{{Norad: sat, Name: "TESTSAT-1", Visible: true, ElevationD: 41.2, AzimuthD: 201, Downlinks: []meta.Downlink{dl}}}, true
}

// fakeRadio records tap and VFO retunes.
type fakeRadio struct {
	mu     sync.Mutex
	st     radio.Status
	tapSet []call
	vfo    []call
	tunes  []uint32
	pcm    chan []int16
}

type call struct {
	at time.Time
	hz uint32
}

func newFakeRadio() *fakeRadio {
	return &fakeRadio{st: radio.Status{CenterHz: 440_000_000, SampleRate: 2_048_000, Demod: radio.DemodInfo{Mode: "off"}}}
}

func (f *fakeRadio) Status() radio.Status { f.mu.Lock(); defer f.mu.Unlock(); return f.st }
func (f *fakeRadio) Tune(r radio.TuneRequest) (radio.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.FreqHz != nil {
		f.st.CenterHz = *r.FreqHz
		f.tunes = append(f.tunes, *r.FreqHz)
	}
	if r.SampleRate != nil {
		f.st.SampleRate = *r.SampleRate
	}
	return f.st, nil
}
func (f *fakeRadio) SetDemod(r radio.DemodRequest) (radio.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Mode != nil {
		f.st.Demod.Mode = *r.Mode
	}
	if r.FreqHz != nil {
		f.st.Demod.FreqHz = *r.FreqHz
		f.vfo = append(f.vfo, call{time.Now(), *r.FreqHz})
	}
	return f.st, nil
}
func (f *fakeRadio) AddAudioTap(uint32, dsp.Mode, float64, bool) (int, <-chan []int16, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pcm = make(chan []int16, 4)
	f.pcm <- []int16{1, -2, 3}
	return 7, f.pcm, 48000
}
func (f *fakeRadio) AddIQTap() (int, <-chan []byte) { return 8, make(chan []byte) }
func (f *fakeRadio) RemoveTap(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == 7 && f.pcm != nil {
		close(f.pcm)
		f.pcm = nil
	}
}
func (f *fakeRadio) SetTapFreq(id int, hz uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tapSet = append(f.tapSet, call{time.Now(), hz})
	return nil
}

// fakeDecoders is a decoder manager that only records.
type fakeDecoders struct {
	mu      sync.Mutex
	ev      *decoder.Events
	running string
	opt     decoder.Options
	starts  []string
	stops   int
	retune  []call
}

func (f *fakeDecoders) Start(name string, opt decoder.Options) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, name)
	f.running, f.opt = "", decoder.Options{}
	if name != "none" {
		f.running, f.opt = name, opt
	}
	return nil
}
func (f *fakeDecoders) Stop() { f.mu.Lock(); f.stops++; f.running = ""; f.mu.Unlock() }
func (f *fakeDecoders) Current() (string, decoder.Options, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, f.opt, f.running != ""
}
func (f *fakeDecoders) Status() decoder.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return decoder.Status{Name: f.running, Running: f.running != ""}
}
func (f *fakeDecoders) RetuneChannel(hz uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retune = append(f.retune, call{time.Now(), hz})
	return nil
}
func (f *fakeDecoders) Events() *decoder.Events { return f.ev }

type env struct {
	ap   *autopilot.Autopilot
	rad  *fakeRadio
	dec  *fakeDecoders
	src  *fakeSource
	cfg  *config.Store
	data string
}

func newEnv(t *testing.T, passes ...meta.Pass) *env {
	t.Helper()
	cfg, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{rad: newFakeRadio(), dec: &fakeDecoders{ev: decoder.NewEvents()}, cfg: cfg, data: t.TempDir()}
	t0 := time.Now()
	e.src = &fakeSource{passes: passes, visible: true, dop: func() int { return -3000 + int(time.Since(t0).Seconds())*1000 }}
	e.ap = autopilot.New(autopilot.Deps{Radio: e.rad, Decoders: e.dec, Source: e.src, Config: cfg, DataDir: e.data,
		Tick: 100 * time.Millisecond, Lead: 100 * time.Millisecond, Tail: 100 * time.Millisecond})
	t.Cleanup(e.ap.Close)
	return e
}

func (e *env) enable(t *testing.T, preempt bool) {
	t.Helper()
	c := e.cfg.Get()
	c.Autopilot = &config.Autopilot{Enabled: true, Preempt: preempt}
	if err := e.cfg.Set(c); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timeout waiting for %s", what)
}

func (e *env) logs() []string {
	var out []string
	for _, ev := range e.dec.ev.Since(0, 1000) {
		if ev.Decoder == "autopilot" && ev.Kind == "log" {
			out = append(out, ev.Data.(map[string]string)["line"])
		}
	}
	return out
}

func hasLog(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestSchedulerRunsDuePassAndSkipsOverlap(t *testing.T) {
	now := time.Now().Unix()
	high := meta.Pass{Norad: sat, Name: "HIGH", AOS: now + 1, LOS: now + 3, MaxElevDeg: 60, Downlinks: []meta.Downlink{dlPacket}}
	low := meta.Pass{Norad: 5, Name: "LOW", AOS: now + 1, LOS: now + 4, MaxElevDeg: 30, Downlinks: []meta.Downlink{dlPacket}}
	e := newEnv(t, low, high)
	e.ap.Start()
	e.enable(t, false)
	waitFor(t, "history entry", func() bool { return len(e.ap.Snapshot().History) == 1 })
	h := e.ap.Snapshot().History[0]
	if h.Name != "HIGH" || h.State != "done" || h.Decoder != "packet" {
		t.Fatalf("history: %+v", h)
	}
	if !hasLog(e.logs(), "skip LOW") || !hasLog(e.logs(), "overlaps HIGH") {
		t.Fatalf("overlap not logged: %v", e.logs())
	}
	if len(e.ap.Snapshot().History) != 1 {
		t.Fatal("the skipped pass must not run")
	}
	// the pass ran on a quarter-span offset center with the decoder at the downlink
	if len(e.rad.tunes) == 0 || e.rad.tunes[0] != 437_500_000-512_000 {
		t.Fatalf("center: %v", e.rad.tunes)
	}
	if e.dec.starts[0] != "packet" {
		t.Fatalf("decoder starts: %v", e.dec.starts)
	}
}

func TestDisabledDoesNothing(t *testing.T) {
	now := time.Now().Unix()
	e := newEnv(t, meta.Pass{Norad: sat, Name: "X", AOS: now, LOS: now + 3, MaxElevDeg: 60, Downlinks: []meta.Downlink{dlPacket}})
	e.ap.Start()
	time.Sleep(600 * time.Millisecond)
	st := e.ap.Snapshot()
	if st.Current != nil || len(st.History) != 0 || len(st.Next) != 1 || st.Next[0].WillRun || st.Next[0].Reason != "autopilot disabled" {
		t.Fatalf("%+v", st)
	}
}

func TestSkipWhenBusy(t *testing.T) {
	now := time.Now().Unix()
	p := meta.Pass{Norad: sat, Name: "BUSY1", AOS: now, LOS: now + 3, MaxElevDeg: 60, Downlinks: []meta.Downlink{dlPacket}}
	// a scan holds the radio
	e := newEnv(t, p)
	scan.Busy.Lock()
	e.ap.Start()
	e.enable(t, false)
	waitFor(t, "skip log", func() bool { return hasLog(e.logs(), "skip BUSY1") })
	scan.Busy.Unlock()
	if !hasLog(e.logs(), "radio busy") || len(e.ap.Snapshot().History) != 0 {
		t.Fatalf("%v", e.logs())
	}

	// a running decoder
	e2 := newEnv(t, p)
	e2.dec.running = "aprs"
	e2.ap.Start()
	e2.enable(t, false)
	waitFor(t, "decoder skip", func() bool { return hasLog(e2.logs(), "decoder aprs running") })
	if len(e2.ap.Snapshot().History) != 0 || e2.dec.stops != 0 {
		t.Fatal("must not touch the user's decoder")
	}
}

func TestPreemptStopsAndRestoresDecoder(t *testing.T) {
	now := time.Now().Unix()
	p := meta.Pass{Norad: sat, Name: "PRE", AOS: now, LOS: now + 3, MaxElevDeg: 60, Downlinks: []meta.Downlink{dlPacket}}
	e := newEnv(t, p)
	e.dec.running, e.dec.opt = "aprs", decoder.Options{Region: "eu", FreqHz: 144_800_000}
	scan.Busy.Lock() // a scanner holds the radio until it is told to stop
	e.ap = autopilot.New(autopilot.Deps{Radio: e.rad, Decoders: e.dec, Source: e.src, Config: e.cfg, DataDir: e.data,
		Tick: 100 * time.Millisecond, Lead: 100 * time.Millisecond, Tail: 100 * time.Millisecond,
		StopOthers: func() { scan.Busy.Unlock() }})
	t.Cleanup(e.ap.Close)
	e.rad.st.Demod = radio.DemodInfo{Mode: "am", FreqHz: 440_100_000, BandwidthHz: 8000}
	e.ap.Start()
	e.enable(t, true)
	waitFor(t, "history", func() bool { return len(e.ap.Snapshot().History) == 1 })
	if h := e.ap.Snapshot().History[0]; h.State != "done" {
		t.Fatalf("%+v", h)
	}
	if got := e.dec.starts; len(got) != 2 || got[0] != "packet" || got[1] != "aprs" {
		t.Fatalf("decoder starts: %v", got)
	}
	if e.dec.opt.Region != "eu" || e.dec.opt.FreqHz != 144_800_000 {
		t.Fatalf("restored options: %+v", e.dec.opt)
	}
	if st := e.rad.Status(); st.CenterHz != 440_000_000 || st.Demod.Mode != "am" || st.Demod.FreqHz != 440_100_000 {
		t.Fatalf("restored radio: %+v", st)
	}
}

func TestDopplerRetuneOncePerSecond(t *testing.T) {
	now := time.Now().Unix()
	p := meta.Pass{Norad: sat, Name: "DOP", AOS: now - 1, LOS: now + 5, MaxElevDeg: 50, Downlinks: []meta.Downlink{dlPacket}}
	e := newEnv(t, p)
	e.ap = autopilot.New(autopilot.Deps{Radio: e.rad, Decoders: e.dec, Source: e.src, Config: e.cfg, DataDir: e.data,
		Lead: 100 * time.Millisecond, Tail: 100 * time.Millisecond}) // default 1 s tick
	t.Cleanup(e.ap.Close)
	cur, err := e.ap.Run(sat)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Decoder != "packet" || cur.DownlinkHz != 437_500_000 {
		t.Fatalf("%+v", cur)
	}
	waitFor(t, "end of pass", func() bool { return len(e.ap.Snapshot().History) == 1 })

	e.dec.mu.Lock()
	defer e.dec.mu.Unlock()
	if n := len(e.dec.retune); n < 4 || n > 7 {
		t.Fatalf("expected about one retune per second over 5 s, got %d", n)
	}
	for i, c := range e.dec.retune {
		dop := int64(c.hz) - 437_500_000
		if dop < -3000 || dop > 3000 || dop%1000 != 0 {
			t.Fatalf("retune %d: %d Hz is not downlink%+d", i, c.hz, dop)
		}
		if i > 0 {
			if dt := c.at.Sub(e.dec.retune[i-1].at); dt < 800*time.Millisecond || dt > 1300*time.Millisecond {
				t.Fatalf("retune interval %v", dt)
			}
			if c.hz <= e.dec.retune[i-1].hz { // the fake Doppler rises 1 kHz per second
				t.Fatalf("Doppler did not follow: %d then %d", e.dec.retune[i-1].hz, c.hz)
			}
		}
	}
	if e.dec.retune[0].hz != 437_500_000-3000 {
		t.Fatalf("first retune %d, want downlink-3000 (approaching satellite has positive shift in real life; the fake says -3000)", e.dec.retune[0].hz)
	}
	e.rad.mu.Lock()
	defer e.rad.mu.Unlock()
	if len(e.rad.vfo) < 4 || e.rad.vfo[len(e.rad.vfo)-1].hz != e.dec.retune[len(e.dec.retune)-1].hz {
		t.Fatalf("the VFO must follow the channel: %v", e.rad.vfo)
	}
}

func TestFMVoiceRecordsWAV(t *testing.T) {
	now := time.Now().Unix()
	fm := meta.Downlink{FreqHz: 437_500_000, Mode: "FM", Decoder: str("nfm")}
	p := meta.Pass{Norad: sat, Name: "VOICE", AOS: now - 1, LOS: now + 2, MaxElevDeg: 50, Downlinks: []meta.Downlink{fm}}
	e := newEnv(t, p)
	if _, err := e.ap.Run(sat); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "history", func() bool { return len(e.ap.Snapshot().History) == 1 })
	h := e.ap.Snapshot().History[0]
	if h.Decoder != "nfm" || h.Audio != "pass.wav" || h.State != "done" {
		t.Fatalf("%+v", h)
	}
	b, err := os.ReadFile(filepath.Join(e.data, "passes", h.Dir, "pass.wav"))
	if err != nil || len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("wav: %v %d", err, len(b))
	}
	if len(e.dec.starts) > 1 { // only the restore's "none"
		t.Fatalf("no decoder for voice: %v", e.dec.starts)
	}
	if len(e.rad.tapSet) == 0 {
		t.Fatal("the audio tap must follow Doppler")
	}
}

func TestRunErrors(t *testing.T) {
	now := time.Now().Unix()
	e := newEnv(t, meta.Pass{Norad: sat, Name: "X", AOS: now + 3600, LOS: now + 4200, MaxElevDeg: 50, Downlinks: []meta.Downlink{dlPacket}})
	e.src.visible = false
	if _, err := e.ap.Run(sat); err != autopilot.ErrNotVisible {
		t.Fatalf("not visible: %v", err)
	}
	e.src.visible = true
	e.src.passes = nil // above the horizon but no pass entry: synthetic window
	if _, err := e.ap.Run(sat); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ap.Run(sat); err == nil {
		t.Fatal("second run while busy must fail")
	}
	e.ap.Stop()
	h := e.ap.Snapshot().History
	if len(h) != 1 || h[0].State != "stopped" || !h[0].Manual {
		t.Fatalf("%+v", h)
	}
}

// ---- end to end with the synthetic radio and the real decoder manager ----

const fakeDirewolf = `#!/bin/sh
cat >/dev/null &
while true; do
  echo "[0.3] N0CALL>APRS,WIDE1-1:!4903.50N/07201.75W-test packet"
  sleep 0.5
done
`

func TestEndToEndSynthPacketPass(t *testing.T) {
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "direwolf"), []byte(fakeDirewolf), 0o755); err != nil {
		t.Fatal(err)
	}
	gain := 20.7
	hub, audio := server.NewHub(), server.NewAudioHub()
	r := radio.New(radio.Config{Synth: true, CenterHz: 440_000_000, SampleRate: 2_048_000, GainDB: &gain,
		FFTSize: 2048, FPS: 20, MinDB: -90, MaxDB: -20}, hub, audio)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	waitFor(t, "radio", func() bool { _, _, _, err := hub.Aggregate(1, "max"); return err == nil })
	cfg, _ := config.Open(filepath.Join(t.TempDir(), "config.json"))
	ev := decoder.NewEvents()
	dec := decoder.NewManager(r, ev)
	dec.ToolDirs = []string{tools}
	defer dec.Stop()
	// the user listens to something before the pass
	nfm, vfo := "nfm", uint32(440_100_000)
	if _, err := r.SetDemod(radio.DemodRequest{Mode: &nfm, FreqHz: &vfo}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	src := &fakeSource{visible: true, dop: func() int { return -2500 },
		passes: []meta.Pass{{Norad: sat, Name: "TESTSAT-1", AOS: now - 1, LOS: now + 9, MaxElevDeg: 41, Downlinks: []meta.Downlink{dlPacket}}}}
	data := t.TempDir()
	ap := autopilot.New(autopilot.Deps{Radio: r, Decoders: dec, Source: src, Config: cfg, DataDir: data,
		Lead: 100 * time.Millisecond, Tail: 100 * time.Millisecond})
	defer ap.Close()
	srv := httptest.NewServer(server.Handler(server.Deps{Radio: r, Hub: hub, Audio: audio, Decoders: dec, Events: ev, Config: cfg,
		Autopilot: ap, Survey: survey.New(survey.Deps{Radio: r, Spectrum: hub, Decoders: dec, Config: cfg}), RecordDir: t.TempDir()}))
	defer srv.Close()
	do := func(method, path string, body any) (int, string) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return resp.StatusCode, buf.String()
	}

	if code, body := do("PUT", "/api/autopilot", map[string]any{"min_elevation": 25, "satellites": []int{sat}}); code != 200 || !strings.Contains(body, `"min_elevation":25`) {
		t.Fatalf("put: %d %s", code, body)
	}
	if code, body := do("PUT", "/api/autopilot", map[string]any{"min_elevation": 120}); code != 400 {
		t.Fatalf("bad min_elevation: %d %s", code, body)
	}
	if code, body := do("POST", "/api/autopilot/run", map[string]any{"norad": sat}); code != 202 {
		t.Fatalf("run: %d %s", code, body)
	}
	if code, body := do("POST", "/api/tune", map[string]any{"freq_hz": 100_000_000}); code != 409 || !strings.Contains(body, "autopilot pass in progress") {
		t.Fatalf("tune during pass: %d %s", code, body)
	}
	if code, _ := do("POST", "/api/decoder", map[string]any{"decoder": "none"}); code != 409 {
		t.Fatalf("decoder during pass: %d", code)
	}
	if code, _ := do("POST", "/api/autopilot/run", map[string]any{"norad": sat}); code != 409 {
		t.Fatalf("second run: %d", code)
	}

	// during the pass: center at downlink - sr/4, packet decoder at the downlink, channel and VFO on downlink+doppler
	waitFor(t, "decoder channel on the Doppler-shifted downlink", func() bool {
		st := r.Status()
		ds := dec.Status()
		return ds.Name == "packet" && ds.Running && ds.FreqHz == 437_497_500 && st.Demod.FreqHz == 437_497_500
	})
	if st := r.Status(); st.CenterHz != 437_500_000-512_000 {
		t.Fatalf("center %d", st.CenterHz)
	}
	_, body := do("GET", "/api/autopilot", nil)
	var state autopilot.State
	if err := json.Unmarshal([]byte(body), &state); err != nil || state.Current == nil || state.Current.DopplerHz != -2500 || state.Current.ElevDeg != 41.2 {
		t.Fatalf("state: %v %s", err, body)
	}

	waitFor(t, "end of pass", func() bool { return len(ap.Snapshot().History) == 1 })
	h := ap.Snapshot().History[0]
	if h.State != "done" || h.Packets < 2 || h.Decoder != "packet" || h.Baud != 1200 || h.Reason != "" {
		t.Fatalf("history: %+v", h)
	}
	dir := filepath.Join(data, "passes", h.Dir)
	b, err := os.ReadFile(filepath.Join(dir, "pass.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pj autopilot.Entry
	if err := json.Unmarshal(b, &pj); err != nil || pj.State != "done" || pj.Packets != h.Packets || pj.Norad != sat {
		t.Fatalf("pass.json: %v %s", err, b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "packets.jsonl")); bytes.Count(b, []byte("\n")) != h.Packets {
		t.Fatalf("packets.jsonl does not match the counter %d: %s", h.Packets, b)
	}
	// the previous tuning and VFO are back, no decoder runs
	waitFor(t, "restore", func() bool { return !ap.Active() })
	st := r.Status()
	if st.CenterHz != 440_000_000 || st.Demod.Mode != "nfm" || st.Demod.FreqHz != 440_100_000 || dec.Status().Running {
		t.Fatalf("not restored: center %d demod %+v decoder %+v", st.CenterHz, st.Demod, dec.Status())
	}
	// LOS events
	var gotPass, gotLOS bool
	for _, e := range ev.Since(0, 1000) {
		if e.Decoder == "autopilot" && e.Kind == "pass" {
			gotPass = true
		}
		if e.Decoder == "autopilot" && e.Kind == "log" && strings.HasPrefix(e.Data.(map[string]string)["line"], "LOS TESTSAT-1") {
			gotLOS = true
		}
	}
	if !gotPass || !gotLOS {
		t.Fatalf("events: pass %v los %v", gotPass, gotLOS)
	}
	// history survives a restart
	ap2 := autopilot.New(autopilot.Deps{Radio: r, Decoders: dec, Source: src, Config: cfg, DataDir: data})
	if hh := ap2.Snapshot().History; len(hh) != 1 || hh[0].Packets != h.Packets || hh[0].Dir != h.Dir {
		t.Fatalf("reloaded history: %+v", hh)
	}
	// tuning works again
	if code, _ := do("POST", "/api/tune", map[string]any{"freq_hz": 100_000_000}); code != 200 {
		t.Fatalf("tune after pass: %d", code)
	}
}
