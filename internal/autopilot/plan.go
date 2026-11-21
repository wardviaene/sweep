package autopilot

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/meta"
)

// Handling kinds, in the order the autopilot prefers them.
const (
	KindPacket = "packet"
	KindSSTV   = "sstv"
	KindFM     = "nfm"
)

// Choice is the downlink the autopilot uses for a pass and how it is handled.
type Choice struct {
	Kind       string // packet | sstv | nfm
	DownlinkHz int64
	Baud       int
}

// Decoder is the name shown in the API: the decoder name, or "nfm" for recorded voice.
func (c Choice) Decoder() string { return c.Kind }

const issSSTVHz = 145_800_000

// choose picks the downlink to use: packet, then SSTV, then FM voice.
func choose(p meta.Pass) (Choice, bool) {
	var best *Choice
	rank := map[string]int{KindPacket: 0, KindSSTV: 1, KindFM: 2}
	for _, dl := range p.Downlinks {
		var kind string
		switch {
		case dl.Decoder != nil && *dl.Decoder == "packet":
			kind = KindPacket
		case strings.Contains(strings.ToUpper(dl.Mode), "SSTV"),
			p.Norad == 25544 && dl.FreqHz >= issSSTVHz-15_000 && dl.FreqHz <= issSSTVHz+15_000:
			kind = KindSSTV
		case dl.Decoder != nil && *dl.Decoder == "nfm":
			kind = KindFM
		default:
			continue
		}
		if best == nil || rank[kind] < rank[best.Kind] {
			best = &Choice{Kind: kind, DownlinkHz: dl.FreqHz, Baud: dl.Baud}
		}
	}
	if best == nil {
		return Choice{}, false
	}
	return *best, true
}

// Planned is one upcoming pass with the autopilot's decision.
type Planned struct {
	Pass    meta.Pass
	Choice  Choice
	WillRun bool
	Reason  string
}

func passKey(p meta.Pass) string { return fmt.Sprintf("%d/%d", p.Norad, p.AOS) }

// buildPlan decides which of the passes the autopilot handles. Passes that
// don't qualify (satellite not selected, below min elevation, no usable
// downlink) are left out; qualifying passes that lose against an overlapping
// pass with a higher maximum elevation are listed with a reason. The windows
// include the AOS lead and LOS tail, because the radio is needed for both.
func buildPlan(passes []meta.Pass, ap config.Autopilot, lead, tail time.Duration) []Planned {
	var cands []Planned
	for _, p := range passes {
		if !ap.Wants(p.Norad) || p.MaxElevDeg < ap.MinEl() {
			continue
		}
		c, ok := choose(p)
		if !ok {
			continue
		}
		cands = append(cands, Planned{Pass: p, Choice: c, WillRun: true})
	}
	win := func(p meta.Pass) (int64, int64) {
		return p.AOS - int64(lead/time.Second), p.LOS + int64(tail/time.Second)
	}
	order := append([]int(nil), seq(len(cands))...)
	sort.SliceStable(order, func(i, j int) bool {
		a, b := cands[order[i]].Pass, cands[order[j]].Pass
		if a.MaxElevDeg != b.MaxElevDeg {
			return a.MaxElevDeg > b.MaxElevDeg
		}
		return a.AOS < b.AOS
	})
	var kept []int
	for _, i := range order {
		s, e := win(cands[i].Pass)
		for _, k := range kept {
			ks, ke := win(cands[k].Pass)
			if s < ke && ks < e {
				cands[i].WillRun = false
				cands[i].Reason = fmt.Sprintf("overlaps %s (max elevation %.0f°)", cands[k].Pass.Name, cands[k].Pass.MaxElevDeg)
				break
			}
		}
		if cands[i].WillRun {
			kept = append(kept, i)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Pass.AOS != cands[j].Pass.AOS {
			return cands[i].Pass.AOS < cands[j].Pass.AOS
		}
		return cands[i].Pass.Norad < cands[j].Pass.Norad
	})
	if !ap.Enabled {
		for i := range cands {
			cands[i].WillRun, cands[i].Reason = false, "autopilot disabled"
		}
	}
	return cands
}

func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}
