package survey

import (
	"strings"
	"testing"

	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
)

func feed(name string, evs ...decoder.Event) *collector {
	c := newCollector(name)
	for _, e := range evs {
		e.Decoder = name
		c.add(e)
	}
	return c
}

func msg(d map[string]any) decoder.Event  { return decoder.Event{Kind: "message", Data: d} }
func snap(d map[string]any) decoder.Event { return decoder.Event{Kind: "snapshot", Data: d} }

func check(t *testing.T, c *collector, found int, summary string, samples int) []any {
	t.Helper()
	f, s, smp := c.result()
	if f != found || s != summary || len(smp) != samples {
		t.Fatalf("%s: found %d %q samples %d; want %d %q %d", c.name, f, s, len(smp), found, summary, samples)
	}
	return smp
}

func TestSummaryAircraft(t *testing.T) {
	ac := func(hex string, n float64) map[string]any { return map[string]any{"hex": hex, "messages": n} }
	c := feed("adsb",
		snap(map[string]any{"aircraft": []any{ac("a", 1), ac("b", 5)}}),
		snap(map[string]any{"aircraft": []map[string]any{ac("b", 9), ac("c", 2), ac("d", 7), ac("e", 1)}}),
		decoder.Event{Kind: "log", Data: map[string]string{"line": "x"}})
	smp := check(t, c, 5, "5 aircraft", 3)
	if smp[0].(map[string]any)["hex"] != "b" { // latest snapshot, most messages first
		t.Fatalf("samples %v", smp)
	}
	check(t, feed("adsb"), 0, "no aircraft", 0)
}

func TestSummaryUAT(t *testing.T) {
	c := feed("uat",
		snap(map[string]any{"aircraft": []any{map[string]any{"hex": "a"}}}),
		msg(map[string]any{"kind": "fisb", "report_type": "METAR", "location": "KXYZ", "time": "1", "text": "m"}),
		msg(map[string]any{"kind": "fisb", "report_type": "METAR", "location": "KXYZ", "time": "1", "text": "m"}),
		msg(map[string]any{"kind": "fisb", "report_type": "TAF", "location": "KXYZ", "time": "1", "text": "t"}))
	check(t, c, 1, "1 aircraft, 2 weather reports", 3)
}

func TestSummaryStations(t *testing.T) {
	for _, name := range []string{"aprs", "packet"} {
		c := feed(name, msg(map[string]any{"from": "N0CALL-9"}), msg(map[string]any{"from": "N0CALL-9"}), msg(map[string]any{"from": "W1AW"}))
		check(t, c, 2, "2 stations", 2)
	}
}

func TestSummaryOthers(t *testing.T) {
	check(t, feed("rtl_433",
		msg(map[string]any{"model": "A", "id": 1.0, "channel": 1.0}),
		msg(map[string]any{"model": "A", "id": 1.0, "channel": 2.0}),
		msg(map[string]any{"model": "A", "id": 1.0, "channel": 1.0})), 2, "2 sensors", 2)
	check(t, feed("ais", msg(map[string]any{"mmsi": 366123456.0}), msg(map[string]any{"mmsi": 366123456.0})), 1, "1 vessel", 1)
	check(t, feed("radiosonde", msg(map[string]any{"id": "S1"}), msg(map[string]any{"id": "S2"})), 2, "2 sondes", 2)
	check(t, feed("dsd",
		msg(map[string]any{"mode": "DMR", "tgt": 1.0, "src": 2.0}),
		msg(map[string]any{"mode": "DMR", "tgt": 1.0, "src": 2.0}),
		msg(map[string]any{"mode": "DMR", "tgt": 1.0, "src": 3.0})), 2, "2 calls", 2)
}

func TestSummaryMeters(t *testing.T) {
	c := feed("meters", snap(map[string]any{"own_ids": 0.0, "other_meters_heard": 23.0}))
	check(t, c, 0, "no own meters, 23 other meters heard", 0)
	c = feed("meters", snap(map[string]any{"other_meters_heard": 4.0}), msg(map[string]any{"model": "SCM", "id": "12345678"}))
	check(t, c, 1, "1 own meter, 4 other meters heard", 1)
}

func TestIgnoresOtherDecoders(t *testing.T) {
	c := newCollector("aprs")
	c.add(decoder.Event{Decoder: "ais", Kind: "message", Data: map[string]any{"from": "X"}})
	check(t, c, 0, "no stations", 0)
}

func TestScanResult(t *testing.T) {
	found, sum, peaks := scanResult([]dsp.Peak{{FreqHz: 146.52e6, DB: -41, SNR: 31}, {FreqHz: 145.1e6, DB: -60, SNR: 12}})
	if found != 2 || len(peaks) != 2 || !strings.HasPrefix(sum, "2 carriers, strongest 146.520 MHz (+31 dB)") {
		t.Fatalf("%d %q", found, sum)
	}
	if _, sum, _ := scanResult(nil); sum != "no carriers" {
		t.Fatal(sum)
	}
	if cs := carriers([]dsp.Peak{{FreqHz: 1}, {FreqHz: 2, Spur: true}}); len(cs) != 1 {
		t.Fatal("spur kept")
	}
}
