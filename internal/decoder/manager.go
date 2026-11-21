package decoder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/meta"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/sstv"
)

// Meta enriches decoded data from offline datasets (see docs/metadata.md).
// It is optional; *meta.DB implements it.
type Meta interface {
	// Aircraft names an aircraft from its Mode S hex and callsign.
	Aircraft(hex, callsign string) meta.AircraftInfo
	// VisibleSatellite finds the satellite above the horizon with a downlink near freqHz.
	VisibleSatellite(freqHz float64) (norad int, name string, ok bool)
}

// Names of the supported decoders.
var Names = []string{"rtl_433", "meters", "adsb", "uat", "aprs", "ais", "packet", "radiosonde", "dsd", "sstv"}

// Options are per-start decoder parameters (see docs/protocol.md).
type Options struct {
	Region string `json:"region"`  // aprs: us | eu
	FreqHz uint32 `json:"freq_hz"` // packet: channel frequency
	Baud   int    `json:"baud"`    // packet: 1200 | 9600
	Mode   string `json:"mode"`    // sstv: auto (default) or a mode name (Robot36, PD90, PD120, PD180, Martin1, Scottie1)
}

// Status is status.decoder.
type Status struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	FreqHz  uint32 `json:"freq_hz,omitempty"`
	Started int64  `json:"started,omitempty"`
	Events  int64  `json:"events"`
	Error   string `json:"error,omitempty"`
	// LastEventTS is the unix time (seconds) of the latest event from this run, 0 if none.
	LastEventTS float64 `json:"last_event_ts"`

	tap int // id of the decoder's private audio tap (0 = none), for RetuneChannel
}

// Manager runs at most one decoder.
type Manager struct {
	r        *radio.Radio
	ev       *Events
	SBSPort  int                                // dump1090 SBS output port (private, 127.0.0.1)
	Receiver func() (lat, lon float64, ok bool) // receiver location for dump1090, if set
	MeterIDs func() []string                    // utility meters the user owns; all others are hidden
	Audio    radio.AudioSink                    // where decoded digital voice (dsd) is sent
	ToolDirs []string                           // extra directories searched for decoder binaries
	Meta     Meta                               // optional: adds country, registration, airline, satellite names
	SSTVDir  string                             // where the sstv decoder writes its PNG files (default: a temp directory)

	mu   sync.Mutex
	cur  *run
	last Status // kept after a decoder stops so its error stays visible
}

type run struct {
	st      Status
	cancel  context.CancelFunc
	done    chan struct{}
	events0 int64
	opt     Options
}

// NewManager creates a manager and registers its status with the radio.
func NewManager(r *radio.Radio, ev *Events) *Manager {
	m := &Manager{r: r, ev: ev, SBSPort: 30903, last: Status{Name: "none"}}
	r.SetDecoderStatus(func() any { return m.Status() })
	return m
}

// Status reports the current decoder.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return m.last
	}
	st := m.cur.st
	st.Events = m.ev.Count() - m.cur.events0
	st.LastEventTS = m.ev.LastTS(st.Name, m.cur.events0)
	return st
}

// Events returns the store the decoders publish to.
func (m *Manager) Events() *Events { return m.ev }

// Current returns the running decoder and the options it was started with
// (defaults filled in), for restoring it later.
func (m *Manager) Current() (name string, opt Options, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return "", Options{}, false
	}
	opt = m.cur.opt
	if opt.FreqHz == 0 {
		opt.FreqHz = m.cur.st.FreqHz
	}
	return m.cur.st.Name, opt, true
}

// Stop stops the running decoder, if any.
func (m *Manager) Stop() {
	m.mu.Lock()
	cur := m.cur
	m.mu.Unlock()
	if cur == nil {
		return
	}
	cur.cancel()
	<-cur.done
}

// Start stops any running decoder and starts name ("none" just stops).
func (m *Manager) Start(name string, opt Options) error {
	region := opt.Region
	m.Stop()
	toolDirs = m.ToolDirs
	if name == "none" || name == "" {
		m.mu.Lock()
		m.last = Status{Name: "none"}
		m.mu.Unlock()
		return nil
	}
	var fn func(ctx context.Context, st *Status) error
	switch name {
	case "rtl_433":
		fn = m.runRTL433
	case "adsb":
		fn = m.runADSB
	case "aprs":
		fn = func(ctx context.Context, st *Status) error { return m.runDirewolf(ctx, st, "aprs", region) }
	case "ais":
		fn = func(ctx context.Context, st *Status) error { return m.runDirewolf(ctx, st, "ais", region) }
	case "meters":
		fn = m.runMeters
	case "uat":
		fn = m.runUAT
	case "radiosonde":
		fn = func(ctx context.Context, st *Status) error { return m.runRadiosonde(ctx, st, opt.FreqHz) }
	case "dsd":
		if opt.FreqHz == 0 {
			opt.FreqHz = m.r.Status().Demod.FreqHz
		}
		if opt.FreqHz == 0 {
			return fmt.Errorf("dsd: give freq_hz (or set the VFO first)")
		}
		if !amateurBand(opt.FreqHz) {
			return fmt.Errorf("dsd: %.4f MHz is outside the amateur bands; sweep only decodes digital voice on amateur frequencies (50-54, 144-148, 222-225, 420-450, 902-928, 1240-1300 MHz)", float64(opt.FreqHz)/1e6)
		}
		fn = func(ctx context.Context, st *Status) error { return m.runDSD(ctx, st, opt.FreqHz) }
	case "sstv":
		if opt.FreqHz == 0 {
			opt.FreqHz = 145_800_000 // the ISS SSTV downlink
		}
		mode, err := sstv.ParseMode(opt.Mode)
		if err != nil {
			return err
		}
		fn = func(ctx context.Context, st *Status) error { return m.runSSTV(ctx, st, opt.FreqHz, mode) }
	case "packet":
		if opt.Baud == 0 {
			opt.Baud = 1200
		}
		if opt.Baud != 1200 && opt.Baud != 9600 {
			return fmt.Errorf("packet: baud must be 1200 or 9600")
		}
		if opt.FreqHz == 0 {
			opt.FreqHz = m.r.Status().Demod.FreqHz
		}
		if opt.FreqHz == 0 {
			return fmt.Errorf("packet: give freq_hz (or set the VFO first)")
		}
		fn = func(ctx context.Context, st *Status) error { return m.runPacket(ctx, st, opt.FreqHz, opt.Baud) }
	default:
		return fmt.Errorf("decoder %q: want one of %s or none", name, strings.Join(Names, ", "))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cur := &run{st: Status{Name: name, Running: true, Started: time.Now().Unix()}, cancel: cancel,
		done: make(chan struct{}), events0: m.ev.Count(), opt: opt}
	m.mu.Lock()
	m.cur = cur
	m.mu.Unlock()

	ready := make(chan error, 1)
	go func() {
		defer close(cur.done)
		err := fn(ctx, &cur.st)
		m.mu.Lock()
		final := cur.st
		final.Running = false
		final.Events = m.ev.Count() - cur.events0
		final.LastEventTS = m.ev.LastTS(name, cur.events0)
		if err != nil && ctx.Err() == nil {
			final.Error = err.Error()
			m.ev.Publish(name, "log", map[string]string{"line": "stopped: " + err.Error()})
		}
		m.last = final
		if m.cur == cur {
			m.cur = nil
		}
		m.mu.Unlock()
		select {
		case ready <- err:
		default:
		}
	}()
	// Report immediate failures (missing tool, bad tune, no sonde found) to the caller.
	wait := 1500 * time.Millisecond
	if name == "radiosonde" && opt.FreqHz == 0 {
		wait = 8 * time.Second // the auto-search scans three windows first
	}
	select {
	case err := <-ready:
		return err
	case <-time.After(wait):
		return nil
	}
}

// setTap records the private audio tap of the running decoder so that
// RetuneChannel can move it. Decoders that run a private demod call it right
// after radio.AddAudioTap.
func (m *Manager) setTap(st *Status, id int) {
	m.mu.Lock()
	st.tap = id
	m.mu.Unlock()
}

// RetuneChannel moves the running decoder's channel to hz in place (Doppler
// tracking): its private demod keeps its filter state and the decoder keeps
// running. It fails when no decoder runs or the decoder has no private channel.
func (m *Manager) RetuneChannel(hz uint32) error {
	m.mu.Lock()
	cur := m.cur
	var id int
	if cur != nil {
		id = cur.st.tap
	}
	m.mu.Unlock()
	if cur == nil {
		return fmt.Errorf("no decoder running")
	}
	if id == 0 {
		return fmt.Errorf("decoder %s has no retunable channel", cur.st.Name)
	}
	if err := m.r.SetTapFreq(id, hz); err != nil {
		return err
	}
	m.mu.Lock()
	cur.st.FreqHz = hz
	m.mu.Unlock()
	return nil
}

func (m *Manager) setFreq(st *Status, f uint32) {
	m.mu.Lock()
	st.FreqHz = f
	m.mu.Unlock()
}

// toolDirs are searched before PATH (set from Manager.ToolDirs at start).
var toolDirs []string

func lookTool(names ...string) (string, error) {
	for _, n := range names {
		for _, d := range toolDirs {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return p, nil
			}
		}
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in %v or PATH (see scripts/install-decoders.sh, or brew install %s)", names[0], toolDirs, names[0])
}

// pumpIQ copies tap blocks into w until the tap closes or ctx ends.
func pumpIQ(ctx context.Context, iq <-chan []byte, w io.WriteCloser) {
	defer w.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-iq:
			if !ok {
				return
			}
			if _, err := w.Write(b); err != nil {
				return
			}
		}
	}
}

// pumpPCM writes s16le audio into w.
func pumpPCM(ctx context.Context, pcm <-chan []int16, w io.WriteCloser) {
	defer w.Close()
	var buf []byte
	for {
		select {
		case <-ctx.Done():
			return
		case a, ok := <-pcm:
			if !ok {
				return
			}
			buf = buf[:0]
			for _, v := range a {
				buf = append(buf, byte(v), byte(uint16(v)>>8))
			}
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	}
}

// logLines publishes interesting stderr lines as log events.
func (m *Manager) logLines(name string, r io.Reader, keep func(string) bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		log.Printf("%s: %s", name, line)
		if keep == nil || keep(line) {
			m.ev.Publish(name, "log", map[string]string{"line": line})
		}
	}
}

// tempDir makes a private working directory for a decoder.
func tempDir(name string) (string, func(), error) {
	d, err := os.MkdirTemp("", "sweep-"+name+"-")
	if err != nil {
		return "", nil, err
	}
	return d, func() { os.RemoveAll(d) }, nil
}

// amateurBand reports whether f (Hz) is inside a VHF/UHF amateur allocation
// (union of ITU regions 1 and 2). Digital voice decoding is limited to these.
func amateurBand(f uint32) bool {
	mhz := float64(f) / 1e6
	for _, b := range [][2]float64{{50, 54}, {144, 148}, {222, 225}, {420, 450}, {902, 928}, {1240, 1300}} {
		if mhz >= b[0] && mhz <= b[1] {
			return true
		}
	}
	return false
}

// enrichAircraft adds the metadata fields to every aircraft of a snapshot.
func (m *Manager) enrichAircraft(ac []aircraft) {
	if m.Meta == nil {
		return
	}
	for i := range ac {
		ac[i].AircraftInfo = m.Meta.Aircraft(ac[i].Hex, ac[i].Flight)
	}
}

// enrichAircraftMap does the same for uat aircraft (maps shaped like the adsb snapshot).
func (m *Manager) enrichAircraftMap(a map[string]any) {
	if m.Meta == nil {
		return
	}
	hex, _ := a["hex"].(string)
	flight, _ := a["flight"].(string)
	info := m.Meta.Aircraft(hex, flight)
	if info.Country != "" {
		a["country"] = info.Country
	}
	if info.Registration != "" {
		a["registration"] = info.Registration
	}
	if info.Type != "" {
		a["type"] = info.Type
	}
	if info.Year != 0 {
		a["year"] = info.Year
	}
	if info.Airline != nil {
		a["airline"] = map[string]any{"icao": info.Airline.ICAO, "name": info.Airline.Name, "radio": info.Airline.Radio}
		a["flight_number"] = info.FlightNumber
	}
}

// tagSatellite adds satellite {norad, name} to a packet message when a satellite
// with a downlink near freqHz is above the horizon.
func (m *Manager) tagSatellite(msg map[string]any, freqHz uint32) {
	if m.Meta == nil {
		return
	}
	if norad, name, ok := m.Meta.VisibleSatellite(float64(freqHz)); ok {
		msg["satellite"] = map[string]any{"norad": norad, "name": name}
	}
}
