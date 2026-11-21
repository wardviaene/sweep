package meta

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Public registry facts (N-number <-> Mode S hex) taken from the FAA releasable
// registry. No owner data.
var nnumberFixtures = []struct{ n, hex string }{
	{"N1", "A00001"}, {"N1A", "A00002"}, {"N1ZZ", "A00259"}, {"N10A", "A0025B"},
	{"N100", "A004B3"}, {"N100AB", "A004B6"}, {"N1000Z", "A00724"}, {"N10000", "A00725"},
	{"N1000A", "A0070D"}, {"N11A", "A029DA"}, {"N12", "A05158"}, {"N12345", "A061D9"},
	{"N177", "A1332D"}, {"N494XC", "A62104"}, {"N8000A", "AAE436"}, {"N99ZZ", "ADD2A1"},
	{"N99999", "ADF7C7"},
}

func TestNNumberFromHex(t *testing.T) {
	for _, f := range nnumberFixtures {
		h, _ := strconv.ParseUint(f.hex, 16, 32)
		got, ok := NNumberFromHex(uint32(h))
		if !ok || got != f.n {
			t.Errorf("%s: got %q ok=%v, want %s", f.hex, got, ok, f.n)
		}
	}
	for _, hex := range []uint32{0xA00000, 0xADF7C8, 0xAE0000, 0x3C4DD2, 0} {
		if n, ok := NNumberFromHex(hex); ok {
			t.Errorf("%06X should be outside the N-number range, got %s", hex, n)
		}
	}
}

// Every address in the range maps to a distinct, well-formed registration.
func TestNNumberUniqueAndValid(t *testing.T) {
	seen := make(map[string]struct{}, nCount)
	for h := uint32(nFirstHex); h <= nLastHexIn; h++ {
		n, ok := NNumberFromHex(h)
		if !ok || !IsRegistration(n) {
			t.Fatalf("%06X: %q ok=%v", h, n, ok)
		}
		if _, dup := seen[n]; dup {
			t.Fatalf("%06X: duplicate %s", h, n)
		}
		seen[n] = struct{}{}
	}
	if len(seen) != nCount {
		t.Fatalf("%d registrations, want %d", len(seen), nCount)
	}
}

// With the downloaded FAA registry present (SWEEP_DB or the default db dir),
// every row must agree with the algorithm. Skipped otherwise.
func TestNNumberAgainstFAAFile(t *testing.T) {
	dir := os.Getenv("SWEEP_DB")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share", "sweep", "db")
	}
	f, err := os.Open(filepath.Join(dir, "faa", "MASTER.txt"))
	if err != nil {
		t.Skip("FAA MASTER.txt not downloaded")
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	sc.Scan() // header
	n, bad := 0, 0
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r,")
		fs := strings.Split(line, ",")
		if len(fs) < 34 {
			continue
		}
		reg, hex := "N"+strings.TrimSpace(fs[0]), strings.TrimSpace(fs[len(fs)-1])
		h, err := strconv.ParseUint(hex, 16, 32)
		if err != nil || h < nFirstHex || h > nLastHexIn {
			continue // non-US block (never happens in practice)
		}
		n++
		if got, _ := NNumberFromHex(uint32(h)); got != reg {
			bad++
			if bad < 5 {
				t.Errorf("%s: algorithm gives %s, FAA says %s", hex, got, reg)
			}
		}
	}
	t.Logf("checked %d registry rows, %d mismatches", n, bad)
}
