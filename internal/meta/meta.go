// Package meta enriches decoded data with names from public datasets that are
// downloaded in bulk (scripts/fetch-metadata.sh) and queried offline: airline
// names, aircraft registration and type, country, satellite names and passes,
// and labels for known frequencies. Nothing here touches the network. See
// docs/metadata.md.
//
// Every method is safe on a nil *DB and returns empty results, so callers
// never need to check whether metadata is available.
package meta

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Observer is the receiver location, from the local config store.
type Observer struct {
	Lat, Lon float64 // degrees
	AltM     float64 // metres above sea level
}

// DB holds the loaded datasets. Create it with New and call Load (or Start).
type DB struct {
	dir      string
	receiver func() (Observer, bool)
	now      func() time.Time

	mu        sync.RWMutex
	airlines  map[string]Airline
	faa       *faaIndex
	blocks    []block
	channels  []channel
	names     map[int]string // satnogs satellite names by NORAD id
	trs       []transmitter
	tles      map[int]tleEntry
	sats      *satIndex
	status    map[string]*DatasetStatus
	fetched   map[string]string
	loadStart time.Time

	passMu    sync.Mutex
	passCache map[passKey]passEntry
}

// DatasetStatus is one entry of GET /api/meta.
type DatasetStatus struct {
	Name        string `json:"name"`
	Loaded      bool   `json:"loaded"`
	Records     int    `json:"records"`
	Fetched     string `json:"fetched,omitempty"`
	License     string `json:"license,omitempty"`
	Attribution string `json:"attribution,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Status is the GET /api/meta payload.
type Status struct {
	DBDir    string          `json:"db_dir"`
	Datasets []DatasetStatus `json:"datasets"`
}

var datasetOrder = []string{"airlines", "faa", "satnogs", "tle", "icao_countries"}

var datasetInfo = map[string][2]string{ // license, attribution
	"airlines":       {"ODbL 1.0", "OpenFlights"},
	"faa":            {"US public domain", "FAA aircraft registry"},
	"satnogs":        {"CC BY-SA 4.0", "SatNOGS DB"},
	"tle":            {"public", "CelesTrak"},
	"icao_countries": {"ICAO Annex 10 Vol III allocations", "bundled"},
}

// New creates an empty DB reading from dir. receiver returns the receiver
// location (ok false when unset); it may be nil.
func New(dir string, receiver func() (Observer, bool)) *DB {
	d := &DB{dir: dir, receiver: receiver, now: time.Now, status: map[string]*DatasetStatus{},
		passCache: map[passKey]passEntry{}}
	for _, n := range datasetOrder {
		info := datasetInfo[n]
		d.status[n] = &DatasetStatus{Name: n, License: info[0], Attribution: info[1], Error: "loading"}
	}
	d.loadBundled()
	return d
}

// Start loads all datasets in the background and logs what was loaded.
func (d *DB) Start() {
	if d == nil {
		return
	}
	go d.Load()
}

// Load reads every dataset from the db directory. Missing datasets are
// recorded as such, never an error.
func (d *DB) Load() {
	if d == nil {
		return
	}
	t0 := time.Now()
	d.readFetched()
	d.loadAirlines()
	d.loadFAA()
	d.loadSatnogs()
	d.loadTLE()
	debug.FreeOSMemory() // parsing garbage (FAA type table, JSON) goes back to the OS
	st := d.Status()
	var parts []string
	for _, ds := range st.Datasets {
		if ds.Loaded {
			parts = append(parts, fmt.Sprintf("%s=%d", ds.Name, ds.Records))
		}
	}
	log.Printf("metadata: loaded %s from %s in %s", strings.Join(parts, " "), d.dir, time.Since(t0).Round(time.Millisecond))
	for _, ds := range st.Datasets {
		if !ds.Loaded && ds.Error != "" {
			log.Printf("metadata: %s: %s", ds.Name, ds.Error)
		}
	}
}

// Status reports which datasets are loaded.
func (d *DB) Status() Status {
	if d == nil {
		return Status{Datasets: []DatasetStatus{}}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := Status{DBDir: d.dir, Datasets: []DatasetStatus{}}
	for _, n := range datasetOrder {
		s := *d.status[n]
		out.Datasets = append(out.Datasets, s)
	}
	return out
}

func (d *DB) setStatus(name string, loaded bool, records int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.status[name]
	s.Loaded, s.Records, s.Error = loaded, records, ""
	if err != nil {
		s.Error = err.Error()
	}
	key := name
	f := d.fetched[key]
	if f == "" && name == "tle" {
		f = d.fetched["tle_amateur"]
	}
	s.Fetched = f
}

func (d *DB) readFetched() {
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(d.dir, "fetched.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	d.mu.Lock()
	d.fetched = m
	d.mu.Unlock()
}

func missing(err error, path string) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("not downloaded: run make metadata")
	}
	return fmt.Errorf("%s: %w", filepath.Base(path), err)
}

func (d *DB) loadBundled() {
	if bundledBlocksErr != nil {
		d.setStatus("icao_countries", false, 0, bundledBlocksErr)
	} else {
		d.mu.Lock()
		d.blocks = bundledBlocks
		d.mu.Unlock()
		d.setStatus("icao_countries", true, len(bundledBlocks), nil)
	}
	d.loadChannels()
}

func (d *DB) loadAirlines() {
	p := filepath.Join(d.dir, "airlines.dat")
	f, err := os.Open(p)
	if err != nil {
		d.setStatus("airlines", false, 0, missing(err, p))
		return
	}
	defer f.Close()
	m, err := parseAirlines(f)
	if err != nil {
		d.setStatus("airlines", false, 0, err)
		return
	}
	d.mu.Lock()
	d.airlines = m
	d.mu.Unlock()
	d.setStatus("airlines", true, len(m), nil)
}

func (d *DB) loadFAA() {
	mp := filepath.Join(d.dir, "faa", "MASTER.txt")
	mf, err := os.Open(mp)
	if err != nil {
		d.setStatus("faa", false, 0, missing(err, mp))
		return
	}
	defer mf.Close()
	refs := map[string]string{}
	if rf, err := os.Open(filepath.Join(d.dir, "faa", "ACFTREF.txt")); err == nil {
		refs = parseACFTREF(rf)
		rf.Close()
	}
	idx, err := parseMaster(mf, refs)
	if err != nil {
		d.setStatus("faa", false, 0, err)
		return
	}
	d.mu.Lock()
	d.faa = idx
	d.mu.Unlock()
	d.setStatus("faa", true, len(idx.recs), nil)
}

// AircraftInfo are the enrichment fields added to adsb/uat aircraft.
type AircraftInfo struct {
	Country      string   `json:"country,omitempty"`
	Registration string   `json:"registration,omitempty"`
	Type         string   `json:"type,omitempty"`
	Year         int      `json:"year,omitempty"`
	Airline      *Airline `json:"airline,omitempty"`
	FlightNumber string   `json:"flight_number,omitempty"`
}

// Aircraft looks up a Mode S hex address and (optionally) a callsign. Unknown
// parts are left empty.
func (d *DB) Aircraft(hex, callsign string) AircraftInfo {
	var info AircraftInfo
	if d == nil {
		return info
	}
	d.mu.RLock()
	blocks, faa, air := d.blocks, d.faa, d.airlines
	d.mu.RUnlock()
	if h, ok := ParseHex(hex); ok {
		info.Country = countryOf(blocks, h)
		if reg, ok := NNumberFromHex(h); ok {
			info.Registration = reg
		}
		if faa != nil {
			if reg, typ, year, ok := faa.lookup(h); ok {
				info.Registration, info.Type, info.Year = reg, typ, year
			}
		}
	}
	if a, num := airlineOf(air, callsign); a != nil {
		a.Country = "" // the snapshot carries icao, name and radio only
		info.Airline, info.FlightNumber = a, num
	}
	return info
}

// airlineOf resolves "UAL123" to the airline and flight number.
func airlineOf(air map[string]Airline, callsign string) (*Airline, string) {
	icao, num, ok := SplitCallsign(strings.ToUpper(strings.TrimSpace(callsign)))
	if !ok {
		return nil, ""
	}
	a, ok := air[icao]
	if !ok {
		return nil, ""
	}
	return &a, num
}

// CallsignInfo is the GET /api/meta/callsign/{callsign} payload.
type CallsignInfo struct {
	Callsign     string   `json:"callsign"`
	Registration string   `json:"registration,omitempty"`
	Airline      *Airline `json:"airline,omitempty"`
	FlightNumber string   `json:"flight_number,omitempty"`
}

// Callsign explains a callsign: an airline flight, or a registration.
func (d *DB) Callsign(cs string) CallsignInfo {
	cs = strings.ToUpper(strings.TrimSpace(cs))
	out := CallsignInfo{Callsign: cs}
	if IsRegistration(cs) {
		out.Registration = cs
		return out
	}
	if d == nil {
		return out
	}
	d.mu.RLock()
	air := d.airlines
	d.mu.RUnlock()
	out.Airline, out.FlightNumber = airlineOf(air, cs)
	return out
}

// Country returns the country of a Mode S hex address.
func (d *DB) Country(hex string) string {
	if d == nil {
		return ""
	}
	h, ok := ParseHex(hex)
	if !ok {
		return ""
	}
	d.mu.RLock()
	blocks := d.blocks
	d.mu.RUnlock()
	return countryOf(blocks, h)
}

func (d *DB) observer() (Observer, bool) {
	if d == nil || d.receiver == nil {
		return Observer{}, false
	}
	return d.receiver()
}
