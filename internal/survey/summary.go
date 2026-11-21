package survey

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
)

// thing is one distinct item heard (aircraft, sensor, station, ...), with its latest data.
type thing struct {
	data  map[string]any
	seq   int
	score float64
}

// collector turns one decoder's events into found / summary / samples.
type collector struct {
	name   string
	things map[string]*thing
	fisb   map[string]*thing // uat weather reports
	other  int               // meters: other meters heard
	seq    int
}

func newCollector(name string) *collector {
	return &collector{name: name, things: map[string]*thing{}, fisb: map[string]*thing{}}
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	var m map[string]any
	if b, err := json.Marshal(v); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// str renders a JSON scalar for use in a key.
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func key(m map[string]any, fields ...string) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = str(m[f])
	}
	return strings.Join(parts, "|")
}

func (c *collector) put(into map[string]*thing, k string, m map[string]any, score float64) {
	into[k] = &thing{data: m, seq: c.seq, score: score}
}

// add records one event; events of other decoders and log events are ignored.
func (c *collector) add(ev decoder.Event) {
	if ev.Decoder != c.name || ev.Kind == "log" {
		return
	}
	c.seq++
	m := asMap(ev.Data)
	if m == nil {
		return
	}
	switch c.name {
	case "adsb", "uat":
		if ev.Kind == "snapshot" {
			var ac []map[string]any
			if b, err := json.Marshal(m["aircraft"]); err == nil {
				_ = json.Unmarshal(b, &ac)
			}
			for _, a := range ac {
				if hex := str(a["hex"]); hex != "" {
					n, _ := a["messages"].(float64)
					c.put(c.things, hex, a, n)
				}
			}
		} else if str(m["kind"]) == "fisb" {
			c.put(c.fisb, key(m, "report_type", "location", "time", "text"), m, 0)
		}
	case "rtl_433":
		c.put(c.things, key(m, "model", "id", "channel"), m, 0)
	case "aprs", "packet":
		c.put(c.things, key(m, "from"), m, 0)
	case "ais":
		c.put(c.things, key(m, "mmsi"), m, 0)
	case "radiosonde":
		c.put(c.things, key(m, "id"), m, 0)
	case "dsd":
		c.put(c.things, key(m, "mode", "tgt", "src"), m, 0)
	case "meters":
		if ev.Kind == "snapshot" {
			if n, ok := m["other_meters_heard"].(float64); ok {
				c.other = int(n)
			}
		} else {
			c.put(c.things, key(m, "model", "id", "ERTSerialNumber", "EndpointID", "meter_id"), m, 0)
		}
	}
}

// top returns up to three latest items (ties: higher score first).
func top(things map[string]*thing) []any {
	all := make([]*thing, 0, len(things))
	for _, t := range things {
		all = append(all, t)
	}
	slices.SortFunc(all, func(a, b *thing) int {
		if a.seq != b.seq {
			return b.seq - a.seq
		}
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return 0
	})
	var out []any
	for _, t := range all[:min(3, len(all))] {
		out = append(out, t.data)
	}
	return out
}

// count formats "3 stations", "1 station" or "no stations".
func count(n int, one, many string) string {
	switch n {
	case 0:
		return "no " + many
	case 1:
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// result reports the number of things found, a summary line and up to 3 samples.
func (c *collector) result() (found int, summary string, samples []any) {
	found = len(c.things)
	samples = top(c.things)
	switch c.name {
	case "adsb":
		summary = count(found, "aircraft", "aircraft")
	case "uat":
		summary = count(found, "aircraft", "aircraft")
		if n := len(c.fisb); n > 0 {
			summary += ", " + count(n, "weather report", "weather reports")
			if len(samples) < 3 {
				samples = append(samples, top(c.fisb)[:min(3-len(samples), n)]...)
			}
		}
	case "rtl_433":
		summary = count(found, "sensor", "sensors")
	case "aprs", "packet":
		summary = count(found, "station", "stations")
	case "ais":
		summary = count(found, "vessel", "vessels")
	case "radiosonde":
		summary = count(found, "sonde", "sondes")
	case "dsd":
		summary = count(found, "call", "calls")
	case "meters":
		summary = count(found, "own meter", "own meters")
		if c.other > 0 {
			summary += fmt.Sprintf(", %d other meters heard", c.other)
		}
	}
	return
}

// scanResult summarises a band scan: found carriers and the one-line summary.
func scanResult(peaks []dsp.Peak) (found int, summary string, out []StepPeak) {
	out = []StepPeak{}
	for _, p := range peaks {
		out = append(out, StepPeak{FreqHz: p.FreqHz, DB: p.DB, SNR: p.SNR, Label: p.Label})
	}
	found = len(out)
	if found == 0 {
		return 0, "no carriers", out
	}
	best := out[0]
	for _, p := range out {
		if p.SNR > best.SNR {
			best = p
		}
	}
	return found, fmt.Sprintf("%s, strongest %.3f MHz (%+.0f dB)", count(found, "carrier", "carriers"), best.FreqHz/1e6, best.SNR), out
}
