package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/scanner"
	"github.com/wardviaene/sweep/internal/survey"
)

type testEnv struct {
	srv    *httptest.Server
	radio  *radio.Radio
	events *decoder.Events
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	t.Setenv("PATH", t.TempDir()) // no decoder tools: decoder steps must fail cleanly
	gain := 20.7
	hub, audio := NewHub(), NewAudioHub()
	r := radio.New(radio.Config{Synth: true, CenterHz: 440_000_000, SampleRate: 2_048_000, GainDB: &gain,
		FFTSize: 2048, FPS: 20, MinDB: -90, MaxDB: -20}, hub, audio)
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
	sc := scanner.New(scanner.Deps{Radio: r, Decoders: dec, Config: store})
	t.Cleanup(func() { sc.Stop() })
	srv := httptest.NewServer(Handler(Deps{Scanner: sc, Radio: r, Hub: hub, Audio: audio, Decoders: dec, Events: ev, Config: store,
		RecordDir: t.TempDir(), Survey: survey.New(survey.Deps{Radio: r, Spectrum: hub, Decoders: dec, Config: store})}))
	t.Cleanup(srv.Close)
	waitFor(t, func() bool { _, _, _, err := hub.Aggregate(1, "max"); return err == nil })
	return &testEnv{srv, r, ev}
}

func (e *testEnv) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(b))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timeout")
}

func TestAntennaConfig(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do(t, "PUT", "/api/config", map[string]any{"antenna": "yagi9000"}); code != 400 {
		t.Fatalf("unknown antenna: %d", code)
	}
	code, b := e.do(t, "PUT", "/api/config", map[string]any{"antenna": "jpole"})
	if code != 200 || !strings.Contains(string(b), `"antenna":"jpole"`) {
		t.Fatalf("set: %d %s", code, b)
	}
	if code, b := e.do(t, "PUT", "/api/config", map[string]any{"meter_ids": []string{"1"}}); code != 200 || !strings.Contains(string(b), "jpole") {
		t.Fatalf("merge lost antenna: %d %s", code, b)
	}
	if _, b := e.do(t, "PUT", "/api/config", map[string]any{"antenna": nil}); strings.Contains(string(b), "antenna") {
		t.Fatalf("clear: %s", b)
	}
}

func TestSurveyScanAndMissingTool(t *testing.T) {
	e := newEnv(t)
	if code, b := e.do(t, "POST", "/api/survey", map[string]any{"steps": []string{"nope"}}); code != 400 || !strings.Contains(string(b), "scan:ism433") {
		t.Fatalf("bad step: %d %s", code, b)
	}
	if code, _ := e.do(t, "POST", "/api/survey", map[string]any{"seconds": 2}); code != 400 {
		t.Fatalf("seconds: %d", code)
	}
	before := e.radio.Status()
	_, _ = e.radio.SetDemod(radio.DemodRequest{Mode: ptr("nfm"), FreqHz: ptr(uint32(440_100_000))})
	before = e.radio.Status()

	code, b := e.do(t, "POST", "/api/survey", map[string]any{"antenna": "wideband", "steps": []string{"scan:ism433", "adsb", "packet"}})
	if code != 202 {
		t.Fatalf("start: %d %s", code, b)
	}
	// busy: a second survey, a scan and a recording are refused while it runs
	if code, _ := e.do(t, "POST", "/api/survey", map[string]any{"steps": []string{"scan:2m"}}); code != 409 {
		t.Fatalf("second survey: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/scan", map[string]any{"start_hz": 433e6, "stop_hz": 434e6}); code != 400 {
		t.Fatalf("scan during survey: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/record", map[string]any{"seconds": 1}); code != 409 {
		t.Fatalf("record during survey: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/decoder", map[string]any{"decoder": "adsb"}); code != 409 {
		t.Fatalf("decoder during survey: %d", code)
	}

	var st survey.State
	waitFor(t, func() bool {
		_, b := e.do(t, "GET", "/api/survey", nil)
		return json.Unmarshal(b, &st) == nil && st.State != "running"
	})
	if st.State != "done" || len(st.Steps) != 3 {
		t.Fatalf("final state: %+v", st)
	}
	sc, ad, pk := st.Steps[0], st.Steps[1], st.Steps[2]
	if sc.State != "done" || sc.Found == nil || *sc.Found < 1 || len(sc.Peaks) == 0 || !strings.Contains(sc.Summary, "433.9") {
		t.Errorf("scan step: %+v", sc)
	}
	if ad.State != "error" || !strings.Contains(ad.Error, "not found") {
		t.Errorf("adsb step: %+v", ad)
	}
	if pk.State != "skipped" || pk.Reason == "" {
		t.Errorf("packet step: %+v", pk)
	}
	after := e.radio.Status()
	if after.CenterHz != before.CenterHz || after.SampleRate != before.SampleRate ||
		*after.GainDB != *before.GainDB || after.Demod.Mode != "nfm" || after.Demod.FreqHz != before.Demod.FreqHz {
		t.Errorf("tuning not restored:\nbefore %+v\nafter  %+v", before, after)
	}
	var logs int
	for _, ev := range e.events.Since(0, 100) {
		if ev.Decoder == "survey" && ev.Kind == "log" {
			logs++
		}
	}
	if logs != 3 {
		t.Errorf("%d survey log events, want 3", logs)
	}
	// the radio is free again
	if code, _ := e.do(t, "POST", "/api/scan", map[string]any{"start_hz": 433e6, "stop_hz": 434e6}); code != 200 {
		t.Fatalf("scan after survey: %d", code)
	}
}

func TestSurveyStop(t *testing.T) {
	e := newEnv(t)
	if code, b := e.do(t, "POST", "/api/survey", map[string]any{"antenna": "wideband", "steps": []string{"scan:70cm", "scan:ism433"}}); code != 202 {
		t.Fatalf("start: %d %s", code, b)
	}
	code, b := e.do(t, "POST", "/api/survey/stop", nil)
	var st survey.State
	if code != 200 || json.Unmarshal(b, &st) != nil || st.State != "stopped" {
		t.Fatalf("stop: %d %s", code, b)
	}
	if st.Steps[1].State != "pending" {
		t.Errorf("second step ran after stop: %+v", st.Steps[1])
	}
	if got := e.radio.Status().CenterHz; got != 440_000_000 {
		t.Errorf("center %d after stop", got)
	}
}

func ptr[T any](v T) *T { return &v }

func runSurvey(t *testing.T, e *testEnv, body map[string]any) survey.State {
	t.Helper()
	if code, b := e.do(t, "POST", "/api/survey", body); code != 202 {
		t.Fatalf("start: %d %s", code, b)
	}
	var st survey.State
	waitFor(t, func() bool {
		_, b := e.do(t, "GET", "/api/survey", nil)
		st = survey.State{}
		return json.Unmarshal(b, &st) == nil && st.State != "running"
	})
	if st.State != "done" {
		t.Fatalf("final state: %+v", st)
	}
	return st
}

func TestSurveyListen(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do(t, "POST", "/api/survey", map[string]any{"listen_seconds": 1}); code != 400 {
		t.Fatalf("listen_seconds range: %d", code)
	}
	_, _ = e.radio.SetDemod(radio.DemodRequest{Mode: ptr("am"), FreqHz: ptr(uint32(440_100_000))})
	before := e.radio.Status()

	st := runSurvey(t, e, map[string]any{"antenna": "wideband", "steps": []string{"scan:70cm"}, "listen_seconds": 2})
	l := st.Steps[0].Listened
	if !st.Listen || l == nil || l.Mode != "nfm" || l.Seconds < 1.5 || l.Seconds > 3 ||
		l.FreqHz < 433_915_000 || l.FreqHz > 433_925_000 {
		t.Fatalf("listened: %+v (step %+v)", l, st.Steps[0])
	}
	if st.Steps[0].Seconds >= 10 && st.Steps[0].Seconds-l.Seconds < 0 {
		t.Errorf("step seconds include listen time: %+v", st.Steps[0])
	}
	if st.Listening != nil {
		t.Errorf("still listening: %+v", st.Listening)
	}
	after := e.radio.Status()
	if after.CenterHz != before.CenterHz || after.Demod.Mode != "am" || after.Demod.FreqHz != before.Demod.FreqHz {
		t.Errorf("not restored:\nbefore %+v\nafter  %+v", before, after)
	}

	st = runSurvey(t, e, map[string]any{"antenna": "wideband", "steps": []string{"scan:70cm"}, "listen": false})
	if st.Listen || st.Steps[0].Listened != nil {
		t.Errorf("listen:false still listened: %+v", st.Steps[0])
	}
}
