// Package survey runs the "what's on the air?" survey: a list of short scan
// and decoder steps with the current antenna, then restores the radio.
package survey

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/wardviaene/sweep/data"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/scan"
)

// Band is a catalog band (the fields the survey needs).
type Band struct {
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Regions  []string             `json:"regions"`
	LoMHz    float64              `json:"lo_mhz"`
	HiMHz    float64              `json:"hi_mhz"`
	Edges    map[string][]float64 `json:"edges"`
	TuneMHz  float64              `json:"tune_mhz"`
	HF       bool                 `json:"hf"`
	Decoder  *string              `json:"decoder"`
	Mode     string               `json:"mode"` // nfm | am | usb | lsb | none
	Channels []struct {
		MHz float64 `json:"mhz"`
	} `json:"channels"`
}

// listenMode is the demod mode used to listen to a band ("" = not a voice band).
func (b Band) listenMode() string {
	switch b.Mode {
	case "nfm", "am", "usb", "lsb":
		return b.Mode
	}
	return ""
}

// listenGrid is the frequency rounding (Hz) when listening to a carrier of the band:
// 100 Hz for SSB, 5 kHz for FM/AM channels (6.25 kHz when the catalog channels sit on
// that raster instead), 1 kHz below 30 MHz.
func (b Band) listenGrid() float64 {
	switch b.Mode {
	case "usb", "lsb":
		return 100
	}
	if b.LoMHz < 30 {
		return 1000
	}
	on := func(hz, grid float64) bool { r := math.Mod(hz, grid); return r < 1 || grid-r < 1 }
	all5, all625, any := true, true, false
	for _, c := range b.Channels {
		hz := c.MHz * 1e6
		any = true
		all5 = all5 && on(hz, 5000)
		all625 = all625 && on(hz, 6250)
	}
	switch {
	case any && !all5 && all625:
		return 6250
	case any && !all5:
		return 100
	}
	return 5000
}

// Antenna lists the MHz ranges where an antenna works well (good) or so-so (fair).
type Antenna struct {
	ID   string       `json:"id"`
	Name string       `json:"name"`
	Good [][2]float64 `json:"good"`
	Fair [][2]float64 `json:"fair"`
}

// Fit rates a frequency: "good", "fair" or "poor".
func (a Antenna) Fit(mhz float64) string {
	in := func(rs [][2]float64) bool {
		for _, r := range rs {
			if mhz >= r[0] && mhz <= r[1] {
				return true
			}
		}
		return false
	}
	switch {
	case in(a.Good):
		return "good"
	case in(a.Fair):
		return "fair"
	}
	return "poor"
}

// Catalog is the parsed data/bands.json.
type Catalog struct {
	DefaultRegion string            `json:"default_region"`
	Regions       map[string]string `json:"regions"`
	Bands         []Band            `json:"bands"`
	Antennas      []Antenna         `json:"antennas"`
}

// LoadCatalog parses the embedded catalog once.
var LoadCatalog = sync.OnceValue(func() *Catalog {
	var c Catalog
	if err := json.Unmarshal(data.Bands, &c); err != nil {
		panic("bands.json: " + err.Error())
	}
	return &c
})

func (c *Catalog) antenna(id string) (Antenna, bool) {
	for _, a := range c.Antennas {
		if a.ID == id {
			return a, true
		}
	}
	return Antenna{}, false
}

func (c *Catalog) antennaIDs() string {
	var ids []string
	for _, a := range c.Antennas {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ", ")
}

// ValidAntenna returns an error if id is not an antenna in the catalog.
func ValidAntenna(id string) error {
	c := LoadCatalog()
	if _, ok := c.antenna(id); !ok {
		return fmt.Errorf("unknown antenna %q (want one of %s)", id, c.antennaIDs())
	}
	return nil
}

// Step is one survey step as reported by GET /api/survey.
type Step struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"` // scan | decoder
	Decoder  string     `json:"decoder,omitempty"`
	Band     string     `json:"band,omitempty"`
	Label    string     `json:"label"`
	FreqLoHz uint32     `json:"freq_lo_hz,omitempty"`
	FreqHiHz uint32     `json:"freq_hi_hz,omitempty"`
	FreqHz   uint32     `json:"freq_hz,omitempty"`
	State    string     `json:"state"` // pending | running | done | skipped | error
	Seconds  float64    `json:"seconds,omitempty"`
	Found    *int       `json:"found,omitempty"`
	Summary  string     `json:"summary,omitempty"`
	Peaks    []StepPeak `json:"peaks,omitempty"`
	Samples  []any      `json:"samples,omitempty"`
	Error    string     `json:"error,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	Listened *Listened  `json:"listened,omitempty"`

	fit    string // antenna fit of the band's tune frequency
	secs   int    // decoder: how long to listen
	opt    decoder.Options
	mode   string  // scan: demod mode used to listen to the strongest carrier ("" = none)
	grid   float64 // scan: listen frequency rounding in Hz
	tuneHz uint32  // decoder: center to tune to before starting (rtl_433 picks its ISM band from it)
}

// Listened is what a scan step played after its scan (listen hold).
type Listened struct {
	FreqHz  uint32  `json:"freq_hz"`
	Mode    string  `json:"mode"`
	Seconds float64 `json:"seconds"`
	LevelDB float64 `json:"level_db"` // mean demod channel level over the hold
	Label   string  `json:"label,omitempty"`
}

// Listening is set in State while a listen hold is active.
type Listening struct {
	Step    string  `json:"step"`
	FreqHz  uint32  `json:"freq_hz"`
	Mode    string  `json:"mode"`
	Label   string  `json:"label,omitempty"`
	Seconds int     `json:"seconds"` // planned hold
	Started float64 `json:"started"` // unix seconds
}

// StepPeak is a carrier found by a scan step.
type StepPeak struct {
	FreqHz float64 `json:"freq_hz"`
	DB     float64 `json:"db"`
	SNR    float64 `json:"snr_db"`
	Label  string  `json:"label,omitempty"` // known channel or visible satellite (docs/metadata.md)
}

// carriers drops the RTL2832 crystal spurs.
func carriers(ps []dsp.Peak) []dsp.Peak {
	var out []dsp.Peak
	for _, p := range ps {
		if !p.Spur {
			out = append(out, p)
		}
	}
	return out
}

var decoderLabels = map[string]string{
	"rtl_433": "rtl_433 sensors", "adsb": "ADS-B", "aprs": "APRS", "ais": "AIS", "uat": "UAT",
	"radiosonde": "Radiosonde", "dsd": "Digital voice (DSD)", "packet": "Packet", "meters": "Utility meters",
}

// decoderFreq is the frequency a decoder works on for a band (0 = found at run time).
func decoderFreq(name, region string, b Band) uint32 {
	switch name {
	case "aprs":
		if region == "eu" {
			return 144_800_000
		}
		return 144_390_000
	case "ais":
		return 161_975_000
	case "adsb":
		return 1_090_000_000
	case "uat":
		return 978_000_000
	case "meters":
		return 914_000_000
	case "rtl_433":
		return mhz(b.TuneMHz)
	}
	return 0
}

func mhz(v float64) uint32 { return uint32(math.Round(v * 1e6)) }

func decoderLabel(name string, freq uint32, b Band) string {
	l := decoderLabels[name]
	switch {
	case name == "radiosonde":
		return fmt.Sprintf("%s (%g-%g MHz)", l, b.LoMHz, b.HiMHz)
	case freq > 0:
		return fmt.Sprintf("%s (%.3f MHz)", l, float64(freq)/1e6)
	}
	return l
}

// universe lists every step of a region in catalog order (scan, then decoder,
// per band), leaving out HF bands unless the antenna is the HF wire.
func universe(c *Catalog, ant Antenna, region string, seconds int) []Step {
	uses := map[string]int{}
	for _, b := range c.Bands {
		if b.Decoder != nil && inRegion(b, region) {
			uses[*b.Decoder]++
		}
	}
	var out []Step
	for _, b := range c.Bands {
		if !inRegion(b, region) || b.HF && ant.ID != "hfwire" {
			continue
		}
		fit := ant.Fit(b.TuneMHz)
		lo, hi := b.LoMHz, b.HiMHz
		if e := b.Edges[region]; len(e) == 2 {
			lo, hi = max(lo, e[0]), min(hi, e[1])
		}
		out = append(out, Step{ID: "scan:" + b.ID, Kind: "scan", Band: b.ID, Label: b.Name + ": band scan",
			FreqLoHz: mhz(lo), FreqHiHz: mhz(hi), State: "pending", fit: fit,
			mode: b.listenMode(), grid: b.listenGrid()})
		if b.Decoder == nil {
			continue
		}
		d := *b.Decoder
		id := d
		if uses[d] > 1 {
			id += "@" + b.ID
		}
		f := decoderFreq(d, region, b)
		s := Step{ID: id, Kind: "decoder", Decoder: d, Band: b.ID, Label: decoderLabel(d, f, b), FreqHz: f,
			State: "pending", fit: fit, secs: seconds, opt: decoder.Options{Region: region, FreqHz: f}}
		switch d {
		case "rtl_433":
			s.tuneHz = f
		case "packet":
			s.State, s.Reason = "skipped", "needs a satellite pass; start it manually with a frequency"
		case "radiosonde":
			if b.ID == "sonde1680" {
				s.State, s.Reason = "skipped", "the 400 MHz radiosonde step also searches 1676-1682 MHz"
			}
		}
		out = append(out, s)
	}
	return out
}

func inRegion(b Band, region string) bool {
	for _, r := range b.Regions {
		if r == region {
			return true
		}
	}
	return false
}

// buildPlan returns the steps of a survey: the explicit ids if given, else
// every band that fits the antenna (good, or fair with includeFair).
func buildPlan(c *Catalog, antenna, region string, seconds int, includeFair bool, explicit []string) ([]Step, error) {
	ant, ok := c.antenna(antenna)
	if !ok {
		return nil, fmt.Errorf("unknown antenna %q (want one of %s)", antenna, c.antennaIDs())
	}
	if _, ok := c.Regions[region]; !ok {
		var rs []string
		for r := range c.Regions {
			rs = append(rs, r)
		}
		sort.Strings(rs)
		return nil, fmt.Errorf("unknown region %q (want one of %s)", region, strings.Join(rs, ", "))
	}
	all := universe(c, ant, region, seconds)
	var out []Step
	if len(explicit) > 0 {
		byID := map[string]Step{}
		var ids []string
		for _, s := range all {
			byID[s.ID] = s
			ids = append(ids, s.ID)
		}
		seen := map[string]bool{}
		for _, id := range explicit {
			s, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("unknown step %q for region %s, antenna %s (valid: %s)", id, region, antenna, strings.Join(ids, ", "))
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, s)
			}
		}
		return out, nil
	}
	for _, s := range all {
		if s.fit == "good" || includeFair && s.fit == "fair" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no band fits antenna %s in region %s (try include_fair, another antenna, or explicit steps)", antenna, region)
	}
	return out, nil
}

// estimate is the expected duration of the plan in seconds.
// listenS is the hold per voice-band scan step (0 = not listening); every such
// step is assumed to find a carrier.
func estimate(steps []Step, rate uint32, listenS int) int {
	var t float64
	for _, s := range steps {
		switch {
		case s.State == "skipped":
		case s.Kind == "scan":
			n := math.Ceil(float64(s.FreqHiHz-s.FreqLoHz) / float64(scan.StepHz(rate)))
			t += n*0.6 + 1
			if s.mode != "" {
				t += float64(listenS)
			}
		default:
			t += float64(s.secs + 3)
		}
	}
	return int(math.Ceil(t))
}
