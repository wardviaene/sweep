package decoder

import (
	"testing"
	"time"
)

func TestParseAPRS(t *testing.T) {
	p, ok := parseAPRS("N0CALL-9>APDW18,WIDE1-1,WIDE2-1:!4903.50N/07201.75W>Test 123<0x0a>")
	if !ok {
		t.Fatal("not parsed")
	}
	if p["from"] != "N0CALL-9" || p["to"] != "APDW18" || len(p["path"].([]string)) != 2 {
		t.Fatalf("%v", p)
	}
	if p["lat"] != 49.058333 || p["lon"] != -72.029167 || p["comment"] != "Test 123" {
		t.Fatalf("position %v %v %q", p["lat"], p["lon"], p["comment"])
	}
	if _, ok := parseAPRS("Ready to accept KISS TCP client"); ok {
		t.Fatal("non-packet parsed")
	}
}

func TestAISType1(t *testing.T) {
	// Example from the gpsd AIVDM/AIVDO protocol documentation.
	m, ok := newAISAssembler().add("!AIVDM,1,1,,B,177KQJ5000G?tO`K>RA1wUbN0TKH,0*5C")
	if !ok {
		t.Fatal("not decoded")
	}
	if m["mmsi"] != uint64(477553000) || m["type"] != 1 {
		t.Fatalf("%v", m)
	}
	if m["lat"] != 47.582833 || m["lon"] != -122.345833 {
		t.Fatalf("position %v %v", m["lat"], m["lon"])
	}
	if m["cog"] != 51.0 || m["heading"] != uint64(181) {
		t.Fatalf("cog %v heading %v", m["cog"], m["heading"])
	}
}

func TestSBS(t *testing.T) {
	tb := newAircraftTable()
	now := time.Now()
	tb.applySBS("MSG,1,1,1,A1B2C3,1,2026/11/21,12:00:00.000,2026/11/21,12:00:00.000,UAL123  ,,,,,,,,,,,", now)
	tb.applySBS("MSG,3,1,1,A1B2C3,1,2026/11/21,12:00:00.000,2026/11/21,12:00:00.000,,35000,,,40.1,-74.2,,,0,0,0,0", now)
	s := tb.snapshot(now.Add(500 * time.Millisecond))
	if len(s) != 1 || s[0].Hex != "a1b2c3" || s[0].Flight != "UAL123" || *s[0].AltBaro != 35000 || *s[0].Lat != 40.1 || s[0].Messages != 2 {
		t.Fatalf("%+v", s)
	}
	if len(tb.snapshot(now.Add(2*time.Minute))) != 0 {
		t.Fatal("stale aircraft kept")
	}
}
