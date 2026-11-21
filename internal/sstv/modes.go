// Package sstv decodes slow-scan television (SSTV) pictures from FM-demodulated
// audio, natively in Go. It supports Robot 36, PD 90/120/180, Martin 1 and
// Scottie 1, detects the mode from the VIS header (or syncs on line pulses when
// the mode is given) and corrects slant from the measured line period.
//
// The encoder (Encode) exists to synthesize test audio.
package sstv

import (
	"fmt"
	"strings"
)

// Rate is the internal sample rate of the decoder and the default encoder rate.
const Rate = 11025

// Tone frequencies in Hz (JL Barber, "Proposed SSTV mode specifications", 1998;
// Dayton Paper SSTV Handbook; the same values are used by all supported modes).
const (
	FreqSync   = 1200.0 // sync pulses, VIS start/stop bits
	FreqBlack  = 1500.0 // luminance 0; also porches and separators
	FreqWhite  = 2300.0 // luminance 255
	FreqLeader = 1900.0 // VIS leader, also chroma 128
	FreqBit1   = 1100.0 // VIS bit value 1
	FreqBit0   = 1300.0 // VIS bit value 0
)

// VIS header timings in ms (VIS spec, Dayton Paper / sstv-handbook).
const (
	visLeaderMs = 300.0 // 1900 Hz, twice, separated by the break
	visBreakMs  = 10.0  // 1200 Hz
	visBitMs    = 30.0  // start bit (1200), 8 data bits (1100/1300, LSB first, even parity), stop bit (1200)
)

// Format selects how the scanned components of one sync unit become pixels.
type Format int

// Formats.
const (
	FormatRobot36 Format = iota // Y per line, alternating R-Y / B-Y at half width
	FormatPD                    // one unit = two rows: Y1, R-Y, B-Y, Y2
	FormatRGB                   // Martin / Scottie: sequential G, B, R scans
)

// Channel is one scanned component.
type Channel int

// Channels.
const (
	ChY Channel = iota
	ChY2
	ChCr // R-Y
	ChCb // B-Y
	ChR
	ChG
	ChB
	ChChroma // Robot 36: R-Y or B-Y, told apart by the separator
)

// Comp is one scanned component inside a sync unit.
type Comp struct {
	Ch    Channel
	OffMs float64 // start, from the start of the unit
	DurMs float64
	N     int // pixels
}

// Mode describes one SSTV mode. A unit is the stretch between two sync pulses:
// one image row, except for PD where it is two rows.
type Mode struct {
	Name          string
	VIS           int
	Width, Height int
	Format        Format
	RowsPerUnit   int
	UnitMs        float64 // sync to sync
	SyncMs        float64 // 1200 Hz pulse length
	SyncOffMs     float64 // start of the sync pulse inside the unit (Scottie: mid-line)
	PorchMs       float64 // 1500 Hz after the sync pulse
	Comps         []Comp
	// Robot 36 only: the separator before the chroma scan (1500 Hz = R-Y, 2300 Hz = B-Y).
	SepOffMs, SepDurMs float64
}

// Units is the number of sync units in an image.
func (m *Mode) Units() int { return m.Height / m.RowsPerUnit }

// DurationS is the transmission time of the picture without the VIS header.
func (m *Mode) DurationS() float64 { return float64(m.Units()) * m.UnitMs / 1000 }

// Modes are the supported modes. Timings:
//
//	Robot 36: Robot Research / sstv-handbook. Line 150 ms = sync 9 + porch 3 (1500)
//	  + Y 88 (320 px) + separator 4.5 + porch 1.5 (1900) + chroma 44 (160 px).
//	  Even lines carry R-Y (separator 1500 Hz), odd lines B-Y (2300 Hz).
//	PD 90/120/180: Paul Dunn / sstv-handbook. Per line pair: sync 20 ms,
//	  porch 2.08 ms (1500), then Y(even row), R-Y, B-Y, Y(odd row), each
//	  width*pixel time. PD90 0.532 ms/px (320x256), PD120 0.19 ms/px (640x496,
//	  pair = 508.48 ms), PD180 0.286 ms/px (640x496, pair = 754.24 ms).
//	Martin 1: Martin Emmerson. Line = sync 4.862 + porch 0.572, then
//	  G, B, R scans of 146.432 ms (0.4576 ms/px x 320), each followed by a
//	  0.572 ms 1500 Hz separator. Line 446.446 ms.
//	Scottie 1: Eddie Murphy. Line = sep 1.5, G 138.24, sep 1.5, B 138.24,
//	  sync 9 (mid-line), porch 1.5, R 138.24 (0.432 ms/px x 320). Line 428.22 ms.
//
// VIS codes: Robot 36 = 8, PD90 = 99, PD120 = 95, PD180 = 96, Martin 1 = 44,
// Scottie 1 = 60.
var Modes = []*Mode{
	{
		Name: "Robot36", VIS: 8, Width: 320, Height: 240, Format: FormatRobot36, RowsPerUnit: 1,
		UnitMs: 150, SyncMs: 9, SyncOffMs: 0, PorchMs: 3,
		Comps: []Comp{
			{ChY, 12, 88, 320},
			{ChChroma, 106, 44, 160},
		},
		SepOffMs: 100, SepDurMs: 4.5,
	},
	pdMode("PD90", 99, 320, 256, 0.532),
	pdMode("PD120", 95, 640, 496, 0.19),
	pdMode("PD180", 96, 640, 496, 0.286),
	{
		Name: "Martin1", VIS: 44, Width: 320, Height: 256, Format: FormatRGB, RowsPerUnit: 1,
		UnitMs: 446.446, SyncMs: 4.862, SyncOffMs: 0, PorchMs: 0.572,
		Comps: []Comp{{ChG, 5.434, 146.432, 320}, {ChB, 152.438, 146.432, 320}, {ChR, 299.442, 146.432, 320}},
	},
	{
		Name: "Scottie1", VIS: 60, Width: 320, Height: 256, Format: FormatRGB, RowsPerUnit: 1,
		UnitMs: 428.22, SyncMs: 9, SyncOffMs: 279.48, PorchMs: 1.5,
		Comps: []Comp{{ChG, 1.5, 138.24, 320}, {ChB, 141.24, 138.24, 320}, {ChR, 289.98, 138.24, 320}},
	},
}

func pdMode(name string, vis, w, h int, pxMs float64) *Mode {
	ch := pxMs * float64(w)
	off := 20 + 2.08
	return &Mode{
		Name: name, VIS: vis, Width: w, Height: h, Format: FormatPD, RowsPerUnit: 2,
		UnitMs: off + 4*ch, SyncMs: 20, PorchMs: 2.08,
		Comps: []Comp{
			{ChY, off, ch, w}, {ChCr, off + ch, ch, w}, {ChCb, off + 2*ch, ch, w}, {ChY2, off + 3*ch, ch, w},
		},
	}
}

// ByVIS returns the mode with the given VIS code, or nil.
func ByVIS(code int) *Mode {
	for _, m := range Modes {
		if m.VIS == code {
			return m
		}
	}
	return nil
}

// ParseMode resolves a mode name ("PD120", "robot 36", "martin-1"...), case and
// punctuation insensitive. "" and "auto" return nil (detect from the VIS).
func ParseMode(s string) (*Mode, error) {
	n := norm(s)
	if n == "" || n == "auto" {
		return nil, nil
	}
	alias := map[string]string{"r36": "robot36", "m1": "martin1", "s1": "scottie1"}
	if a, ok := alias[n]; ok {
		n = a
	}
	var names []string
	for _, m := range Modes {
		if norm(m.Name) == n {
			return m, nil
		}
		names = append(names, m.Name)
	}
	return nil, fmt.Errorf("unknown SSTV mode %q (want auto or one of %s)", s, strings.Join(names, ", "))
}

func norm(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_', '.':
			return -1
		}
		return r
	}, strings.ToLower(s))
}
