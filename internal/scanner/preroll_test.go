package scanner

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/source"
	"github.com/wardviaene/sweep/internal/transcribe"
)

// goertzel is the magnitude of the f Hz component of x.
func goertzel(x []float64, f, rate float64) float64 {
	w := 2 * math.Pi * f / rate
	c := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s1, s2 = v+c*s1-s2, s1
	}
	return math.Sqrt(s1*s1+s2*s2-c*s1*s2) / float64(len(x))
}

// onsetS is the time (s) the 1 kHz tone starts in a clip: the first 20 ms
// window whose 1 kHz component reaches half the clip's maximum.
func onsetS(pcm []int16, rate float64) float64 {
	n := int(0.02 * rate)
	var mags []float64
	var mx float64
	for i := 0; i+n <= len(pcm); i += n {
		w := make([]float64, n)
		for j := range w {
			w[j] = float64(pcm[i+j]) / 32768
		}
		m := goertzel(w, 1000, rate)
		mags = append(mags, m)
		mx = math.Max(mx, m)
	}
	for i, m := range mags {
		if m >= mx/2 {
			return float64(i*n) / rate
		}
	}
	return -1
}

// burstStart watches the raw IQ and returns the wall-clock time the first
// strong sample arrived (the true start of the burst), on the radio's own clock.
func burstStart(r *radio.Radio, found chan<- time.Time) {
	id, iq := r.AddIQTap()
	go func() {
		defer r.RemoveTap(id)
		sr := float64(r.Status().SampleRate)
		for b := range iq {
			now := time.Now()
			for i := 0; i+128 <= len(b); i += 128 { // mean |I| over 64 samples: noise 0.3, the burst 1.6
				var sum float64
				for j := i; j < i+128; j += 2 {
					sum += math.Abs(float64(b[j]) - 127.5)
				}
				if sum/64 > 1.0 {
					found <- now.Add(-time.Duration(float64(len(b)-i) / 2 / sr * float64(time.Second)))
					return
				}
			}
		}
	}()
}

// runPreRoll runs a scanner over a speech-like FM burst and returns how far
// into the clip the burst starts (s) and how far into it the clip should start
// by the timestamps.
func runPreRoll(t *testing.T, useIQ bool) (onset, expected float64) {
	e, onset, expected := preRoll(t, useIQ, nil, 0, "")
	_ = e
	return onset, expected
}

// preRoll runs the scanner over a keyed FM burst (a 1 kHz tone, or speech when
// pcm is given) and returns the transcript entry of its clip. With model set it
// uses a real whisper-server.
func preRoll(t *testing.T, useIQ bool, pcm []int16, rate float64, model string) (e transcribe.Entry, onset, expected float64) {
	t.Helper()
	srvURL := ""
	if model == "" {
		t.Setenv("PATH", t.TempDir())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"text": "alpha bravo"})
		}))
		t.Cleanup(srv.Close)
		srvURL = srv.URL
	}
	on := 2.5
	if pcm != nil {
		on = float64(len(pcm))/rate + 0.6
	}
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	tee := transcribe.NewTee(nil)
	gain := 20.7
	r := radio.New(radio.Config{Synth: true, SynthBurst: []radio.SynthBurst{{FreqHz: 440_400_000, OnS: on, OffS: 3, Amp: 0.02, ModHz: 1000, PCM: pcm, Rate: rate}},
		CenterHz: 440_000_000, SampleRate: 1_000_000, GainDB: &gain, FFTSize: 2048, FPS: 20, MinDB: -90, MaxDB: -20}, nopSink{}, tee)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	o := transcribe.Options{Dir: filepath.Join(t.TempDir(), "clips"), Model: "/x/ggml-small.en.bin", Tee: tee, IQ: r,
		ServerURL: func(context.Context) (string, error) { return srvURL, nil }}
	if model != "" {
		o.Model, o.ServerURL = model, nil
	}
	tr, err := transcribe.New(o)
	if err != nil {
		t.Fatal(err)
	}
	go tr.Run(ctx)
	t.Cleanup(func() { cancel(); tr.Close() })
	go r.Run(ctx)
	for end := time.Now().Add(10 * time.Second); !r.Status().Connected; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("radio not connected")
		}
	}
	truth := make(chan time.Time, 1)
	burstStart(r, truth)
	dec := decoder.NewManager(r, decoder.NewEvents())
	dec.ToolDirs = []string{t.TempDir()}
	t.Cleanup(dec.Stop)
	d := Deps{Radio: r, Decoders: dec, Config: store, Clip: tr.ClipFunc("scanner")}
	if useIQ {
		d.ClipIQ, d.Buffering = tr.ClipIQFunc("scanner"), tr.SetScanning
	}
	sc := New(d)
	t.Cleanup(func() { sc.Stop() })
	if _, err := sc.Start(Request{LoHz: 439_700_000, HiHz: 440_500_000, Mode: "nfm", HangS: 0.4}); err != nil {
		t.Fatal(err)
	}
	var tTrue time.Time
	select {
	case tTrue = <-truth:
	case <-time.After(15 * time.Second):
		t.Fatal("burst never seen")
	}
	for end := time.Now().Add(30 * time.Second); tr.Store().Len() == 0; time.Sleep(25 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("no clip: scanner %+v", sc.Snapshot())
		}
	}
	e = tr.Store().Query(0, 1, "", 0)[0]
	clip, crate, err := source.ReadWAV(transcribe.ClipPath(tr.Dir(), e.ID))
	if err != nil {
		t.Fatal(err)
	}
	start := time.UnixMilli(int64(e.Start * 1e3))
	// the clip begins 0.3 s before the (back-dated) lock start
	expected = tTrue.Sub(start.Add(-300 * time.Millisecond)).Seconds()
	onset = onsetS(clip, crate)
	t.Logf("iq=%v clip %.2f s: tone starts %.2f s into the clip (timestamps predict %.2f s), detected %.2f s after the true start",
		useIQ, e.DurationS, onset, expected, start.Sub(tTrue).Seconds())
	return e, onset, expected
}

func TestClipStartsBeforeLock(t *testing.T) {
	onset, expected := runPreRoll(t, true)
	if onset < 0.04 || onset > 0.5 {
		t.Errorf("tone starts %.2f s into the clip, want it to start within 0.5 s of the beginning (cut when ~0)", onset)
	}
	if math.Abs(onset-expected) > 0.04 {
		t.Errorf("onset %.2f s vs %.2f s predicted by the timestamps", onset, expected)
	}
}

// TestClipStartLegacy documents the old behaviour (the clip starts at the lock,
// so the beginning is cut).
func TestClipStartLegacy(t *testing.T) {
	onset, _ := runPreRoll(t, false)
	t.Logf("legacy: the clip is missing the first %.2f s of the burst", math.Max(0, onset-0.25))
}

// TestFirstWordTranscribed needs SWEEP_WHISPER_TEST=1, whisper-server, the
// model, macOS say and sox: a spoken burst keys up on the channel and the
// transcript must start with its first word.
func TestFirstWordTranscribed(t *testing.T) {
	if os.Getenv("SWEEP_WHISPER_TEST") != "1" {
		t.Skip("set SWEEP_WHISPER_TEST=1")
	}
	for _, b := range []string{"say", "sox", "whisper-server"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	model := os.Getenv("SWEEP_WHISPER_MODEL")
	if model == "" {
		home, _ := os.UserHomeDir()
		model = filepath.Join(home, ".local/share/sweep/models/ggml-small.en.bin")
	}
	if _, err := os.Stat(model); err != nil {
		t.Skipf("model: %v", err)
	}
	tmp := t.TempDir()
	aiff, wav := filepath.Join(tmp, "s.aiff"), filepath.Join(tmp, "s.wav")
	if out, err := exec.Command("say", "-o", aiff, "Cessna one two three, left downwind runway one seven").CombinedOutput(); err != nil {
		t.Skipf("say: %v %s", err, out)
	}
	if out, err := exec.Command("sox", aiff, "-r", "24000", "-c", "1", "-b", "16", wav).CombinedOutput(); err != nil {
		t.Skipf("sox: %v %s", err, out)
	}
	pcm, rate, err := source.ReadWAV(wav)
	if err != nil {
		t.Fatal(err)
	}
	e, onset, expected := preRoll(t, true, pcm, rate, model)
	t.Logf("transcript %q (onset %.2f, predicted %.2f)", e.Text, onset, expected)
	if !strings.Contains(strings.ToLower(e.Text), "cessna") {
		t.Errorf("first word missing: %q", e.Text)
	}
}
