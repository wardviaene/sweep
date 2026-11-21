package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/wardviaene/sweep/internal/autopilot"
	"github.com/wardviaene/sweep/internal/scanner"
)

// routeRE matches mux.HandleFunc("GET /api/x", ...) and mux.Handle("/", ...).
var routeRE = regexp.MustCompile(`mux\.Handle(?:Func)?\(\s*"(?:([A-Z]+) )?(/[^"]*)"`)

// sourceRoutes returns the registered routes as "METHOD /path" (method "WS"
// for /ws/ paths, which are all GET upgrades). The bare "/" static UI route is
// not part of the API and is skipped.
func sourceRoutes(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range routeRE.FindAllStringSubmatch(string(b), -1) {
			method, path := m[1], m[2]
			if method == "" && path == "/" {
				continue
			}
			if strings.HasPrefix(path, "/ws/") {
				method = "WS"
			}
			out[method+" "+path] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("no routes found in source; is the regexp stale?")
	}
	return out
}

// docRoutes scans docs/openapi.yaml for paths: and x-websockets: entries
// without a YAML parser (the repo takes no new dependencies).
func docRoutes(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	section, path := "", ""
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		key := strings.TrimSpace(line)
		switch {
		case indent == 0:
			section, path = strings.TrimSuffix(key, ":"), ""
		case indent == 2 && (section == "paths" || section == "x-websockets") && strings.HasSuffix(key, ":"):
			path = strings.TrimSuffix(key, ":")
			if section == "x-websockets" {
				out["WS "+path] = true
			}
		case indent == 4 && section == "paths" && path != "":
			name := strings.TrimSuffix(strings.SplitN(key, ":", 2)[0], "")
			switch name {
			case "get", "post", "put", "delete", "patch", "head", "options":
				out[strings.ToUpper(name)+" "+path] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no routes found in docs/openapi.yaml")
	}
	return out
}

func TestOpenAPIMatchesRoutes(t *testing.T) {
	src, doc := sourceRoutes(t), docRoutes(t)
	var missing, extra []string
	for r := range src {
		if !doc[r] {
			missing = append(missing, r)
		}
	}
	for r := range doc {
		if !src[r] {
			extra = append(extra, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("routes registered in internal/server but missing from docs/openapi.yaml (paths: or x-websockets:): %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("routes in docs/openapi.yaml that no handler registers: %v", extra)
	}
}

// TestDocumentedRoutesAreServed asks the real mux which pattern serves each
// documented route, so a renamed pattern that the regexp missed still fails.
func TestDocumentedRoutesAreServed(t *testing.T) {
	mux, ok := Handler(Deps{
		Scanner:   scanner.New(scanner.Deps{}),
		Autopilot: autopilot.New(autopilot.Deps{DataDir: t.TempDir()}),
	}).(*http.ServeMux)
	if !ok {
		t.Skip("Handler no longer returns *http.ServeMux")
	}
	for r := range docRoutes(t) {
		method, path, _ := strings.Cut(r, " ")
		if method == "WS" {
			method = http.MethodGet
		}
		req := httptest.NewRequest(method, path, nil)
		if _, pattern := mux.Handler(req); pattern != method+" "+path {
			t.Errorf("%s %s is documented but the mux matches %q", method, path, pattern)
		}
	}
}

func TestAPIDocsMention(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "api.md"))
	if err != nil {
		t.Fatal(err)
	}
	for r := range docRoutes(t) {
		_, path, _ := strings.Cut(r, " ")
		if !strings.Contains(string(b), path) {
			t.Errorf("docs/api.md never mentions %s", path)
		}
	}
}
