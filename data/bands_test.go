package data

import (
	"encoding/json"
	"testing"
)

func TestBandsValid(t *testing.T) {
	var cat struct {
		DefaultRegion string            `json:"default_region"`
		Regions       map[string]string `json:"regions"`
		Bands         []struct {
			ID       string               `json:"id"`
			Regions  []string             `json:"regions"`
			Lo       float64              `json:"lo_mhz"`
			Hi       float64              `json:"hi_mhz"`
			Edges    map[string][]float64 `json:"edges"`
			Tune     float64              `json:"tune_mhz"`
			HF       bool                 `json:"hf"`
			Mode     string               `json:"mode"`
			Decoder  *string              `json:"decoder"`
			Channels []struct {
				MHz     float64  `json:"mhz"`
				Mode    string   `json:"mode"`
				Regions []string `json:"regions"`
			} `json:"channels"`
		} `json:"bands"`
	}
	if err := json.Unmarshal(Bands, &cat); err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Regions[cat.DefaultRegion]; !ok {
		t.Fatalf("default region %q unknown", cat.DefaultRegion)
	}
	modes := map[string]bool{"nfm": true, "am": true, "usb": true, "lsb": true, "none": true}
	decoders := map[string]bool{"rtl_433": true, "adsb": true, "aprs": true, "ais": true}
	seen := map[string]bool{}
	for _, b := range cat.Bands {
		if seen[b.ID] {
			t.Errorf("duplicate id %s", b.ID)
		}
		seen[b.ID] = true
		if b.Lo >= b.Hi || b.Tune < b.Lo-1 || b.Tune > b.Hi+1 {
			t.Errorf("%s: bad edges/tune %v-%v tune %v", b.ID, b.Lo, b.Hi, b.Tune)
		}
		if b.HF != (b.Lo < 24) {
			t.Errorf("%s: hf flag %v but lo %v MHz", b.ID, b.HF, b.Lo)
		}
		if !modes[b.Mode] {
			t.Errorf("%s: mode %q", b.ID, b.Mode)
		}
		if b.Decoder != nil && !decoders[*b.Decoder] {
			t.Errorf("%s: decoder %q", b.ID, *b.Decoder)
		}
		for _, r := range b.Regions {
			if _, ok := cat.Regions[r]; !ok {
				t.Errorf("%s: region %q", b.ID, r)
			}
		}
		for r, e := range b.Edges {
			if len(e) != 2 || e[0] >= e[1] {
				t.Errorf("%s: edges[%s] = %v", b.ID, r, e)
			}
		}
		for _, c := range b.Channels {
			if !modes[c.Mode] {
				t.Errorf("%s: channel %v mode %q", b.ID, c.MHz, c.Mode)
			}
		}
	}
}
