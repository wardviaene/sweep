package decoder

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/sstv"
)

// sstvProgressEvery is the minimum time between progress events (and PNG rewrites).
const sstvProgressEvery = time.Second

// runSSTV demodulates NFM at freq on a private channel and feeds the audio to
// the native SSTV decoder. It stays armed and decodes every picture it hears;
// the PNG is rewritten as lines arrive (atomic rename), see docs/autopilot.md.
func (m *Manager) runSSTV(ctx context.Context, st *Status, freq uint32, mode *sstv.Mode) error {
	dir := m.SSTVDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "sweep-sstv")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sstv: %w", err)
	}
	cur := m.r.Status()
	// Put the channel a quarter of the span above center, away from the DC spike.
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(freq - cur.SampleRate/4)}); err != nil {
		return err
	}
	m.setFreq(st, freq)
	id, pcm, rate := m.r.AddAudioTap(freq, dsp.ModeNFM, 25000, true)
	defer m.r.RemoveTap(id)
	m.setTap(st, id)

	want := "auto"
	if mode != nil {
		want = mode.Name
	}
	m.ev.Publish("sstv", "log", map[string]string{"line": fmt.Sprintf("listening on %.4f MHz, mode %s", float64(freq)/1e6, want)})

	dec := sstv.NewDecoder(rate, mode)
	var (
		active   *sstv.Mode
		started  time.Time
		file     string
		lastProg time.Time
	)
	url := func() string { return "/api/files/sstv/" + filepath.Base(file) }
	save := func(img *image.RGBA) {
		if err := sstv.WritePNG(file, img); err != nil {
			m.ev.Publish("sstv", "log", map[string]string{"line": "write " + file + ": " + err.Error()})
		}
	}
	dec.OnStart = func(md *sstv.Mode) {
		active, started, lastProg = md, time.Now(), time.Now()
		file = uniqueName(dir, started.UTC().Format("20060102-150405")+"_"+strings.ToLower(md.Name), ".png")
		m.ev.Publish("sstv", "log", map[string]string{"line": "receiving " + md.Name})
		m.ev.Publish("sstv", "progress", map[string]any{"mode": md.Name, "lines": 0, "total_lines": md.Height, "url": url()})
	}
	dec.OnLine = func(rows int) {
		if time.Since(lastProg) < sstvProgressEvery {
			return
		}
		lastProg = time.Now()
		save(dec.Image())
		m.ev.Publish("sstv", "progress", map[string]any{"mode": active.Name, "lines": rows, "total_lines": active.Height, "url": url()})
	}
	dec.OnImage = func(img *image.RGBA, rows int) {
		save(img)
		abs, err := filepath.Abs(file)
		if err != nil {
			abs = file
		}
		msg := map[string]any{"mode": active.Name, "width": active.Width, "height": active.Height, "lines": rows,
			"url": url(), "path": abs, "started": started.Unix(), "duration_s": time.Since(started).Seconds(), "freq_hz": freq}
		m.tagSatellite(msg, freq)
		m.ev.Publish("sstv", "image", msg)
		active = nil
	}
	defer func() {
		if active != nil {
			dec.Finish() // keep the partial picture
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case a, ok := <-pcm:
			if !ok {
				return nil
			}
			dec.Feed(a)
		}
	}
}

// uniqueName returns dir/base+ext, with -2, -3... added if the file exists.
func uniqueName(dir, base, ext string) string {
	p := filepath.Join(dir, base+ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); err != nil {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
	}
}
