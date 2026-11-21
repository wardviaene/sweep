package decoder

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/radio"
)

// Utility-meter protocols in rtl_433 (all disabled by default there).
var meterProtocols = []string{"149", "154", "160", "161", "228"} // ERT SCM, SCM+, IDM, IDM net, Neptune R900

// meterFilter keeps only the user's own meters. Other meters are counted by
// a salted hash so their IDs are never stored or published.
type meterFilter struct {
	mu    sync.Mutex
	own   map[string]bool
	other map[uint64]struct{}
	salt  uint64
}

func newMeterFilter(own []string) *meterFilter {
	f := &meterFilter{own: map[string]bool{}, other: map[uint64]struct{}{}, salt: uint64(time.Now().UnixNano())}
	for _, id := range own {
		f.own[strings.TrimSpace(id)] = true
	}
	return f
}

// meterID returns the meter identifier from an rtl_433 meter message.
func meterID(obj map[string]any) (string, bool) {
	for _, k := range []string{"id", "ERTSerialNumber", "EndpointID", "meter_id"} {
		switch v := obj[k].(type) {
		case float64:
			return strconv.FormatInt(int64(v), 10), true
		case string:
			return v, true
		}
	}
	return "", false
}

// keep reports whether obj belongs to one of the user's meters; otherwise it
// is counted anonymously.
func (f *meterFilter) keep(obj map[string]any) bool {
	id, ok := meterID(obj)
	if !ok {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.own[id] {
		return true
	}
	h := f.salt
	for _, c := range []byte(id) {
		h = (h ^ uint64(c)) * 1099511628211
	}
	f.other[h] = struct{}{}
	return false
}

func (f *meterFilter) counts() (own, other int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.own), len(f.other)
}

// runMeters listens for ERT/Neptune utility meters around 912-916 MHz and
// publishes only the user's own meters (config meter_ids).
func (m *Manager) runMeters(ctx context.Context, st *Status) error {
	bin, err := lookTool("rtl_433")
	if err != nil {
		return err
	}
	var own []string
	if m.MeterIDs != nil {
		own = m.MeterIDs()
	}
	filt := newMeterFilter(own)
	const center, rate = 914_000_000, 2_400_000
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(center), SampleRate: u32(rate)}); err != nil {
		return err
	}
	m.setFreq(st, center)
	args := []string{"-r", "cu8:-", "-s", strconv.Itoa(rate), "-f", strconv.Itoa(center), "-F", "json", "-M", "level", "-M", "time:unix"}
	for _, p := range meterProtocols {
		args = append(args, "-R", p)
	}
	cmd := exec.CommandContext(ctx, bin, args...)

	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-t.C:
				o, n := filt.counts()
				m.ev.Publish("meters", "snapshot", map[string]any{"own_ids": o, "other_meters_heard": n})
			}
		}
	}()
	if len(own) == 0 {
		m.ev.Publish("meters", "log", map[string]string{"line": "no meter_ids configured: other meters are counted but hidden; add your meter's ID (printed on the meter) in settings"})
	}
	return m.runPiped(ctx, "meters", cmd, func(line string) {
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			return
		}
		if filt.keep(obj) {
			m.ev.Publish("meters", "message", obj)
		}
	}, func(l string) bool {
		return strings.Contains(strings.ToLower(l), "error") || strings.Contains(l, "fail")
	})
}
