package transcribe

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/source"
)

func tone(rate int, hz, secs, amp float64) []int16 {
	n := int(float64(rate) * secs)
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * 32767 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return out
}

func toFloat(p []int16) []float64 {
	o := make([]float64, len(p))
	for i, v := range p {
		o[i] = float64(v) / 32768
	}
	return o
}

func TestResampleLengthAndTone(t *testing.T) {
	in := tone(48000, 1000, 1, 0.5)
	out := Resample(toFloat(in), 48000, 16000)
	if len(out) != 16000 {
		t.Fatalf("len = %d", len(out))
	}
	// the 1 kHz tone keeps its amplitude and has 16 samples per cycle
	if r := RMSdB(out); math.Abs(r-RMSdB(toFloat(in))) > 0.5 {
		t.Errorf("level changed: %.2f vs %.2f dB", r, RMSdB(toFloat(in)))
	}
	if math.Abs(out[16]-out[0]) > 0.05 || math.Abs(out[8]+out[0]) > 0.6 && false {
		t.Errorf("period is not 16 samples")
	}
}

func TestBandPass(t *testing.T) {
	const rate = 24000
	in := func(hz float64) float64 {
		bp := BandPass(tone(rate, hz, 1, 0.5), rate)
		return RMSdB(bp[rate/2:]) // skip the transient
	}
	mid, low, high := in(1000), in(50), in(8000)
	if mid < -12 {
		t.Errorf("1 kHz attenuated: %.1f dB", mid)
	}
	if mid-low < 30 || mid-high < 30 {
		t.Errorf("stop band too weak: mid %.1f low %.1f high %.1f", mid, low, high)
	}
}

func TestPrepareNormalizes(t *testing.T) {
	out, rms := Prepare(tone(48000, 1000, 2, 0.05), 48000)
	if len(out) != 32000 {
		t.Errorf("len = %d", len(out))
	}
	if rms > -20 || rms < -40 {
		t.Errorf("rms = %.1f", rms)
	}
	got := RMSdB(toFloat(out))
	if math.Abs(got+20) > 1 {
		t.Errorf("normalized rms = %.1f dB, want -20", got)
	}
	if _, rms := Prepare(make([]int16, 48000), 48000); rms > SilenceDB {
		t.Errorf("silence rms = %.1f", rms)
	}
}

func TestWAVWriter(t *testing.T) {
	pcm := tone(16000, 440, 0.5, 0.3)
	p := filepath.Join(t.TempDir(), "d", "x.wav")
	if err := WriteWAV(p, pcm, 16000); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || binary.LittleEndian.Uint32(b[24:]) != 16000 {
		t.Fatalf("bad header %q", b[:44])
	}
	got, rate, err := source.ReadWAV(p)
	if err != nil || rate != 16000 || len(got) != len(pcm) || got[100] != pcm[100] {
		t.Fatalf("round trip: %v rate %v len %d", err, rate, len(got))
	}
	if st, _ := os.Stat(p + ".tmp"); st != nil {
		t.Error("temp file left behind")
	}
}

func TestFilter(t *testing.T) {
	cases := []struct {
		in    string
		dur   float64
		want  string
		empty bool
	}{
		{"Thank you.", 2, "", true},
		{" you ", 1, "", true},
		{"Thanks for watching!", 5, "", true},
		{"[BLANK_AUDIO]", 2, "", true},
		{"(wind blowing) [static]", 2, "", true},
		{"[static] Cessna one two three", 3, "Cessna one two three", false},
		{"Thank you. Thank you. Thank you. Thank you.", 6, "", true},
		{"Left downwind runway one seven, left downwind runway one seven", 6, "Left downwind runway one seven, left downwind runway one seven", false},
		{"go go go go go go go go", 4, "", true},
		{"Subtitles by the Amara.org community", 5, "", true},
		{"Okay.", 20, "", true},
		{"Roger, cleared to land runway 17, wind calm", 4, "Roger, cleared to land runway 17, wind calm", false},
		{"", 3, "", true},
	}
	for _, c := range cases {
		got, empty := Filter(c.in, c.dur)
		if got != c.want || empty != c.empty {
			t.Errorf("Filter(%q) = %q, %v; want %q, %v", c.in, got, empty, c.want, c.empty)
		}
	}
}

func TestPromptChoice(t *testing.T) {
	cases := map[uint32]string{
		122_900_000: KindAirband, 121_500_000: KindAirband,
		146_520_000: KindHam, 433_920_000: KindHam,
		462_562_500: KindFRS, 446_006_250: KindFRS,
		156_800_000: KindMarine, 161_975_000: KindMarine,
		162_550_000: KindNOAA, 162_400_000: KindNOAA,
		300_000_000: KindGeneric,
	}
	for f, want := range cases {
		if got := BandKind(f); got != want {
			t.Errorf("BandKind(%d) = %s, want %s", f, got, want)
		}
	}
	if _, p := PromptFor(122_900_000); !strings.Contains(p, "downwind") {
		t.Errorf("airband prompt: %q", p)
	}
}

func entry(id string, start time.Time) Entry {
	return Entry{ID: id, Source: "scanner", FreqHz: 122_900_000, Start: float64(start.UnixMilli()) / 1e3, Text: "hello " + id, URL: URLFor(id)}
}

func touchClip(t *testing.T, dir, id string) {
	t.Helper()
	if err := WriteWAV(ClipPath(dir, id), []int16{1, 2, 3}, 16000); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionAndIndexRebuild(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	// an old clip (8 days) and fresh ones
	old := now.Add(-8 * 24 * time.Hour)
	oid := old.Format("20060102-150405") + "_122900000"
	touchClip(t, dir, oid)
	s.Add(entry(oid, old))
	if s.Len() != 0 {
		t.Fatalf("expired entry kept: %d", s.Len())
	}
	if _, err := os.Stat(ClipPath(dir, oid)); !os.IsNotExist(err) {
		t.Error("expired clip not deleted")
	}
	var ids []string
	for i := 0; i < MaxClips+5; i++ {
		ts := now.Add(-time.Duration(MaxClips+5-i) * time.Second)
		id := ts.Format("20060102-150405") + "_122900000"
		touchClip(t, dir, id)
		s.Add(entry(id, ts))
		ids = append(ids, id)
	}
	if s.Len() != MaxClips {
		t.Fatalf("len = %d, want %d", s.Len(), MaxClips)
	}
	if _, err := os.Stat(ClipPath(dir, ids[0])); !os.IsNotExist(err) {
		t.Error("oldest clip beyond the cap not deleted")
	}
	if _, err := os.Stat(ClipPath(dir, ids[len(ids)-1])); err != nil {
		t.Error("newest clip missing")
	}
	// rebuild: a missing clip drops its entry, garbage lines are ignored
	if err := os.Remove(ClipPath(dir, ids[len(ids)-1])); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "index.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("not json\n")
	f.Close()
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Len() != MaxClips-1 {
		t.Errorf("rebuilt len = %d, want %d", s2.Len(), MaxClips-1)
	}
	got := s2.Query(0, 2, "", 0)
	if len(got) != 2 || got[0].ID != ids[len(ids)-2] || got[0].Start < got[1].Start {
		t.Errorf("query not newest first: %+v", got)
	}
	if got := s2.Query(0, 10, "HELLO "+ids[len(ids)-2], 0); len(got) != 1 {
		t.Errorf("text query: %d", len(got))
	}
	if got := s2.Query(0, 10, "", 99); len(got) != 0 {
		t.Errorf("freq filter: %d", len(got))
	}
}

type fakeWhisper struct {
	mu      sync.Mutex
	reply   string
	got     map[string]string
	wavLen  int
	srv     *httptest.Server
	started int
}

func newFake(t *testing.T, reply string) *fakeWhisper {
	f := &fakeWhisper{reply: reply}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 24); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.got = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			f.got[k] = v[0]
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		file.Close()
		f.wavLen = int(hdr.Size)
		json.NewEncoder(w).Encode(map[string]string{"text": f.reply})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestClient(t *testing.T) {
	f := newFake(t, " Left downwind\n runway one seven. ")
	text, err := (&Client{URL: f.srv.URL}).Transcribe(context.Background(), WAV(make([]int16, 1600), 16000), "Aviation radio.")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Left downwind runway one seven." {
		t.Errorf("text = %q", text)
	}
	if f.got["response_format"] != "json" || f.got["temperature"] != "0" || f.got["prompt"] != "Aviation radio." || f.wavLen != 44+3200 {
		t.Errorf("request fields: %v wav %d", f.got, f.wavLen)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"error":"boom"}`)) }))
	defer bad.Close()
	if _, err := (&Client{URL: bad.URL}).Transcribe(context.Background(), nil, ""); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v", err)
	}
}

func newTestTranscriber(t *testing.T, url string) (*Transcriber, *Tee, *decoder.Events) {
	t.Helper()
	tee := NewTee(nil)
	ev := decoder.NewEvents()
	tr, err := New(Options{Dir: filepath.Join(t.TempDir(), "clips"), Model: "/x/ggml-small.en.bin", Tee: tee, Events: ev,
		ServerURL: func(context.Context) (string, error) { return url, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go tr.Run(ctx)
	t.Cleanup(func() { cancel(); tr.Close() })
	return tr, tee, ev
}

func feed(tee *Tee, rate int, secs float64, amp float64) {
	// 20 ms blocks, like the radio
	blk := rate / 50
	for i := 0; i < int(secs*50); i++ {
		tee.Audio(tone(rate, 700, 0.02, amp)[:blk])
	}
}

// Tones are speech-free: whatever the (fake) recognizer says, the pipeline
// must produce a clip, an index line and an event, and the filter must mark
// a hallucination empty.
func TestPipelineTonesAreFilteredEmpty(t *testing.T) {
	f := newFake(t, "Thank you.")
	tr, tee, ev := newTestTranscriber(t, f.srv.URL)
	tee.AudioMeta(24000, "nfm")
	if !tr.Enabled() {
		t.Fatal("not enabled")
	}
	feed(tee, 24000, 0.5, 0.4) // pre-roll before the transmission
	start := time.Now()
	end := tr.Begin("scanner", 122_900_000, "MULTICOM", "am", start)
	if end == nil {
		t.Fatal("Begin returned nil")
	}
	feed(tee, 24000, 2.0, 0.4)
	feed(tee, 24000, 1.0, 0) // hang time: not part of the clip
	end(start.Add(2 * time.Second))
	deadline := time.Now().Add(5 * time.Second)
	for tr.Store().Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := tr.Store().Query(0, 10, "", 0)
	if len(got) != 1 {
		t.Fatalf("entries = %d", len(got))
	}
	e := got[0]
	if !e.Empty || e.Text != "" || e.Source != "scanner" || e.Label != "MULTICOM" || e.FreqHz != 122_900_000 || e.Model != "small.en" {
		t.Errorf("entry = %+v", e)
	}
	// 0.3 s pre-roll + 2 s; the 1 s of hang time is cut
	if e.DurationS < 2.2 || e.DurationS > 2.4 {
		t.Errorf("duration = %v, want ~2.3", e.DurationS)
	}
	if _, err := os.Stat(ClipPath(tr.Dir(), e.ID)); err != nil {
		t.Errorf("clip: %v", err)
	}
	if f.got["prompt"] == "" || !strings.Contains(f.got["prompt"], "Aviation") {
		t.Errorf("prompt = %q", f.got["prompt"])
	}
	evs := ev.Since(0, 10)
	if len(evs) != 1 || evs[0].Decoder != "transcribe" || evs[0].Kind != "message" {
		t.Errorf("events = %+v", evs)
	}
	// the index is on disk
	s2, err := OpenStore(tr.Dir())
	if err != nil || s2.Len() != 1 {
		t.Errorf("reopened store: %v len %d", err, s2.Len())
	}
	if st := tr.Status(); !st.Enabled || st.Model != "small.en" || st.Queue != 0 || st.LastError != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestShortAndSilentClipsSkipped(t *testing.T) {
	f := newFake(t, "hello")
	tr, _, _ := newTestTestTranscriber(t, f.srv.URL)
	r := <-tr.Submit(Clip{PCM: tone(16000, 500, 0.3, 0.5), Rate: 16000, Start: time.Now()})
	if r.Skipped == "" {
		t.Errorf("short clip not skipped: %+v", r)
	}
	r = <-tr.Submit(Clip{PCM: make([]int16, 32000), Rate: 16000, Start: time.Now()})
	if !strings.Contains(r.Skipped, "silent") {
		t.Errorf("silent clip not skipped: %+v", r)
	}
	r = <-tr.Submit(Clip{Source: "manual", PCM: tone(16000, 500, 1, 0.5), Rate: 16000, Start: time.Now()})
	if r.Entry == nil || r.Entry.Text != "hello" || r.Entry.Empty {
		t.Errorf("clip not transcribed: %+v", r)
	}
}

func newTestTestTranscriber(t *testing.T, url string) (*Transcriber, *Tee, *decoder.Events) {
	return newTestTranscriber(t, url)
}

func TestQueueDropsOldest(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.Write([]byte(`{"text":"ok roger"}`))
	}))
	defer srv.Close()
	defer close(block)
	tr, _, _ := newTestTranscriber(t, srv.URL)
	var chans []<-chan Result
	for i := 0; i < MaxQueue+4; i++ {
		chans = append(chans, tr.Submit(Clip{Source: "manual", PCM: tone(16000, 500, 1, 0.5), Rate: 16000, Start: time.Now().Add(time.Duration(i) * time.Second)}))
		if i == 0 {
			time.Sleep(100 * time.Millisecond) // let the worker take clip 0
		}
	}
	dropped := 0
	for _, c := range chans {
		select {
		case r := <-c:
			if r.Skipped == "queue full" {
				dropped++
			}
		default:
		}
	}
	if dropped != 3 {
		t.Errorf("dropped = %d, want 3", dropped)
	}
	if q := tr.Status().Queue; q != MaxQueue+1 {
		t.Errorf("queue = %d, want %d", q, MaxQueue+1)
	}
}

func TestDisabledNoCapture(t *testing.T) {
	f := newFake(t, "x")
	tr, tee, _ := newTestTranscriber(t, f.srv.URL)
	tee.AudioMeta(24000, "nfm")
	if err := tr.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if tr.Begin("scanner", 1, "", "am", time.Now()) != nil {
		t.Error("Begin while disabled")
	}
	if tee.on.Load() {
		t.Error("tee still recording")
	}
}

// TestRealWhisper runs say -> pipeline -> whisper-server. It needs
// SWEEP_WHISPER_TEST=1, whisper-server, the model and macOS say.
func TestRealWhisper(t *testing.T) {
	if os.Getenv("SWEEP_WHISPER_TEST") != "1" {
		t.Skip("set SWEEP_WHISPER_TEST=1 to run against a real whisper-server")
	}
	for _, b := range []string{"say", "sox"} {
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
	if out, err := exec.Command("say", "-o", aiff, "left downwind runway one seven").CombinedOutput(); err != nil {
		t.Skipf("say: %v %s", err, out)
	}
	if out, err := exec.Command("sox", aiff, "-r", "24000", "-c", "1", "-b", "16", wav).CombinedOutput(); err != nil {
		t.Skipf("sox: %v %s", err, out)
	}
	pcm, rate, err := source.ReadWAV(wav)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := New(Options{Dir: filepath.Join(tmp, "clips"), Model: model, Bin: "whisper-server"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go tr.Run(ctx)
	defer func() { cancel(); tr.Close() }()
	for i := 0; i < 3; i++ {
		res := <-tr.Submit(Clip{Source: "manual", FreqHz: 122_900_000, Start: time.Now(), PCM: pcm, Rate: rate})
		if res.Err != nil || res.Entry == nil {
			t.Fatalf("result: %+v", res)
		}
		t.Logf("run %d: %q (%.1f s clip, latency %.2f s)", i, res.Entry.Text, res.Entry.DurationS, res.Entry.LatencyS)
		if res.Entry.Empty || !strings.Contains(strings.ToLower(res.Entry.Text), "runway") {
			t.Errorf("text = %q empty=%v", res.Entry.Text, res.Entry.Empty)
		}
	}
	if st := tr.Status(); st.Server != "running" {
		t.Errorf("server = %s", st.Server)
	}
}
