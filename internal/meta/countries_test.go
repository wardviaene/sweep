package meta

import (
	"strings"
	"testing"
)

func TestBlocksDoNotOverlap(t *testing.T) {
	if bundledBlocksErr != nil {
		t.Fatal(bundledBlocksErr)
	}
	if len(bundledBlocks) < 60 {
		t.Fatalf("only %d blocks", len(bundledBlocks))
	}
	for i, b := range bundledBlocks {
		if b.start > b.end {
			t.Errorf("%s: start after end", b.country)
		}
		if b.country == "" || strings.TrimSpace(b.country) != b.country {
			t.Errorf("block %06X: bad country %q", b.start, b.country)
		}
		if i > 0 && bundledBlocks[i-1].end >= b.start {
			t.Errorf("%s (%06X-%06X) overlaps %s (%06X-%06X)", bundledBlocks[i-1].country,
				bundledBlocks[i-1].start, bundledBlocks[i-1].end, b.country, b.start, b.end)
		}
	}
}

func TestCountryOf(t *testing.T) {
	for hex, want := range map[string]string{
		"a1b2c3": "United States", "A00000": "United States", "AFFFFF": "United States",
		"c05f21": "Canada", "3c4dd2": "Germany", "4840d6": "Netherlands", "400a2b": "United Kingdom",
		"38a1b2": "France", "7c6b0b": "Australia", "840123": "Japan", "710345": "Saudi Arabia",
		"e48f00": "Brazil", "0d0a00": "Mexico", "89a000": "", "000000": "", "b00000": "", "zzzzzz": "",
	} {
		h, ok := ParseHex(hex)
		if !ok {
			if want != "" {
				t.Errorf("%s: unparsable", hex)
			}
			continue
		}
		if got := countryOf(bundledBlocks, h); got != want {
			t.Errorf("%s: got %q want %q", hex, got, want)
		}
	}
}

func TestParseHex(t *testing.T) {
	if _, ok := ParseHex("abc"); ok {
		t.Error("short hex accepted")
	}
	if v, ok := ParseHex(" a1b2c3 "); !ok || v != 0xa1b2c3 {
		t.Error("trim")
	}
}
