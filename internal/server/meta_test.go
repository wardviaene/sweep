package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/meta"
)

func metaGet(t *testing.T, m *meta.DB, path string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	metaRoutes(mux, Deps{Meta: m})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%s: %v: %s", path, err, rec.Body.String())
	}
	return rec.Code, v
}

func TestMetaRoutes(t *testing.T) {
	m := meta.New(t.TempDir(), nil) // no datasets downloaded, no receiver
	code, v := metaGet(t, m, "/api/meta")
	if code != 200 || v["db_dir"] == "" || len(v["datasets"].([]any)) < 4 {
		t.Errorf("%d %v", code, v)
	}
	code, v = metaGet(t, m, "/api/meta/aircraft/A1B2C3")
	if code != 200 || v["hex"] != "a1b2c3" || v["country"] != "United States" || v["registration"] == nil {
		// registration is derived from the hex even without the FAA file
		t.Errorf("%d %v", code, v)
	}
	if code, _ = metaGet(t, m, "/api/meta/aircraft/xyz"); code != 400 {
		t.Errorf("bad hex: %d", code)
	}
	code, v = metaGet(t, m, "/api/meta/callsign/N123AB")
	if code != 200 || v["registration"] != "N123AB" {
		t.Errorf("%d %v", code, v)
	}
	code, v = metaGet(t, m, "/api/meta/callsign/UAL123")
	if code != 200 || v["callsign"] != "UAL123" || v["airline"] != nil {
		t.Errorf("%d %v", code, v)
	}
	if code, _ = metaGet(t, m, "/api/satellites/passes"); code != 400 {
		t.Errorf("passes without receiver: %d", code)
	}
	if code, _ = metaGet(t, m, "/api/satellites/overhead"); code != 400 {
		t.Errorf("overhead without receiver: %d", code)
	}
	if code, _ = metaGet(t, m, "/api/satellites/match"); code != 400 {
		t.Errorf("match without freq: %d", code)
	}
	code, v = metaGet(t, m, "/api/satellites/match?freq_hz=436500000&tolerance_hz=15000")
	if code != 200 || v["receiver_set"] != false || len(v["candidates"].([]any)) != 0 {
		t.Errorf("%d %v", code, v)
	}
	// a nil DB must work too
	if code, _ = metaGet(t, nil, "/api/meta"); code != 200 {
		t.Errorf("nil: %d", code)
	}
}

func TestLabelPeaks(t *testing.T) {
	ps := []dsp.Peak{{FreqHz: 162_400_000}, {FreqHz: 100_000_000}}
	labelPeaks(meta.New("", nil), ps)
	if ps[0].Label != "NOAA Weather Radio: WX2" || ps[1].Label != "" {
		t.Errorf("%+v", ps)
	}
	b, _ := json.Marshal(ps[1])
	if strings.Contains(string(b), "label") {
		t.Errorf("empty label must be omitted: %s", b)
	}
	labelPeaks(nil, ps) // nil DB clears nothing and does not panic
}
