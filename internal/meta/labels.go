package meta

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/wardviaene/sweep/data"
)

// channel is one named frequency of the band catalog (data/bands.json).
type channel struct {
	hz   float64
	name string // "NOAA Weather Radio: WX2"
}

// LabelToleranceHz is how close a catalog channel must be to label a carrier.
const LabelToleranceHz = 5000

func (d *DB) loadChannels() {
	var cat struct {
		Bands []struct {
			Name     string `json:"name"`
			Channels []struct {
				MHz  float64 `json:"mhz"`
				Name string  `json:"name"`
			} `json:"channels"`
		} `json:"bands"`
	}
	if json.Unmarshal(data.Bands, &cat) != nil {
		return
	}
	var cs []channel
	for _, b := range cat.Bands {
		for _, c := range b.Channels {
			cs = append(cs, channel{c.MHz * 1e6, b.Name + ": " + c.Name})
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].hz < cs[j].hz })
	d.channels = cs
}

// channelLabel returns the catalog channel closest to hz within tol, or "".
func channelLabel(cs []channel, hz, tol float64) string {
	i := sort.Search(len(cs), func(i int) bool { return cs[i].hz >= hz-tol })
	best, bd := "", math.Inf(1)
	for ; i < len(cs) && cs[i].hz <= hz+tol; i++ {
		if dd := math.Abs(cs[i].hz - hz); dd < bd {
			best, bd = cs[i].name, dd
		}
	}
	return best
}

// Label names the signal at hz: a visible satellite whose downlink matches
// ("NAME downlink"), else a catalog channel within 5 kHz ("NOAA Weather
// Radio: WX2"), else "".
func (d *DB) Label(hz float64) string {
	if d == nil {
		return ""
	}
	if c := d.visibleMatch(hz, DefaultToleranceHz); c != nil {
		return c.Name + " downlink"
	}
	return channelLabel(d.channels, hz, LabelToleranceHz)
}
