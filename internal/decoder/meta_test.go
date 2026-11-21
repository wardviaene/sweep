package decoder

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/meta"
)

type fakeMeta struct{}

func (fakeMeta) Aircraft(hex, callsign string) meta.AircraftInfo {
	if hex != "a1b2c3" {
		return meta.AircraftInfo{}
	}
	info := meta.AircraftInfo{Country: "United States", Registration: "N123AB", Type: "BOEING 737-800", Year: 2015}
	if callsign == "UAL123" {
		info.Airline = &meta.Airline{ICAO: "UAL", Name: "United Airlines", Radio: "UNITED"}
		info.FlightNumber = "123"
	}
	return info
}

func (fakeMeta) VisibleSatellite(f float64) (int, string, bool) {
	if f > 145.8e6-15000 && f < 145.8e6+15000 {
		return 25544, "ISS", true
	}
	return 0, "", false
}

func TestEnrichADSBSnapshot(t *testing.T) {
	tbl := newAircraftTable()
	now := time.Now()
	tbl.applySBS("MSG,3,1,1,A1B2C3,1,2024/01/01,00:00:00.000,2024/01/01,00:00:00.000,UAL123,35000,450,90,12.34,56.78,0,,0,0,0,0", now)
	tbl.applySBS("MSG,1,1,1,3C4DD2,1,2024/01/01,00:00:00.000,2024/01/01,00:00:00.000,DLH4AB,,,,,,,,,,,", now)
	snap := tbl.snapshot(now)

	m := &Manager{}
	m.enrichAircraft(snap) // nil Meta: no-op
	if b, _ := json.Marshal(snap[0]); json.Valid(b) && snap[0].Country != "" {
		t.Fatalf("enriched without Meta: %s", b)
	}

	m.Meta = fakeMeta{}
	m.enrichAircraft(snap)
	var got, other map[string]any
	for _, a := range snap {
		b, _ := json.Marshal(a)
		var v map[string]any
		json.Unmarshal(b, &v)
		if a.Hex == "a1b2c3" {
			got = v
		} else {
			other = v
		}
	}
	if got["country"] != "United States" || got["registration"] != "N123AB" || got["type"] != "BOEING 737-800" ||
		got["year"] != float64(2015) || got["flight_number"] != "123" || got["flight"] != "UAL123" {
		t.Errorf("%v", got)
	}
	if al, _ := got["airline"].(map[string]any); al["icao"] != "UAL" || al["name"] != "United Airlines" || al["radio"] != "UNITED" || al["country"] != nil {
		t.Errorf("airline %v", got["airline"])
	}
	for _, k := range []string{"country", "registration", "type", "year", "airline", "flight_number"} {
		if _, ok := other[k]; ok {
			t.Errorf("unknown aircraft has %s", k)
		}
	}
}

func TestEnrichUATAircraft(t *testing.T) {
	m := &Manager{Meta: fakeMeta{}}
	a := normUATAircraft(map[string]any{"hex": "~A1B2C3", "flight": "UAL123 ", "lat": 12.34})
	m.enrichAircraftMap(a)
	if a["country"] != "United States" || a["registration"] != "N123AB" || a["flight_number"] != "123" || a["year"] != 2015 {
		t.Errorf("%v", a)
	}
	if al, _ := a["airline"].(map[string]any); al["name"] != "United Airlines" {
		t.Errorf("%v", a["airline"])
	}
	b := normUATAircraft(map[string]any{"hex": "ffffff"})
	m.enrichAircraftMap(b)
	if len(b) != 1 {
		t.Errorf("%v", b)
	}
	(&Manager{}).enrichAircraftMap(a) // nil Meta
}

func TestTagSatellite(t *testing.T) {
	m := &Manager{Meta: fakeMeta{}}
	p, _ := parseAPRS("N0CALL>APRS,ARISS:!1234.00N/05646.00E-test")
	m.tagSatellite(p, 145_810_000)
	if s, _ := p["satellite"].(map[string]any); s["norad"] != 25544 || s["name"] != "ISS" {
		t.Errorf("%v", p)
	}
	q, _ := parseAPRS("N0CALL>APRS:>x")
	m.tagSatellite(q, 437_000_000)
	if _, ok := q["satellite"]; ok {
		t.Error("tagged without a match")
	}
	(&Manager{}).tagSatellite(q, 145_810_000)
}
