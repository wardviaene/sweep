package autopilot

import (
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/meta"
)

func str(s string) *string { return &s }

func pass(norad int, name string, aos, los int64, el float64, dls ...meta.Downlink) meta.Pass {
	return meta.Pass{Norad: norad, Name: name, AOS: aos, LOS: los, MaxElevDeg: el, Downlinks: dls}
}

var (
	dlPacket = meta.Downlink{FreqHz: 437_500_000, Mode: "GMSK", Baud: 9600, Decoder: str("packet")}
	dlFM     = meta.Downlink{FreqHz: 145_900_000, Mode: "FM", Decoder: str("nfm")}
	dlSSTV   = meta.Downlink{FreqHz: 145_800_000, Mode: "SSTV"}
)

func TestChoose(t *testing.T) {
	c, ok := choose(pass(1, "A", 0, 1, 30, dlFM, dlSSTV, dlPacket))
	if !ok || c.Kind != KindPacket || c.DownlinkHz != 437_500_000 || c.Baud != 9600 {
		t.Fatalf("packet first: %+v", c)
	}
	if c, _ := choose(pass(1, "A", 0, 1, 30, dlFM, dlSSTV)); c.Kind != KindSSTV {
		t.Fatalf("sstv before fm: %+v", c)
	}
	if c, _ := choose(pass(1, "A", 0, 1, 30, dlFM)); c.Kind != KindFM {
		t.Fatalf("fm: %+v", c)
	}
	// the ISS on 145.800 MHz counts as SSTV even when SatNOGS calls it FM
	iss := meta.Downlink{FreqHz: 145_800_000, Mode: "FM", Decoder: str("nfm")}
	if c, _ := choose(pass(25544, "ISS", 0, 1, 30, iss)); c.Kind != KindSSTV {
		t.Fatalf("iss: %+v", c)
	}
	if c, _ := choose(pass(99, "OTHER", 0, 1, 30, iss)); c.Kind != KindFM {
		t.Fatalf("non-iss on 145.800: %+v", c)
	}
	if _, ok := choose(pass(1, "A", 0, 1, 30, meta.Downlink{FreqHz: 437_000_000, Mode: "CW"})); ok {
		t.Fatal("cw is not usable")
	}
}

func TestBuildPlan(t *testing.T) {
	ap := config.Autopilot{Enabled: true}
	lead, tail := 30*time.Second, 10*time.Second
	passes := []meta.Pass{
		pass(1, "LOW", 1000, 1600, 10, dlPacket),                  // below 20 degrees
		pass(2, "MID", 2000, 2600, 35, dlPacket),                  // overlaps HIGH
		pass(3, "HIGH", 2500, 3100, 62, dlFM),                     // wins
		pass(4, "NODL", 4000, 4600, 70, meta.Downlink{FreqHz: 1}), // no usable downlink
		pass(5, "NEAR", 3120, 3700, 40, dlSSTV),                   // starts 20 s after HIGH's LOS: lead+tail overlap
		pass(6, "FAR", 5000, 5600, 25, dlPacket),
	}
	plan := buildPlan(passes, ap, lead, tail)
	got := map[string]Planned{}
	for _, p := range plan {
		got[p.Pass.Name] = p
	}
	if _, ok := got["LOW"]; ok {
		t.Error("LOW should not qualify")
	}
	if _, ok := got["NODL"]; ok {
		t.Error("NODL should not qualify")
	}
	if !got["HIGH"].WillRun || !got["FAR"].WillRun {
		t.Errorf("HIGH and FAR should run: %+v", got)
	}
	if got["MID"].WillRun || got["MID"].Reason == "" {
		t.Errorf("MID should be skipped with a reason: %+v", got["MID"])
	}
	if got["NEAR"].WillRun || got["NEAR"].Reason == "" { // HIGH has the higher elevation
		t.Errorf("NEAR should lose against HIGH: %+v", got["NEAR"])
	}
	for i := 1; i < len(plan); i++ {
		if plan[i].Pass.AOS < plan[i-1].Pass.AOS {
			t.Fatal("plan not sorted by AOS")
		}
	}

	// satellite filter and min elevation
	min := 30.0
	ap2 := config.Autopilot{Enabled: true, Satellites: []int{2, 6}, MinElevation: &min}
	plan = buildPlan(passes, ap2, lead, tail)
	if len(plan) != 1 || plan[0].Pass.Name != "MID" || !plan[0].WillRun { // FAR is 25 degrees
		t.Fatalf("filtered plan: %+v", plan)
	}

	// disabled: listed, nothing runs
	for _, p := range buildPlan(passes, config.Autopilot{}, lead, tail) {
		if p.WillRun || p.Reason != "autopilot disabled" {
			t.Fatalf("disabled: %+v", p)
		}
	}
}
