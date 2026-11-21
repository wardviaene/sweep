package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wardviaene/sweep/internal/source"
	"github.com/wardviaene/sweep/internal/transcribe"
)

var (
	clipDateRE = regexp.MustCompile(`^\d{8}$`)
	clipNameRE = regexp.MustCompile(`^\d{6}_\d+(-\d+)?\.wav$`)
)

// transcribeRoutes serves docs/transcription.md: the transcripts, the on/off
// switch, manual recording and the clip files. d.Transcribe may be nil.
func transcribeRoutes(mux *http.ServeMux, d Deps) {
	tr := d.Transcribe
	mux.HandleFunc("GET /api/transcripts", func(w http.ResponseWriter, req *http.Request) {
		if tr == nil {
			writeJSON(w, http.StatusOK, []transcribe.Entry{})
			return
		}
		q := req.URL.Query()
		var since float64
		if v := q.Get("since"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "since: " + err.Error()})
				return
			}
			since = f
		}
		limit, err := qint(req, "limit", 100, 1, 1000)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		freq, err := qint(req, "freq_hz", 0, 0, 2_000_000_000)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, tr.Store().Query(since, limit, q.Get("q"), uint32(freq)))
	})
	mux.HandleFunc("GET /api/transcribe", func(w http.ResponseWriter, _ *http.Request) {
		if tr == nil {
			writeJSON(w, http.StatusOK, transcribe.Status{Server: "stopped", LastError: "transcription is not available"})
			return
		}
		writeJSON(w, http.StatusOK, tr.Status())
	})
	mux.HandleFunc("PUT /api/transcribe", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&body); err != nil || body.Enabled == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `want {"enabled": true|false}`})
			return
		}
		if tr == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "transcription is not available"})
			return
		}
		if err := tr.SetEnabled(*body.Enabled); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, tr.Status())
	})
	mux.HandleFunc("POST /api/transcribe", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Seconds float64 `json:"seconds"`
			Path    string  `json:"path"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if tr == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "transcription is not available"})
			return
		}
		var res transcribe.Result
		var err error
		switch {
		case body.Path != "":
			pcm, rate, perr := readDataWAV(tr.DataDir(), body.Path)
			if perr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": perr.Error()})
				return
			}
			res, err = tr.TranscribeFile(req.Context(), pcm, rate, time.Now().Add(-time.Duration(float64(len(pcm))/rate*float64(time.Second))))
		case body.Seconds >= 1 && body.Seconds <= 60:
			res, err = tr.Record(req.Context(), body.Seconds)
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `want {"seconds": 1..60} or {"path": "<WAV in the data dir>"}`})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if res.Entry == nil {
			writeJSON(w, http.StatusOK, map[string]any{"text": "", "empty": true, "skipped": res.Skipped})
			return
		}
		writeJSON(w, http.StatusOK, res.Entry)
	})
	// Clips live one directory deeper (<date>/<name>) than the files of
	// GET /api/files/{kind}/{name}, so they get their own, stricter route.
	mux.HandleFunc("GET /api/files/clips/{date}/{name}", func(w http.ResponseWriter, req *http.Request) {
		date, name := req.PathValue("date"), req.PathValue("name")
		if tr == nil || !clipDateRE.MatchString(date) || !clipNameRE.MatchString(name) {
			http.NotFound(w, req)
			return
		}
		p := filepath.Join(tr.Dir(), date, name)
		if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() { // symlinks are not followed
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such file"})
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, req, p)
	})
}

// readDataWAV reads a WAV that lies inside the data directory.
func readDataWAV(dataDir, path string) ([]int16, float64, error) {
	if dataDir == "" {
		return nil, 0, errors.New("no data dir configured")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dataDir, path)
	}
	root, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return nil, 0, err
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, 0, errors.New("no such file in the data dir")
	}
	if rel, err := filepath.Rel(root, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, 0, errors.New("path must be inside the data dir")
	}
	pcm, rate, err := source.ReadWAV(p)
	if err != nil {
		return nil, 0, err
	}
	if rate < 4000 || len(pcm) == 0 {
		return nil, 0, errors.New("unusable WAV")
	}
	return pcm, rate, nil
}
