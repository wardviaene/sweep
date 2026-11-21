package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesHandler(t *testing.T) {
	sstv, passes, secret := t.TempDir(), t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(sstv, "20261121-151200_pd120.png"), []byte("\x89PNG"), 0o644)
	os.WriteFile(filepath.Join(sstv, ".sstv-1.tmp"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(sstv, "notes"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(passes, "pass-1.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(secret, "secret.png"), []byte("secret"), 0o644)
	os.Symlink(filepath.Join(secret, "secret.png"), filepath.Join(sstv, "link.png"))
	os.Mkdir(filepath.Join(sstv, "dir.png"), 0o755)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/files/{kind}/{name}", filesHandler(Deps{SSTVDir: sstv, PassesDir: passes}))
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	for _, c := range []struct {
		path string
		code int
		ct   string
	}{
		{"/api/files/sstv/20261121-151200_pd120.png", 200, "image/png"},
		{"/api/files/passes/pass-1.json", 200, "application/json"},
		{"/api/files/sstv/notes", 200, "application/octet-stream"},
		{"/api/files/sstv/missing.png", 404, ""},
		{"/api/files/other/x.png", 404, ""},
		{"/api/files/sstv/.sstv-1.tmp", 400, ""},
		{"/api/files/sstv/..%2f..%2fetc%2fpasswd", 400, ""},
		{"/api/files/sstv/%2e%2e", 400, ""},
		{"/api/files/sstv/a%2fb.png", 400, ""},
		{"/api/files/sstv/a%5cb.png", 400, ""},
		{"/api/files/sstv/link.png", 404, ""},
		{"/api/files/sstv/dir.png", 404, ""},
		{"/api/files/passes/20261121-151200_pd120.png", 404, ""},
	} {
		w := get(c.path)
		if w.Code != c.code {
			t.Errorf("%s: %d, want %d (%s)", c.path, w.Code, c.code, w.Body.String())
			continue
		}
		if c.ct != "" && w.Header().Get("Content-Type") != c.ct {
			t.Errorf("%s: Content-Type %q, want %q", c.path, w.Header().Get("Content-Type"), c.ct)
		}
	}
	if w := get("/api/files/sstv/20261121-151200_pd120.png"); w.Body.String() != "\x89PNG" {
		t.Errorf("body %q", w.Body.String())
	}
	// disabled kind
	mux2 := http.NewServeMux()
	mux2.HandleFunc("GET /api/files/{kind}/{name}", filesHandler(Deps{SSTVDir: sstv}))
	w := httptest.NewRecorder()
	mux2.ServeHTTP(w, httptest.NewRequest("GET", "/api/files/passes/pass-1.json", nil))
	if w.Code != 404 {
		t.Errorf("passes without a dir: %d", w.Code)
	}
}
