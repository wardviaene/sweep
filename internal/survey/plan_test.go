package survey

import (
	"slices"
	"strings"
	"testing"
)

func ids(steps []Step) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.ID)
	}
	return out
}

func plan(t *testing.T, antenna, region string, fair bool, explicit ...string) []Step {
	t.Helper()
	steps, err := buildPlan(LoadCatalog(), antenna, region, 20, fair, explicit)
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

func TestPlanJPole(t *testing.T) {
	got := ids(plan(t, "jpole", "us", false))
	want := []string{"scan:2m", "aprs", "scan:marine", "ais", "scan:noaawx", "scan:70cm", "dsd", "scan:sat70", "packet",
		"scan:ism433", "rtl_433@ism433"}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestPlanFairAndHF(t *testing.T) {
	fair := ids(plan(t, "jpole", "us", true))
	for _, id := range []string{"scan:sonde", "radiosonde@sonde", "scan:air", "scan:frs"} {
		if !slices.Contains(fair, id) {
			t.Errorf("include_fair plan lacks %s: %v", id, fair)
		}
	}
	if slices.Contains(fair, "scan:hf") {
		t.Error("hf band planned for a VHF antenna")
	}
	if slices.Contains(ids(plan(t, "wideband", "us", false)), "scan:hf") {
		t.Error("hf band planned for wideband")
	}
	if !slices.Contains(ids(plan(t, "hfwire", "us", false)), "scan:hf") {
		t.Error("hfwire plan lacks scan:hf")
	}
}

func TestPlanAtRuleAndRegion(t *testing.T) {
	us := ids(plan(t, "wideband", "us", false))
	for _, id := range []string{"rtl_433@ism433", "rtl_433@ism915", "uat", "adsb", "aprs"} {
		if !slices.Contains(us, id) {
			t.Errorf("us plan lacks %s: %v", id, us)
		}
	}
	if slices.Contains(us, "rtl_433") || slices.Contains(us, "scan:pmr446") {
		t.Errorf("us plan has rtl_433 without @ or an eu band: %v", us)
	}
	eu := ids(plan(t, "wideband", "eu", false))
	if !slices.Contains(eu, "rtl_433@ism868") || slices.Contains(eu, "uat") || !slices.Contains(eu, "scan:pmr446") {
		t.Errorf("eu plan: %v", eu)
	}
}

func TestPlanSteps(t *testing.T) {
	steps := plan(t, "jpole", "eu", false)
	by := map[string]Step{}
	for _, s := range steps {
		by[s.ID] = s
	}
	// eu amateur edges are clamped to the band: 144-146 MHz
	if s := by["scan:2m"]; s.FreqLoHz != 144_000_000 || s.FreqHiHz != 146_000_000 {
		t.Errorf("scan:2m range %d-%d", s.FreqLoHz, s.FreqHiHz)
	}
	if s := by["aprs"]; s.FreqHz != 144_800_000 || s.opt.Region != "eu" || s.Label != "APRS (144.800 MHz)" {
		t.Errorf("aprs step %+v", s)
	}
	if s := by["packet"]; s.State != "skipped" || s.Reason == "" {
		t.Errorf("packet not skipped: %+v", s)
	}
	if s := by["rtl_433@ism433"]; s.tuneHz != 433_920_000 || s.secs != 20 {
		t.Errorf("rtl_433 step %+v", s)
	}
	if by["dsd"].Kind != "decoder" || by["dsd"].opt.FreqHz != 0 {
		t.Errorf("dsd step %+v", by["dsd"])
	}
}

func TestPlanExplicit(t *testing.T) {
	got := ids(plan(t, "jpole", "us", false, "aprs", "scan:2m", "aprs", "rtl_433@ism915"))
	if want := []string{"aprs", "scan:2m", "rtl_433@ism915"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	_, err := buildPlan(LoadCatalog(), "jpole", "us", 20, false, []string{"aprs", "rtl_433"})
	if err == nil || !strings.Contains(err.Error(), `"rtl_433"`) || !strings.Contains(err.Error(), "rtl_433@ism433") {
		t.Fatalf("unknown step error: %v", err)
	}
	if _, err := buildPlan(LoadCatalog(), "jpole", "us", 20, false, []string{"scan:hf"}); err == nil {
		t.Fatal("scan:hf accepted for jpole")
	}
	if _, err := buildPlan(LoadCatalog(), "nope", "us", 20, false, nil); err == nil {
		t.Fatal("unknown antenna accepted")
	}
	if _, err := buildPlan(LoadCatalog(), "jpole", "mars", 20, false, nil); err == nil {
		t.Fatal("unknown region accepted")
	}
	if _, err := buildPlan(LoadCatalog(), "none", "us", 20, false, nil); err == nil {
		t.Fatal("empty plan accepted")
	}
}

func TestEstimate(t *testing.T) {
	steps := []Step{
		{Kind: "scan", FreqLoHz: 144e6, FreqHiHz: 148e6},           // 3 steps of 1.6384 MHz
		{Kind: "decoder", secs: 20},                                // 23
		{Kind: "decoder", secs: 20, State: "skipped", Reason: "x"}, // 0
	}
	if got := estimate(steps, 2_048_000, 0); got != 26 { // 3*0.6+1 = 2.8, +23 = 25.8 rounded up
		t.Fatalf("estimate %d", got)
	}
}
