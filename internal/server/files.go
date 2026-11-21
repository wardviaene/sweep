package server

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// fileNameRE is a plain file name: no separators, no leading dot (this also
// hides temporary files), at most 128 characters.
var fileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// fileTypes maps extensions of served files to their Content-Type.
var fileTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8",
	".wav":  "audio/wav",
}

// filesHandler serves GET /api/files/{kind}/{name}: read-only access to the
// files sdrd writes (SSTV pictures, pass records). kind selects the directory;
// name must be a plain file name that exists as a regular file there.
func filesHandler(d Deps) http.HandlerFunc {
	dirs := map[string]string{"sstv": d.SSTVDir, "passes": d.PassesDir}
	return func(w http.ResponseWriter, req *http.Request) {
		dir, ok := dirs[req.PathValue("kind")]
		if !ok || dir == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown file kind (want sstv or passes)"})
			return
		}
		serveDataFile(w, req, dir, req.PathValue("name"))
	}
}

// passFileHandler serves GET /api/files/passes/{dir}/{name}: a file inside one
// pass directory (pass.json, pass.wav, image-NN.png, packets.jsonl).
func passFileHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		sub := req.PathValue("dir")
		if d.PassesDir == "" || !fileNameRE.MatchString(sub) || strings.Contains(sub, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pass directory"})
			return
		}
		dir := filepath.Join(d.PassesDir, sub)
		if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such pass"})
			return
		}
		serveDataFile(w, req, dir, req.PathValue("name"))
	}
}

// serveDataFile serves a plain, regular file name from dir (no symlinks).
func serveDataFile(w http.ResponseWriter, req *http.Request, dir, name string) {
	{
		if !fileNameRE.MatchString(name) || strings.Contains(name, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file name"})
			return
		}
		p := filepath.Join(dir, name)
		// Lstat: a symlink in the directory is not followed.
		if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such file"})
			return
		}
		f, err := os.Open(p)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such file"})
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such file"})
			return
		}
		ct := fileTypes[strings.ToLower(filepath.Ext(name))]
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache") // pictures are rewritten while they arrive
		http.ServeContent(w, req, name, st.ModTime(), f)
	}
}
