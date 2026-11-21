package meta

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	satellite "github.com/joshuaferrara/go-satellite"
)

const (
	// Tuner range of an RTL-SDR: downlinks outside it are ignored.
	MinTunerHz = 24_000_000
	MaxTunerHz = 1_766_000_000
	// DefaultToleranceHz leaves room for Doppler shift (up to ~±10 kHz at 437 MHz).
	DefaultToleranceHz = 15_000

	lightMS = 299_792_458.0
)

// transmitter is one alive SatNOGS downlink inside the tuner range.
type transmitter struct {
	norad         int
	desc          string
	lowHz, highHz int64
	mode          string
	baud          int
}

type tleEntry struct{ name, l1, l2 string }

// sat is a satellite with at least one downlink in range.
type sat struct {
	norad    int
	name     string
	rec      satellite.Satellite
	hasOrbit bool
	trs      []transmitter
}

type freqEntry struct {
	lowHz, highHz int64
	s             *sat
	t             *transmitter
}

// satIndex is immutable once built.
type satIndex struct {
	byFreq  []freqEntry // sorted by lowHz
	orbital []*sat      // satellites with an orbit and downlinks, sorted by NORAD id
}

type satnogsSat struct {
	Norad  int    `json:"norad_cat_id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type satnogsTx struct {
	Desc  string   `json:"description"`
	Alive bool     `json:"alive"`
	Low   *float64 `json:"downlink_low"` // SatNOGS sometimes writes numbers like 2048.0
	High  *float64 `json:"downlink_high"`
	Mode  string   `json:"mode"`
	Baud  *float64 `json:"baud"`
	Norad int      `json:"norad_cat_id"`
}

func (d *DB) loadSatnogs() {
	dir := filepath.Join(d.dir, "satnogs")
	tp := filepath.Join(dir, "transmitters.json")
	b, err := os.ReadFile(tp)
	if err != nil {
		d.setStatus("satnogs", false, 0, missing(err, tp))
		return
	}
	var txs []satnogsTx
	if err := json.Unmarshal(b, &txs); err != nil {
		d.setStatus("satnogs", false, 0, fmt.Errorf("transmitters.json: %w", err))
		return
	}
	names := map[int]string{}
	if sb, err := os.ReadFile(filepath.Join(dir, "satellites.json")); err == nil {
		var ss []satnogsSat
		if json.Unmarshal(sb, &ss) == nil {
			for _, s := range ss {
				if s.Norad > 0 && s.Name != "" {
					names[s.Norad] = s.Name
				}
			}
		}
	}
	var out []transmitter
	for _, t := range txs {
		if !t.Alive || t.Low == nil || t.Norad <= 0 || *t.Low < MinTunerHz || *t.Low > MaxTunerHz {
			continue
		}
		low := int64(*t.Low)
		tr := transmitter{norad: t.Norad, desc: t.Desc, lowHz: low, highHz: low, mode: t.Mode}
		if t.High != nil && int64(*t.High) > low && *t.High <= MaxTunerHz {
			tr.highHz = int64(*t.High)
		}
		if t.Baud != nil {
			tr.baud = int(*t.Baud)
		}
		out = append(out, tr)
	}
	d.mu.Lock()
	d.names, d.trs = names, out
	d.mu.Unlock()
	d.rebuildSats()
	d.setStatus("satnogs", true, len(out), nil)
}

func (d *DB) loadTLE() {
	tles := map[int]tleEntry{}
	found := false
	var firstErr error
	for _, g := range []string{"amateur", "weather"} {
		p := filepath.Join(d.dir, "tle", g+".txt")
		f, err := os.Open(p)
		if err != nil {
			if firstErr == nil {
				firstErr = missing(err, p)
			}
			continue
		}
		found = true
		for _, e := range parseTLE(bufio.NewScanner(f)) {
			n, _ := strconv.Atoi(strings.TrimSpace(e.l1[2:7]))
			if _, dup := tles[n]; !dup && n > 0 {
				tles[n] = e
			}
		}
		f.Close()
	}
	if !found {
		d.setStatus("tle", false, 0, firstErr)
		return
	}
	d.mu.Lock()
	d.tles = tles
	d.mu.Unlock()
	d.rebuildSats()
	d.setStatus("tle", true, len(tles), nil)
}

// parseTLE reads 3-line (name, line 1, line 2) or 2-line TLE sets.
func parseTLE(sc *bufio.Scanner) []tleEntry {
	var lines []string
	for sc.Scan() {
		if l := strings.TrimRight(sc.Text(), " \r\n"); l != "" {
			lines = append(lines, l)
		}
	}
	var out []tleEntry
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if len(l) < 69 || l[0] != '1' || l[1] != ' ' || i+1 >= len(lines) {
			continue
		}
		l2 := lines[i+1]
		if len(l2) < 69 || l2[0] != '2' || l2[1] != ' ' {
			continue
		}
		name := ""
		if i > 0 && !(len(lines[i-1]) > 1 && lines[i-1][0] == '2' && lines[i-1][1] == ' ') {
			name = strings.TrimSpace(strings.TrimPrefix(lines[i-1], "0 "))
		}
		out = append(out, tleEntry{name, l[:69], l2[:69]})
		i++
	}
	return out
}

// rebuildSats joins transmitters with TLEs (by NORAD id) into the index.
func (d *DB) rebuildSats() {
	d.mu.RLock()
	names, trs, tles := d.names, d.trs, d.tles
	d.mu.RUnlock()
	idx := buildSatIndex(names, trs, tles)
	d.mu.Lock()
	d.sats = idx
	d.mu.Unlock()
	d.passMu.Lock()
	d.passCache = map[passKey]passEntry{}
	d.passMu.Unlock()
}

func buildSatIndex(names map[int]string, trs []transmitter, tles map[int]tleEntry) *satIndex {
	by := map[int]*sat{}
	for _, t := range trs {
		s := by[t.norad]
		if s == nil {
			s = &sat{norad: t.norad, name: names[t.norad]}
			if e, ok := tles[t.norad]; ok {
				if s.name == "" {
					s.name = e.name
				}
				s.rec = satellite.TLEToSat(e.l1, e.l2, satellite.GravityWGS84)
				s.hasOrbit = s.rec.Error == 0
			}
			if s.name == "" {
				s.name = "NORAD " + strconv.Itoa(t.norad)
			}
			by[t.norad] = s
		}
		s.trs = append(s.trs, t)
	}
	idx := &satIndex{}
	for _, s := range by {
		for i := range s.trs {
			idx.byFreq = append(idx.byFreq, freqEntry{s.trs[i].lowHz, s.trs[i].highHz, s, &s.trs[i]})
		}
		if s.hasOrbit {
			idx.orbital = append(idx.orbital, s)
		}
	}
	sort.Slice(idx.byFreq, func(i, j int) bool {
		a, b := idx.byFreq[i], idx.byFreq[j]
		if a.lowHz != b.lowHz {
			return a.lowHz < b.lowHz
		}
		return a.s.norad < b.s.norad
	})
	sort.Slice(idx.orbital, func(i, j int) bool { return idx.orbital[i].norad < idx.orbital[j].norad })
	return idx
}

func (d *DB) satIndex() *satIndex {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sats
}

// ---- geometry ----

type look struct{ el, az, rng float64 } // degrees, degrees, km

func (o Observer) latlong() satellite.LatLong {
	return satellite.LatLong{Latitude: o.Lat * math.Pi / 180, Longitude: o.Lon * math.Pi / 180}
}

// look computes where s is seen from o at t (whole-second resolution).
func (s *sat) look(o Observer, t time.Time) (look, bool) {
	t = t.UTC()
	pos, _ := satellite.Propagate(s.rec, t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())
	if math.IsNaN(pos.X) || math.IsNaN(pos.Y) || math.IsNaN(pos.Z) || math.Abs(pos.X)+math.Abs(pos.Y)+math.Abs(pos.Z) < 6000 {
		return look{}, false
	}
	jd := satellite.JDay(t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())
	la := satellite.ECIToLookAngles(pos, o.latlong(), o.AltM/1000, jd)
	if math.IsNaN(la.El) {
		return look{}, false
	}
	return look{el: la.El * 180 / math.Pi, az: la.Az * 180 / math.Pi, rng: la.Rg}, true
}

// rangeRate is d(range)/dt in m/s (positive = receding), by finite difference over 1 s.
func (s *sat) rangeRate(o Observer, t time.Time) (float64, bool) {
	a, ok1 := s.look(o, t)
	b, ok2 := s.look(o, t.Add(time.Second))
	if !ok1 || !ok2 {
		return 0, false
	}
	return (b.rng - a.rng) * 1000, true
}

// dopplerHz is the shift of a downlink at freq given the range rate.
func dopplerHz(freqHz, rangeRate float64) float64 { return -freqHz * rangeRate / lightMS }

// ---- API types ----

// Downlink is one transmitter of a satellite.
type Downlink struct {
	FreqHz    int64   `json:"freq_hz"`
	Mode      string  `json:"mode,omitempty"`
	Baud      int     `json:"baud,omitempty"`
	Decoder   *string `json:"decoder"`
	DopplerHz *int    `json:"doppler_hz,omitempty"`
}

// Candidate is one /api/satellites/match entry.
type Candidate struct {
	Norad       int      `json:"norad"`
	Name        string   `json:"name"`
	DownlinkHz  int64    `json:"downlink_hz"`
	Mode        string   `json:"mode,omitempty"`
	Baud        int      `json:"baud,omitempty"`
	Transmitter string   `json:"transmitter,omitempty"`
	Visible     bool     `json:"visible"`
	ElevationD  *float64 `json:"elevation_deg,omitempty"`
	AzimuthD    *float64 `json:"azimuth_deg,omitempty"`
	RangeKm     *int     `json:"range_km,omitempty"`
	DopplerHz   *int     `json:"doppler_hz,omitempty"`

	dist float64
}

// MatchResult is the /api/satellites/match payload.
type MatchResult struct {
	FreqHz      int64       `json:"freq_hz"`
	ReceiverSet bool        `json:"receiver_set"`
	Candidates  []Candidate `json:"candidates"`
}

// OverheadSat is one /api/satellites/overhead entry.
type OverheadSat struct {
	Norad      int        `json:"norad"`
	Name       string     `json:"name"`
	Visible    bool       `json:"visible"`
	ElevationD float64    `json:"elevation_deg"`
	AzimuthD   float64    `json:"azimuth_deg"`
	RangeKm    int        `json:"range_km"`
	Downlinks  []Downlink `json:"downlinks"`
}

// Pass is one /api/satellites/passes entry.
type Pass struct {
	Norad      int        `json:"norad"`
	Name       string     `json:"name"`
	AOS        int64      `json:"aos"`
	LOS        int64      `json:"los"`
	MaxElevDeg float64    `json:"max_elevation_deg"`
	MaxTime    int64      `json:"max_time"`
	Downlinks  []Downlink `json:"downlinks"`
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }

// decoderFor says which sweep decoder handles a transmitter: packet for
// AFSK/GMSK/FSK AX.25 at 1200/9600 baud, nfm for FM voice, nil otherwise.
func decoderFor(mode string, baud int) *string {
	m := strings.ToUpper(mode)
	var d string
	switch {
	case (baud == 1200 || baud == 9600) && strings.Contains(m, "FSK") && !strings.Contains(m, "AX.100") && !strings.Contains(m, "QUBIK"),
		(baud == 1200 || baud == 9600) && m == "GMSK":
		d = "packet"
	case m == "FM" || m == "FMN":
		d = "nfm"
	default:
		return nil
	}
	return &d
}

func downlinks(s *sat, o *Observer, now time.Time) []Downlink {
	var rr float64
	var haveRR bool
	if o != nil {
		rr, haveRR = s.rangeRate(*o, now)
	}
	var out []Downlink
	for _, t := range s.trs {
		dl := Downlink{FreqHz: t.lowHz, Mode: t.mode, Baud: t.baud, Decoder: decoderFor(t.mode, t.baud)}
		if haveRR {
			v := int(math.Round(dopplerHz(float64(t.lowHz), rr)))
			dl.DopplerHz = &v
		}
		out = append(out, dl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FreqHz < out[j].FreqHz })
	return out
}

// ---- match / overhead ----

// Match lists satellites with a downlink within tol Hz of freq, best first:
// currently above the horizon, then by frequency distance.
func (d *DB) Match(freq, tol float64) MatchResult {
	res := MatchResult{FreqHz: int64(freq), Candidates: []Candidate{}}
	if d == nil {
		return res
	}
	o, haveObs := d.observer()
	res.ReceiverSet = haveObs
	res.Candidates = d.match(freq, tol, o, haveObs, d.now())
	return res
}

func (d *DB) match(freq, tol float64, o Observer, haveObs bool, now time.Time) []Candidate {
	idx := d.satIndex()
	if idx == nil {
		return []Candidate{}
	}
	out := []Candidate{}
	lo := int64(freq - tol)
	// highHz can exceed lowHz, so scan from the first entry that could reach lo;
	// transponder spans are small, so back up a little.
	start := sort.Search(len(idx.byFreq), func(i int) bool { return idx.byFreq[i].lowHz >= lo-2_000_000 })
	for i := start; i < len(idx.byFreq) && float64(idx.byFreq[i].lowHz) <= freq+tol; i++ {
		e := idx.byFreq[i]
		if float64(e.highHz) < freq-tol {
			continue
		}
		dist := 0.0
		switch {
		case freq < float64(e.lowHz):
			dist = float64(e.lowHz) - freq
		case freq > float64(e.highHz):
			dist = freq - float64(e.highHz)
		}
		c := Candidate{Norad: e.s.norad, Name: e.s.name, DownlinkHz: e.lowHz, Mode: e.t.mode, Baud: e.t.baud,
			Transmitter: e.t.desc, dist: dist}
		if haveObs && e.s.hasOrbit {
			if l, ok := e.s.look(o, now); ok {
				el, az, rg := r1(l.el), r1(l.az), int(math.Round(l.rng))
				c.ElevationD, c.AzimuthD, c.RangeKm = &el, &az, &rg
				c.Visible = l.el > 0
				if rr, ok := e.s.rangeRate(o, now); ok {
					f := math.Min(math.Max(freq, float64(e.lowHz)), float64(e.highHz))
					v := int(math.Round(dopplerHz(f, rr)))
					c.DopplerHz = &v
				}
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Visible != b.Visible {
			return a.Visible
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		return a.Norad < b.Norad
	})
	if len(out) > 25 {
		out = out[:25]
	}
	return out
}

// visibleMatch returns the best visible satellite with a downlink near hz
// (highest elevation), or nil. Needs the receiver location.
func (d *DB) visibleMatch(hz, tol float64) *Candidate {
	o, ok := d.observer()
	if !ok {
		return nil
	}
	var best *Candidate
	for _, c := range d.match(hz, tol, o, true, d.now()) {
		if !c.Visible {
			continue
		}
		if best == nil || (c.ElevationD != nil && best.ElevationD != nil && *c.ElevationD > *best.ElevationD) {
			c := c
			best = &c
		}
	}
	return best
}

// VisibleSatellite names the satellite above the horizon whose downlink is
// within ±15 kHz of freqHz, for tagging decoded packets.
func (d *DB) VisibleSatellite(freqHz float64) (norad int, name string, ok bool) {
	if d == nil {
		return 0, "", false
	}
	if c := d.visibleMatch(freqHz, DefaultToleranceHz); c != nil {
		return c.Norad, c.Name, true
	}
	return 0, "", false
}

// Overhead lists the satellites above the horizon now, highest first.
// ok is false when the receiver location is not set.
func (d *DB) Overhead() (sats []OverheadSat, ok bool) {
	sats = []OverheadSat{}
	if d == nil {
		return sats, false
	}
	o, ok := d.observer()
	if !ok {
		return sats, false
	}
	idx := d.satIndex()
	if idx == nil {
		return sats, true
	}
	now := d.now()
	for _, s := range idx.orbital {
		l, ok := s.look(o, now)
		if !ok || l.el <= 0 {
			continue
		}
		sats = append(sats, OverheadSat{Norad: s.norad, Name: s.name, Visible: true, ElevationD: r1(l.el), AzimuthD: r1(l.az),
			RangeKm: int(math.Round(l.rng)), Downlinks: downlinks(s, &o, now)})
	}
	sort.Slice(sats, func(i, j int) bool { return sats[i].ElevationD > sats[j].ElevationD })
	return sats, true
}

// ---- passes ----

type passKey struct {
	lat, lon int // 0.01 degrees
	hours    int
	minEl    int
}

type passEntry struct {
	at     time.Time
	passes []Pass
}

const passCacheTTL = 3 * time.Minute

// Passes lists upcoming passes within the next hours whose maximum elevation
// is at least minEl degrees, soonest first. ok is false when the receiver
// location is not set. Results are cached for a few minutes.
func (d *DB) Passes(hours int, minEl float64) (passes []Pass, ok bool) {
	passes = []Pass{}
	if d == nil {
		return passes, false
	}
	o, ok := d.observer()
	if !ok {
		return passes, false
	}
	now := d.now()
	key := passKey{int(math.Round(o.Lat * 100)), int(math.Round(o.Lon * 100)), hours, int(math.Round(minEl * 10))}
	d.passMu.Lock()
	if e, hit := d.passCache[key]; hit && now.Sub(e.at) < passCacheTTL {
		d.passMu.Unlock()
		for _, p := range e.passes {
			if p.LOS > now.Unix() {
				passes = append(passes, p)
			}
		}
		return passes, true
	}
	d.passMu.Unlock()

	idx := d.satIndex()
	if idx == nil {
		return passes, true
	}
	passes = computePasses(idx.orbital, o, now, hours, minEl)
	d.passMu.Lock()
	d.passCache[key] = passEntry{now, passes}
	d.passMu.Unlock()
	return passes, true
}

func computePasses(sats []*sat, o Observer, from time.Time, hours int, minEl float64) []Pass {
	var mu sync.Mutex
	var out []Pass
	jobs := make(chan *sat)
	var wg sync.WaitGroup
	for w := 0; w < max(1, runtime.NumCPU()); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				ps := satPasses(s, o, from, hours, minEl)
				mu.Lock()
				out = append(out, ps...)
				mu.Unlock()
			}
		}()
	}
	for _, s := range sats {
		jobs <- s
	}
	close(jobs)
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].AOS != out[j].AOS {
			return out[i].AOS < out[j].AOS
		}
		return out[i].Norad < out[j].Norad
	})
	if out == nil {
		out = []Pass{}
	}
	return out
}

// satPasses steps 30 s through the window and refines AOS, LOS and the
// culmination to one second.
func satPasses(s *sat, o Observer, from time.Time, hours int, minEl float64) []Pass {
	const step = 30 * time.Second
	el := func(t time.Time) float64 {
		l, ok := s.look(o, t)
		if !ok {
			return -90
		}
		return l.el
	}
	// bisect finds the second where elevation crosses 0 between a (below/above) and b.
	cross := func(a, b time.Time, aUp bool) time.Time {
		for b.Sub(a) > time.Second {
			m := a.Add(b.Sub(a) / 2)
			if (el(m) > 0) == aUp {
				a = m
			} else {
				b = m
			}
		}
		if aUp {
			return a
		}
		return b
	}
	peak := func(lo, hi time.Time) (time.Time, float64) {
		for hi.Sub(lo) > 2*time.Second {
			third := hi.Sub(lo) / 3
			m1, m2 := lo.Add(third), hi.Add(-third)
			if el(m1) < el(m2) {
				lo = m1
			} else {
				hi = m2
			}
		}
		bt, be := lo, el(lo)
		for t := lo.Add(time.Second); !t.After(hi); t = t.Add(time.Second) {
			if e := el(t); e > be {
				bt, be = t, e
			}
		}
		return bt, be
	}
	var out []Pass
	end := from.Add(time.Duration(hours) * time.Hour)
	emit := func(aos, los, maxT time.Time, maxE float64) {
		if !aos.After(from) && !los.Before(end) {
			return // above the horizon the whole window (geostationary): not a pass
		}
		lo, hi := maxT.Add(-step), maxT.Add(step)
		if lo.Before(aos) {
			lo = aos
		}
		if hi.After(los) {
			hi = los
		}
		if hi.After(lo) {
			if t, e := peak(lo, hi); e >= maxE {
				maxT, maxE = t, e
			}
		}
		if maxE < minEl {
			return
		}
		out = append(out, Pass{Norad: s.norad, Name: s.name, AOS: aos.Unix(), LOS: los.Unix(), MaxElevDeg: r1(maxE),
			MaxTime: maxT.Unix(), Downlinks: downlinks(s, nil, from)})
	}
	prevT := from
	prevEl := el(from)
	inPass := prevEl > 0
	aos, maxT, maxE := from, from, prevEl
	n := hours * 3600 / int(step.Seconds())
	for k := 1; k <= n; k++ {
		t := from.Add(time.Duration(k) * step)
		e := el(t)
		switch {
		case !inPass && e > 0:
			inPass, aos, maxT, maxE = true, cross(prevT, t, false), t, e
		case inPass && e > 0:
			if e > maxE {
				maxT, maxE = t, e
			}
		case inPass:
			emit(aos, cross(prevT, t, true), maxT, maxE)
			inPass = false
		}
		prevT = t
	}
	if inPass {
		emit(aos, prevT, maxT, maxE)
	}
	return out
}
