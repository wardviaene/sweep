package scanner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/radio"
)

type nopSink struct{}

func (nopSink) Meta(radio.Meta) {}
func (nopSink) Frame([]byte)    {}

type env struct {
	r   *radio.Radio
	sc  *Scanner
	ev  *decoder.Events
	cfg *config.Store
}

func newEnv(t *testing.T, bursts ...radio.SynthBurst) *env {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	gain := 20.7
	r := radio.New(radio.Config{Synth: true, SynthBurst: bursts, CenterHz: 440_000_000, SampleRate: 1_000_000, GainDB: &gain,
		FFTSize: 2048, FPS: 20, MinDB: -90, MaxDB: -20}, nopSink{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev := decoder.NewEvents()
	dec := decoder.NewManager(r, ev)
	dec.ToolDirs = []string{t.TempDir()}
	t.Cleanup(dec.Stop)
	sc := New(Deps{Radio: r, Decoders: dec, Config: store})
	t.Cleanup(func() { sc.Stop() })
	for end := time.Now().Add(10 * time.Second); !r.Status().Connected; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("radio not connected")
		}
	}
	return &env{r, sc, ev, store}
}

func TestBurstsAndRestore(t *testing.T) {
	e := newEnv(t,
		radio.SynthBurst{FreqHz: 440_200_000, OnS: 1, OffS: 0, Amp: 0.05},   // continuous: never a transmission
		radio.SynthBurst{FreqHz: 440_400_000, OnS: 1.5, OffS: 2, Amp: 0.05}, // bursty
	)
	before := e.r.Status()
	sub, cancel := e.ev.Subscribe(0)
	defer cancel()
	st, err := e.sc.Start(Request{LoHz: 439_700_000, HiHz: 440_500_000, Mode: "nfm", HangS: 0.4})
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "watching" || st.StepHz != 12500 {
		t.Fatalf("start: %+v", st)
	}
	if _, err := e.sc.Start(Request{LoHz: 439_700_000, HiHz: 440_500_000, Mode: "nfm", HangS: 0.4}); err != nil {
		t.Fatalf("reconfigure: %v", err) // a second POST reconfigures instead of failing
	}
	lockedOK := false
	for end := time.Now().Add(12 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		s := e.sc.Snapshot()
		if s.State == "locked" {
			if dm := e.r.Status().Demod; dm.FreqHz == 440_400_000 && dm.Mode == "nfm" && dm.SquelchDB != nil && *dm.SquelchDB < 0 && s.Active != nil && s.Active.FreqHz == 440_400_000 {
				lockedOK = true
			}
		}
		if len(s.Recent) >= 2 && lockedOK {
			break
		}
	}
	s := e.sc.Snapshot()
	if !lockedOK {
		t.Fatalf("VFO was never on the burst while locked: %+v demod %+v", s, e.r.Status().Demod)
	}
	if len(s.Recent) < 2 {
		t.Fatalf("want >= 2 transmissions: %+v", s.Recent)
	}
	for _, tx := range s.Recent {
		if tx.FreqHz != 440_400_000 {
			t.Errorf("transmission on %d, want only the bursty carrier: %+v", tx.FreqHz, s.Recent)
		}
		if tx.DurationS < 1.0 || tx.DurationS > 2.5 {
			t.Errorf("duration %.2f s, want about 1.5: %+v", tx.DurationS, tx)
		}
	}
	if len(s.Channels) != 1 || s.Channels[0].Count < 2 {
		t.Errorf("channels: %+v", s.Channels)
	}
	select {
	case b := <-sub:
		t.Logf("event: %s", b)
	default:
		t.Error("no scanner event published")
	}
	if dm := e.r.Status().Demod; s.State == "watching" && (dm.SquelchDB == nil || *dm.SquelchDB < 0) {
		t.Errorf("VFO not muted between transmissions: %+v", dm)
	}

	if _, err := e.sc.Lockout(440_400_000, false); err != nil {
		t.Fatal(err)
	}
	if got := e.cfg.Get().ScannerLockouts["custom"]; len(got) != 1 {
		t.Errorf("lockout not remembered: %+v", e.cfg.Get().ScannerLockouts)
	}
	n := len(e.sc.Snapshot().Recent)
	time.Sleep(4500 * time.Millisecond)
	if after := len(e.sc.Snapshot().Recent); after > n+1 { // one may have been in progress
		t.Errorf("locked-out channel still produces transmissions: %d -> %d", n, after)
	}

	fin := e.sc.Stop()
	if fin.State != "stopped" {
		t.Fatalf("stop: %+v", fin)
	}
	after := e.r.Status()
	if after.CenterHz != before.CenterHz || after.SampleRate != before.SampleRate || after.Demod.Mode != before.Demod.Mode {
		t.Errorf("not restored: before %+v after %+v", before, after)
	}
	if e.sc.Active() {
		t.Error("still active after stop")
	}
}

func TestResolve(t *testing.T) {
	p, err := resolve(Request{Band: "air"})
	if err != nil || p.lo != 118_000_000 || p.hi != 137_000_000 || p.mode != "am" || p.step != 25000 || p.bw != 10000 {
		t.Fatalf("air: %+v %v", p, err)
	}
	if p, err := resolve(Request{Band: "pmr446"}); err != nil || p.step != 6250 || p.mode != "nfm" {
		t.Fatalf("pmr: %+v %v", p, err)
	}
	for _, bad := range []Request{
		{}, {Band: "nope"}, {LoHz: 10_000_000, HiHz: 20_000_000, Mode: "am"},
		{LoHz: 100_000_000, HiHz: 140_000_000, Mode: "am"}, {LoHz: 100_000_000, HiHz: 101_000_000},
		{LoHz: 100_000_000, HiHz: 101_000_000, Mode: "fm"}, {Band: "air", MarginDB: 1},
	} {
		if _, err := resolve(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestWindowCycling(t *testing.T) {
	// 1.5 MHz at 1 MS/s: two windows; the burst is in the second one.
	e := newEnv(t, radio.SynthBurst{FreqHz: 440_300_000, OnS: 2, OffS: 1, Amp: 0.05})
	st, err := e.sc.Start(Request{LoHz: 439_000_000, HiHz: 440_500_000, Mode: "nfm", HangS: 0.4, DwellMS: 700})
	if err != nil {
		t.Fatal(err)
	}
	_ = st
	seen := map[int]bool{}
	for end := time.Now().Add(14 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		s := e.sc.Snapshot()
		if s.Window != nil {
			if s.Window.Count != 2 {
				t.Fatalf("windows: %+v", s.Window)
			}
			seen[s.Window.Index] = true
		}
		if len(s.Recent) >= 1 && len(seen) == 2 {
			break
		}
	}
	s := e.sc.Snapshot()
	if len(seen) != 2 || len(s.Recent) == 0 || s.Recent[0].FreqHz != 440_300_000 {
		t.Fatalf("seen %v recent %+v", seen, s.Recent)
	}
}
