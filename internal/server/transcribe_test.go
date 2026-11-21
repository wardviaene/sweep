package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/transcribe"
)

func TestTranscribeRoutes(t *testing.T) {
	dir := t.TempDir()
	tr, err := transcribe.New(transcribe.Options{Dir: filepath.Join(dir, "clips"), DataDir: dir, Model: "/x/ggml-small.en.bin",
		Tee: transcribe.NewTee(nil), ServerURL: func(context.Context) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	id := time.Now().Format("20060102-150405") + "_122900000"
	if err := transcribe.WriteWAV(transcribe.ClipPath(tr.Dir(), id), []int16{1, 2, 3}, 16000); err != nil {
		t.Fatal(err)
	}
	tr.Store().Add(transcribe.Entry{ID: id, Source: "scanner", FreqHz: 122_900_000, Start: float64(time.Now().Unix()), Text: "left downwind runway one seven", URL: transcribe.URLFor(id)})
	h := Handler(Deps{Transcribe: tr})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	var es []transcribe.Entry
	w := do("GET", "/api/transcripts?q=RUNWAY&freq_hz=122900000", "")
	if json.Unmarshal(w.Body.Bytes(), &es) != nil || len(es) != 1 || es[0].ID != id {
		t.Errorf("transcripts: %d %s", w.Code, w.Body)
	}
	if w = do("GET", "/api/transcripts?q=nothing", ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("no match: %s", w.Body)
	}
	if w = do("GET", "/api/transcripts?limit=0", ""); w.Code != 400 {
		t.Errorf("limit=0: %d", w.Code)
	}
	w = do("PUT", "/api/transcribe", `{"enabled": false}`)
	var st transcribe.Status
	if json.Unmarshal(w.Body.Bytes(), &st) != nil || st.Enabled || st.Model != "small.en" {
		t.Errorf("put: %d %s", w.Code, w.Body)
	}
	if w = do("PUT", "/api/transcribe", `{}`); w.Code != 400 {
		t.Errorf("put without enabled: %d", w.Code)
	}
	if w = do("POST", "/api/transcribe", `{"seconds": 5}`); w.Code != http.StatusConflict {
		t.Errorf("post while off: %d %s", w.Code, w.Body)
	}
	if w = do("POST", "/api/transcribe", `{"seconds": 500}`); w.Code != 400 {
		t.Errorf("post 500 s: %d", w.Code)
	}
	if w = do("POST", "/api/transcribe", `{"path": "/etc/hosts"}`); w.Code != 400 {
		t.Errorf("post outside data dir: %d", w.Code)
	}
	date, name, _ := strings.Cut(id, "-")
	if w = do("GET", "/api/files/clips/"+date+"/"+name+".wav", ""); w.Code != 200 || w.Header().Get("Content-Type") != "audio/wav" {
		t.Errorf("clip: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	for _, p := range []string{"/api/files/clips/" + date + "/index.jsonl", "/api/files/clips/..%2f..%2fx/y.wav", "/api/files/clips/" + date + "/999999_1.wav"} {
		if w = do("GET", p, ""); w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	// no transcriber: the routes still answer
	h = Handler(Deps{})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/transcribe", nil))
	if w.Code != 200 {
		t.Errorf("nil transcriber: %d", w.Code)
	}
}
