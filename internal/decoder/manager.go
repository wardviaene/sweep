package decoder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/radio"
)

// Names of the supported decoders.
var Names = []string{"rtl_433", "adsb", "aprs", "ais"}

// Status is status.decoder.
type Status struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	FreqHz  uint32 `json:"freq_hz,omitempty"`
	Started int64  `json:"started,omitempty"`
	Events  int64  `json:"events"`
	Error   string `json:"error,omitempty"`
}

// Manager runs at most one decoder.
type Manager struct {
	r        *radio.Radio
	ev       *Events
	SBSPort  int                                // dump1090 SBS output port (private, 127.0.0.1)
	Receiver func() (lat, lon float64, ok bool) // receiver location for dump1090, if set

	mu   sync.Mutex
	cur  *run
	last Status // kept after a decoder stops so its error stays visible
}

type run struct {
	st      Status
	cancel  context.CancelFunc
	done    chan struct{}
	events0 int64
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
	return st
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
func (m *Manager) Start(name, region string) error {
	m.Stop()
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
	default:
		return fmt.Errorf("decoder %q: want one of %s or none", name, strings.Join(Names, ", "))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cur := &run{st: Status{Name: name, Running: true, Started: time.Now().Unix()}, cancel: cancel,
		done: make(chan struct{}), events0: m.ev.Count()}
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
	// Report immediate failures (missing tool, bad tune) to the caller.
	select {
	case err := <-ready:
		return err
	case <-time.After(1500 * time.Millisecond):
		return nil
	}
}

func (m *Manager) setFreq(st *Status, f uint32) {
	m.mu.Lock()
	st.FreqHz = f
	m.mu.Unlock()
}

func lookTool(names ...string) (string, error) {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH (brew install %s)", names[0], names[0])
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
