package meta

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/wardviaene/sweep/data"
)

// block is one ICAO 24-bit address allocation.
type block struct {
	start, end uint32
	country    string
}

// loadBlocks parses data.ICAOCountries into blocks sorted by start.
func loadBlocks(raw []byte) ([]block, error) {
	var in []struct {
		Start   string `json:"start"`
		End     string `json:"end"`
		Country string `json:"country"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := make([]block, 0, len(in))
	for _, b := range in {
		s, err := strconv.ParseUint(b.Start, 16, 32)
		if err != nil {
			return nil, err
		}
		e, err := strconv.ParseUint(b.End, 16, 32)
		if err != nil {
			return nil, err
		}
		out = append(out, block{uint32(s), uint32(e), b.Country})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out, nil
}

var bundledBlocks, bundledBlocksErr = loadBlocks(data.ICAOCountries)

// countryOf returns the country owning hex, "" if unallocated or unknown.
func countryOf(blocks []block, hex uint32) string {
	i := sort.Search(len(blocks), func(i int) bool { return blocks[i].start > hex }) - 1
	if i >= 0 && hex <= blocks[i].end {
		return blocks[i].country
	}
	return ""
}

// ParseHex parses a 6-digit Mode S hex address ("a1b2c3", optional "~" prefix
// as used by UAT for non-ICAO addresses is rejected by the caller).
func ParseHex(s string) (uint32, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 6 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}
