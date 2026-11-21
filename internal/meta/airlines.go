package meta

import (
	"encoding/csv"
	"errors"
	"io"
	"strings"
)

// Airline is an ICAO airline designator with its name and radio callsign.
type Airline struct {
	ICAO    string `json:"icao"`
	Name    string `json:"name"`
	Radio   string `json:"radio,omitempty"`
	Country string `json:"country,omitempty"`
}

// parseAirlines reads OpenFlights airlines.dat:
// id,name,alias,iata,icao,callsign,country,active. Active airlines win over
// defunct ones that reused a designator.
func parseAirlines(r io.Reader) (map[string]Airline, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	out := map[string]Airline{}
	active := map[string]bool{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}
		if len(rec) < 8 {
			continue
		}
		icao := strings.ToUpper(strings.TrimSpace(rec[4]))
		if !validICAO(icao) {
			continue
		}
		isActive := rec[7] == "Y"
		if _, dup := out[icao]; dup && (active[icao] || !isActive) {
			continue
		}
		out[icao] = Airline{ICAO: icao, Name: nz(rec[1]), Radio: nz(rec[5]), Country: nz(rec[6])}
		active[icao] = isActive
	}
	return out, nil
}

// nz maps OpenFlights' \N null marker to "".
func nz(s string) string {
	s = strings.TrimSpace(s)
	if s == `\N` {
		return ""
	}
	return s
}

// validICAO accepts three letters or digits and rejects OpenFlights' "N/A".
func validICAO(s string) bool {
	if len(s) != 3 || s == "N/A" {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
