package server

import (
	"strings"
	"testing"
)

func TestScannerAPI(t *testing.T) {
	e := newEnv(t)
	if code, b := e.do(t, "POST", "/api/scanner", map[string]any{"band": "nope"}); code != 400 {
		t.Fatalf("bad band: %d %s", code, b)
	}
	if code, b := e.do(t, "POST", "/api/scanner/lockout", map[string]any{"freq_hz": 440_000_000}); code != 409 {
		t.Fatalf("lockout while stopped: %d %s", code, b)
	}
	if code, b := e.do(t, "GET", "/api/scanner", nil); code != 200 || !strings.Contains(string(b), `"state":"idle"`) {
		t.Fatalf("idle: %d %s", code, b)
	}
	req := map[string]any{"lo_hz": 439_500_000, "hi_hz": 440_500_000, "mode": "nfm"}
	if code, b := e.do(t, "POST", "/api/scanner", req); code != 202 || !strings.Contains(string(b), `"watching"`) {
		t.Fatalf("start: %d %s", code, b)
	}
	if code, _ := e.do(t, "POST", "/api/scanner", req); code != 202 {
		t.Fatalf("reconfigure: %d", code)
	}
	if code, b := e.do(t, "POST", "/api/survey", map[string]any{"include_fair": true}); code != 409 {
		t.Fatalf("survey while scanning: %d %s", code, b)
	}
	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/api/tune", map[string]any{"freq_hz": 433_920_000}},
		{"/api/demod", map[string]any{"mode": "off"}},
		{"/api/decoder", map[string]any{"decoder": "none"}},
	} {
		if code, b := e.do(t, "POST", c.path, c.body); code != 409 || !strings.Contains(string(b), "scanner running") {
			t.Fatalf("%s while scanning: %d %s", c.path, code, b)
		}
	}
	if code, b := e.do(t, "POST", "/api/scanner/lockout", map[string]any{"freq_hz": 440_250_000}); code != 200 || !strings.Contains(string(b), "440250000") {
		t.Fatalf("lockout: %d %s", code, b)
	}
	if code, b := e.do(t, "GET", "/api/config", nil); !strings.Contains(string(b), `"scanner_lockouts":{"custom":[440250000]}`) {
		t.Fatalf("config: %d %s", code, b)
	}
	if code, b := e.do(t, "POST", "/api/scanner/lockout", map[string]any{"freq_hz": 440_250_000, "remove": true}); code != 200 || strings.Contains(string(b), "440250000") {
		t.Fatalf("unlock: %d %s", code, b)
	}
	if code, b := e.do(t, "POST", "/api/scanner/stop", nil); code != 200 || !strings.Contains(string(b), `"stopped"`) {
		t.Fatalf("stop: %d %s", code, b)
	}
	if code, b := e.do(t, "POST", "/api/tune", map[string]any{"freq_hz": 433_920_000}); code != 200 {
		t.Fatalf("tune after stop: %d %s", code, b)
	}
}
