package meta

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitCallsign(t *testing.T) {
	for _, c := range []struct {
		in, icao, num string
		ok            bool
	}{
		{"UAL123", "UAL", "123", true}, {"DLH4AB", "DLH", "4AB", true}, {"BAW9", "BAW", "9", true},
		{"RYR1234", "RYR", "1234", true}, {"N123AB", "", "", false}, {"TEST", "", "", false},
		{"AB12", "", "", false}, {"", "", "", false}, {"UAL", "", "", false}, {"UAL12345", "", "", false},
		{"ual123", "", "", false}, // callers upper-case first
	} {
		icao, num, ok := SplitCallsign(c.in)
		if icao != c.icao || num != c.num || ok != c.ok {
			t.Errorf("%q: got %q %q %v", c.in, icao, num, ok)
		}
	}
}

func TestIsRegistration(t *testing.T) {
	for in, want := range map[string]bool{
		"N123AB": true, "N1": true, "N12345": true, "N1A": true, "N12AB": true, "N1234A": true,
		"N0123": false, "N123456": false, "N12IA": false, "N": false, "NJA123": false, "UAL123": false, "n123ab": false,
	} {
		if got := IsRegistration(in); got != want {
			t.Errorf("%q: got %v", in, got)
		}
	}
}

const airlinesFixture = `-1,"Unknown",\N,"-","N/A",\N,\N,"Y"
2,"135 Airways",\N,"","GNL","GENERAL","United States","N"
5,"Old United",\N,"UA","UAL","OLD UNITED","United States","N"
6,"United Airlines",\N,"UA","UAL","UNITED","United States","Y"
7,"Lufthansa",\N,"LH","DLH","LUFTHANSA","Germany","Y"
8,"Broken",\N,"XX","","","",""
9,"Another Old",\N,"UA","UAL","OLDER","United States","N"
`

// Fake FAA rows: the owner columns hold obvious placeholders that must never surface.
const faaMasterFixture = "\xef\xbb\xbfN-NUMBER,SERIAL NUMBER,MFR MDL CODE,ENG MFR MDL,YEAR MFR,TYPE REGISTRANT,NAME,STREET,STREET2,CITY,STATE,ZIP CODE,REGION,COUNTY,COUNTRY,LAST ACTION DATE,CERT ISSUE DATE,CERTIFICATION,TYPE AIRCRAFT,TYPE ENGINE,STATUS CODE,MODE S CODE,FRACT OWNER,AIR WORTH DATE,OTHER NAMES(1),OTHER NAMES(2),OTHER NAMES(3),OTHER NAMES(4),OTHER NAMES(5),EXPIRATION DATE,UNIQUE ID,KIT MFR, KIT MODEL,MODE S CODE HEX,\r\n" +
	"100  ,5334      ,7100510,17003,1940,1,SECRET OWNER NAME ,SECRET STREET ,                ,SECRET CITY,OK,743490329 ,2,097,US,20230122,20050506,1         ,4,1 ,25,50002263, ,19540430,                    ,                    ,                    ,                    ,                    ,20270430,00600060,                              ,                    ,A004B3    ,\r\n" +
	"123AB,1          ,2072738,     ,2015,7,SECRET, CO LLC,STREET,CITY,TN,372035521 ,3,037,US,20240823,20240823,          ,4,1 ,V ,50003445, ,        ,   ,   ,   ,   ,   ,20290430,00800080,                              ,                    ,A1B2C3    ,\r\n" +
	"55555,2          ,9999999,     ,    ,7,NO HEX,X,X,X,TN,372035521 ,3,037,US,20240823,20240823,          ,4,1 ,V ,50003445, ,        ,   ,   ,   ,   ,   ,20290430,00800080,                              ,                    ,           ,\r\n"

const acftrefFixture = "\xef\xbb\xbfCODE,MFR,MODEL,TYPE-ACFT,TYPE-ENG,AC-CAT,BUILD-CERT-IND,NO-ENG,NO-SEATS,AC-WEIGHT,SPEED,TC-DATA-SHEET,TC-DATA-HOLDER,\r\n" +
	"7100510,CESSNA                   ,172             ,4,1 ,1,0,01,004,CLASS 1,0000,               ,                                                  ,\r\n" +
	"2072738,BOEING                   ,737-800         ,5,3 ,1,0,02,189,CLASS 3,0000,               ,                                                  ,\r\n"

func testDB(t *testing.T) *DB {
	t.Helper()
	d := New("", nil)
	air, err := parseAirlines(strings.NewReader(airlinesFixture))
	if err != nil {
		t.Fatal(err)
	}
	faa, err := parseMaster(strings.NewReader(faaMasterFixture), parseACFTREF(strings.NewReader(acftrefFixture)))
	if err != nil {
		t.Fatal(err)
	}
	d.airlines, d.faa = air, faa
	return d
}

func TestParseAirlines(t *testing.T) {
	air, _ := parseAirlines(strings.NewReader(airlinesFixture))
	if len(air) != 3 {
		t.Fatalf("%d airlines: %v", len(air), air)
	}
	if a := air["UAL"]; a.Name != "United Airlines" || a.Radio != "UNITED" || a.Country != "United States" {
		t.Errorf("active airline should win: %+v", a)
	}
	if _, ok := air["N/A"]; ok {
		t.Error("N/A kept")
	}
}

func TestParseFAA(t *testing.T) {
	d := testDB(t)
	if len(d.faa.recs) != 2 {
		t.Fatalf("%d records, want 2 (the row without hex is dropped)", len(d.faa.recs))
	}
	reg, typ, year, ok := d.faa.lookup(0xA004B3)
	if !ok || reg != "N100" || typ != "CESSNA 172" || year != 1940 {
		t.Errorf("%q %q %d %v", reg, typ, year, ok)
	}
	reg, typ, year, _ = d.faa.lookup(0xA1B2C3)
	if reg != "N123AB" || typ != "BOEING 737-800" || year != 2015 {
		t.Errorf("%q %q %d", reg, typ, year)
	}
	// owner data is never kept: nothing but the interned type table holds strings
	for _, s := range d.faa.types {
		if strings.Contains(s, "SECRET") {
			t.Errorf("owner text leaked into %q", s)
		}
	}
	if _, _, _, ok := d.faa.lookup(0xA00000); ok {
		t.Error("unknown hex found")
	}
}

func TestAircraftEnrichment(t *testing.T) {
	d := testDB(t)
	got := d.Aircraft("a1b2c3", "UAL123 ")
	if got.Country != "United States" || got.Registration != "N123AB" || got.Type != "BOEING 737-800" || got.Year != 2015 {
		t.Errorf("%+v", got)
	}
	if got.Airline == nil || got.Airline.ICAO != "UAL" || got.Airline.Name != "United Airlines" || got.FlightNumber != "123" {
		t.Errorf("%+v %+v", got, got.Airline)
	}
	// not in the FAA file: the registration still follows from the hex
	if g := d.Aircraft("a00001", ""); g.Registration != "N1" || g.Type != "" || g.Airline != nil {
		t.Errorf("%+v", g)
	}
	// foreign aircraft: country only, and an unknown airline is not invented
	if g := d.Aircraft("3c4dd2", "XYZ99"); g.Country != "Germany" || g.Registration != "" || g.Airline != nil {
		t.Errorf("%+v", g)
	}
	if g := d.Aircraft("3c4dd2", "DLH4AB"); g.Airline == nil || g.Airline.Name != "Lufthansa" || g.FlightNumber != "4AB" {
		t.Errorf("%+v", g)
	}
	if g := d.Aircraft("nothex", "N123AB"); g != (AircraftInfo{}) {
		t.Errorf("%+v", g)
	}
}

func TestCallsign(t *testing.T) {
	d := testDB(t)
	if c := d.Callsign("ual123"); c.Callsign != "UAL123" || c.Airline == nil || c.Airline.Radio != "UNITED" || c.FlightNumber != "123" || c.Registration != "" {
		t.Errorf("%+v", c)
	}
	if c := d.Callsign("N123AB"); c.Registration != "N123AB" || c.Airline != nil {
		t.Errorf("%+v", c)
	}
	if c := d.Callsign("TEST"); c.Airline != nil || c.Registration != "" {
		t.Errorf("%+v", c)
	}
}

func TestNilDB(t *testing.T) {
	var d *DB
	d.Start()
	if d.Aircraft("a1b2c3", "UAL1") != (AircraftInfo{}) || d.Label(162.4e6) != "" || d.Country("a1b2c3") != "" {
		t.Error("nil DB returned data")
	}
	if _, _, ok := d.VisibleSatellite(145.8e6); ok {
		t.Error("nil DB satellite")
	}
	if p, ok := d.Passes(24, 10); ok || p == nil {
		t.Error("nil DB passes")
	}
	if m := d.Match(145.8e6, 15000); m.Candidates == nil || len(m.Candidates) != 0 {
		t.Error("nil DB match")
	}
	_ = d.Status()
}

func TestLabelChannel(t *testing.T) {
	d := New("", nil)
	if got := d.Label(162.4e6 + 3000); got != "NOAA Weather Radio: WX2" {
		t.Errorf("got %q", got)
	}
	if got := d.Label(162.4e6 + 7000); got != "" {
		t.Errorf("outside 5 kHz: %q", got)
	}
	if got := d.Label(100e6); got != "" {
		t.Errorf("%q", got)
	}
}

func TestStatus(t *testing.T) {
	d := New(t.TempDir(), nil)
	d.Load()
	st := d.Status()
	byName := map[string]DatasetStatus{}
	for _, s := range st.Datasets {
		byName[s.Name] = s
	}
	if f := byName["faa"]; f.Loaded || !strings.Contains(f.Error, "make metadata") {
		t.Errorf("%+v", f)
	}
	if c := byName["icao_countries"]; !c.Loaded || c.Records < 60 {
		t.Errorf("%+v", c)
	}
}

// ---- satellites ----

// The classic SGP4 verification TLE (ISS, 2008), observed from a made-up location.
const (
	issL1 = "1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927"
	issL2 = "2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537"
)

var testEpoch = time.Date(2008, 9, 20, 12, 25, 40, 0, time.UTC)

func satTestDB(t *testing.T) *DB {
	t.Helper()
	d := New("", func() (Observer, bool) { return Observer{Lat: 12.34, Lon: 56.78, AltM: 10}, true })
	d.now = func() time.Time { return testEpoch }
	d.names = map[int]string{25544: "ISS"}
	d.trs = []transmitter{
		{norad: 25544, desc: "APRS digipeater", lowHz: 145_825_000, highHz: 145_825_000, mode: "AFSK", baud: 1200},
		{norad: 25544, desc: "Voice", lowHz: 437_800_000, highHz: 437_800_000, mode: "FM"},
		{norad: 99999, desc: "No orbit", lowHz: 436_500_000, highHz: 436_500_000, mode: "BPSK", baud: 1200},
	}
	d.tles = map[int]tleEntry{25544: {"ISS (ZARYA)", issL1, issL2}}
	d.rebuildSats()
	return d
}

func TestParseTLE(t *testing.T) {
	in := "ISS (ZARYA)\n" + issL1 + "\n" + issL2 + "\r\n0 NOAA 19\n" + issL1 + "\n" + issL2 + "\n" + issL1 + "\n" + issL2 + "\n"
	es := parseTLE(bufio.NewScanner(strings.NewReader(in)))
	if len(es) != 3 || es[0].name != "ISS (ZARYA)" || es[1].name != "NOAA 19" || es[2].name != "" {
		t.Fatalf("%+v", es)
	}
}

func TestPasses(t *testing.T) {
	d := satTestDB(t)
	ps, ok := d.Passes(24, 0)
	if !ok || len(ps) == 0 {
		t.Fatalf("no passes in 24 h: %v %v", ok, ps)
	}
	if len(ps) > 12 {
		t.Errorf("%d passes in 24 h for one LEO satellite", len(ps))
	}
	prev := int64(0)
	for _, p := range ps {
		if p.AOS >= p.LOS {
			t.Errorf("AOS %d not before LOS %d", p.AOS, p.LOS)
		}
		if p.MaxElevDeg < 0 || p.MaxElevDeg > 90 {
			t.Errorf("max elevation %v", p.MaxElevDeg)
		}
		if p.MaxTime < p.AOS || p.MaxTime > p.LOS {
			t.Errorf("culmination %d outside %d..%d", p.MaxTime, p.AOS, p.LOS)
		}
		if p.LOS-p.AOS > 20*60 {
			t.Errorf("LEO pass of %d s", p.LOS-p.AOS)
		}
		if p.AOS < prev {
			t.Error("passes not sorted")
		}
		prev = p.AOS
		if len(p.Downlinks) != 2 || p.Downlinks[0].Decoder == nil || *p.Downlinks[0].Decoder != "packet" ||
			p.Downlinks[1].Decoder == nil || *p.Downlinks[1].Decoder != "nfm" {
			t.Errorf("downlinks %+v", p.Downlinks)
		}
	}
	hi, ok := d.Passes(24, 20)
	if !ok {
		t.Fatal("no receiver")
	}
	for _, p := range hi {
		if p.MaxElevDeg < 20 {
			t.Errorf("pass below min elevation: %v", p.MaxElevDeg)
		}
	}
	// a second call within the TTL comes from the cache and drops finished passes
	d.now = func() time.Time { return testEpoch.Add(time.Minute) }
	again, _ := d.Passes(24, 0)
	if len(again) == 0 || len(again) > len(ps) {
		t.Errorf("cached: %d vs %d", len(again), len(ps))
	}
}

func TestPassesNeedReceiver(t *testing.T) {
	d := satTestDB(t)
	d.receiver = nil
	if ps, ok := d.Passes(24, 10); ok || len(ps) != 0 {
		t.Error("passes without receiver")
	}
	if m := d.Match(145.825e6, 15000); m.ReceiverSet || len(m.Candidates) != 1 || m.Candidates[0].Visible || m.Candidates[0].ElevationD != nil {
		t.Errorf("%+v", m)
	}
}

func TestMatchOverheadAndDoppler(t *testing.T) {
	d := satTestDB(t)
	ps, _ := d.Passes(24, 5)
	if len(ps) == 0 {
		t.Skip("no pass above 5 degrees in the test window")
	}
	p := ps[0]

	// at culmination the satellite is visible and matches
	d.now = func() time.Time { return time.Unix(p.MaxTime, 0) }
	m := d.Match(145.8e6, 15000) // 25 kHz from nominal: out of tolerance
	if len(m.Candidates) != 0 {
		t.Errorf("tolerance ignored: %+v", m.Candidates)
	}
	m = d.Match(145.83e6, 15000)
	if !m.ReceiverSet || len(m.Candidates) != 1 {
		t.Fatalf("%+v", m)
	}
	c := m.Candidates[0]
	if !c.Visible || c.Norad != 25544 || c.Name != "ISS" || c.ElevationD == nil || *c.ElevationD < p.MaxElevDeg-1 || c.DopplerHz == nil {
		t.Errorf("%+v", c)
	}
	if got := d.Label(145.825e6); got != "ISS downlink" {
		t.Errorf("label %q", got)
	}
	if n, name, ok := d.VisibleSatellite(145.825e6); !ok || n != 25544 || name != "ISS" {
		t.Errorf("%d %q %v", n, name, ok)
	}
	over, ok := d.Overhead()
	if !ok || len(over) != 1 || over[0].Norad != 25544 || len(over[0].Downlinks) != 2 || over[0].Downlinks[0].DopplerHz == nil {
		t.Errorf("%+v", over)
	}

	// Doppler: positive while approaching (just after AOS), negative while receding
	d.now = func() time.Time { return time.Unix(p.AOS+10, 0) }
	near := d.Match(145.825e6, 1000).Candidates[0]
	d.now = func() time.Time { return time.Unix(p.LOS-10, 0) }
	far := d.Match(145.825e6, 1000).Candidates[0]
	if *near.DopplerHz <= 0 || *far.DopplerHz >= 0 {
		t.Errorf("doppler near AOS %d, near LOS %d", *near.DopplerHz, *far.DopplerHz)
	}
	if v := *near.DopplerHz; v > 4000 {
		t.Errorf("implausible doppler %d Hz at 145.8 MHz", v)
	}

	// below the horizon: not visible, no label, not overhead
	d.now = func() time.Time { return time.Unix(p.LOS+300, 0) }
	if c := d.Match(145.825e6, 1000).Candidates[0]; c.Visible {
		t.Errorf("visible after LOS: %+v", c)
	}
	if got := d.Label(145.825e6); strings.Contains(got, "downlink") || !strings.Contains(got, "ISS APRS digipeater") {
		t.Errorf("below the horizon only the catalog names the channel: %q", got)
	}
	if over, _ := d.Overhead(); len(over) != 0 {
		t.Errorf("%+v", over)
	}
}

func TestMatchWithoutOrbit(t *testing.T) {
	d := satTestDB(t)
	m := d.Match(436.5e6, 15000)
	if len(m.Candidates) != 1 || m.Candidates[0].Norad != 99999 || m.Candidates[0].Visible || m.Candidates[0].Name != "NORAD 99999" {
		t.Errorf("%+v", m)
	}
}

func TestDecoderFor(t *testing.T) {
	for _, c := range []struct {
		mode string
		baud int
		want string
	}{
		{"AFSK", 1200, "packet"}, {"FSK AX.25 G3RUH", 9600, "packet"}, {"GMSK", 9600, "packet"}, {"FM", 0, "nfm"}, {"FMN", 0, "nfm"},
		{"BPSK", 1200, ""}, {"CW", 0, ""}, {"FSK AX.100 Mode 5", 9600, ""}, {"AFSK", 0, ""}, {"APT", 0, ""},
	} {
		got := decoderFor(c.mode, c.baud)
		if (got == nil) != (c.want == "") || (got != nil && *got != c.want) {
			t.Errorf("%s %d: %v", c.mode, c.baud, got)
		}
	}
}

func TestLoadSatnogsFloatsAndFilters(t *testing.T) {
	dir := t.TempDir()
	must := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("satnogs/transmitters.json", `[
	 {"description":"ok","alive":true,"downlink_low":145825000,"downlink_high":null,"mode":"AFSK","baud":1200.0,"norad_cat_id":25544},
	 {"description":"floaty","alive":true,"downlink_low":437800000.0,"downlink_high":437810000,"mode":"FM","baud":null,"norad_cat_id":25544},
	 {"description":"dead","alive":false,"downlink_low":145900000,"mode":"FM","norad_cat_id":25544},
	 {"description":"too high","alive":true,"downlink_low":2216000000,"mode":"BPSK","norad_cat_id":25544},
	 {"description":"no downlink","alive":true,"downlink_low":null,"mode":"BPSK","norad_cat_id":25544}]`)
	must("satnogs/satellites.json", `[{"norad_cat_id":25544,"name":"ISS"}]`)
	must("tle/amateur.txt", "ISS (ZARYA)\n"+issL1+"\n"+issL2+"\n")
	d := New(dir, nil)
	d.Load()
	st := d.Status()
	for _, s := range st.Datasets {
		if (s.Name == "satnogs" && (!s.Loaded || s.Records != 2)) || (s.Name == "tle" && (!s.Loaded || s.Records != 1)) {
			t.Errorf("%+v", s)
		}
	}
	m := d.Match(437.805e6, 10000)
	if len(m.Candidates) != 1 || m.Candidates[0].DownlinkHz != 437_800_000 {
		t.Errorf("transponder span: %+v", m)
	}
}
