package decoder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wardviaene/sweep/internal/radio"
)

// UAT 978 needs exactly 2.083334 MS/s (dump978's expected rate).
const uatRate = 2_083_334

// runUAT pipes IQ at 978 MHz into dump978, splits its output into downlink
// frames (aircraft, via uat2json) and uplink frames (FIS-B, via uat2text).
func (m *Manager) runUAT(ctx context.Context, st *Status) error {
	dump, err := lookTool("dump978")
	if err != nil {
		return err
	}
	toJSON, err := lookTool("uat2json")
	if err != nil {
		return err
	}
	toText, err := lookTool("uat2text")
	if err != nil {
		return err
	}
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(978_000_000), SampleRate: u32(uatRate)}); err != nil {
		return err
	}
	m.setFreq(st, 978_000_000)
	dir, cleanup, err := tempDir("uat")
	if err != nil {
		return err
	}
	defer cleanup()

	jcmd := exec.CommandContext(ctx, toJSON, dir)
	jin, err := jcmd.StdinPipe()
	if err != nil {
		return err
	}
	tcmd := exec.CommandContext(ctx, toText)
	tin, err := tcmd.StdinPipe()
	if err != nil {
		return err
	}
	tout, err := tcmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := jcmd.Start(); err != nil {
		return fmt.Errorf("start uat2json: %w", err)
	}
	defer jcmd.Wait()
	if err := tcmd.Start(); err != nil {
		return fmt.Errorf("start uat2text: %w", err)
	}
	defer tcmd.Wait()
	go m.readFISB(tout)

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go m.pollUATAircraft(cctx, filepath.Join(dir, "aircraft.json"))

	cmd := exec.CommandContext(ctx, dump)
	defer jin.Close()
	defer tin.Close()
	return m.runPiped(ctx, "uat", cmd, func(line string) {
		switch {
		case strings.HasPrefix(line, "-"):
			io.WriteString(jin, line+"\n")
		case strings.HasPrefix(line, "+"):
			io.WriteString(tin, line+"\n")
		}
	}, func(l string) bool { return strings.Contains(strings.ToLower(l), "error") })
}

// pollUATAircraft reads uat2json's aircraft.json once a second and publishes
// it in the same shape as the adsb snapshot.
func (m *Manager) pollUATAircraft(ctx context.Context, path string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Aircraft []map[string]any `json:"aircraft"`
		}
		if json.Unmarshal(b, &doc) != nil {
			continue
		}
		out := []map[string]any{}
		for _, a := range doc.Aircraft {
			out = append(out, normUATAircraft(a))
		}
		m.ev.Publish("uat", "snapshot", map[string]any{"aircraft": out})
	}
}

// normUATAircraft maps uat2json fields onto the adsb snapshot names.
func normUATAircraft(a map[string]any) map[string]any {
	o := map[string]any{}
	ren := map[string]string{"hex": "hex", "flight": "flight", "lat": "lat", "lon": "lon", "track": "track",
		"speed": "gs", "altitude": "alt_baro", "seen": "seen", "messages": "messages", "squawk": "squawk",
		"vert_rate": "vert_rate", "addrtype": "addrtype"}
	for k, v := range a {
		if n, ok := ren[k]; ok {
			o[n] = v
		}
	}
	if s, ok := o["flight"].(string); ok {
		o["flight"] = strings.TrimSpace(s)
	}
	if h, ok := o["hex"].(string); ok {
		o["hex"] = strings.ToLower(strings.TrimPrefix(h, "~"))
	}
	return o
}

// readFISB parses uat2text output and publishes FIS-B text reports
// (METAR, TAF, winds aloft, NOTAMs ...).
func (m *Manager) readFISB(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var rep map[string]any
	var text []string
	inText := false
	flush := func() {
		if rep != nil && len(text) > 0 {
			rep["text"] = strings.TrimSpace(strings.Join(text, "\n"))
			m.ev.Publish("uat", "message", rep)
		}
		rep, text, inText = nil, nil, false
	}
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		k, v, hasKV := strings.Cut(t, ":")
		v = strings.TrimSpace(v)
		switch {
		case inText && t != "":
			// the report body is printed unindented, up to a blank line
			text = append(text, t)
		case t == "" || !strings.HasPrefix(line, " "):
			flush()
		case hasKV && k == "Product ID":
			flush()
			rep = map[string]any{"kind": "fisb", "product": v}
		case rep != nil && hasKV && k == "Report type":
			rep["report_type"] = v
		case rep != nil && hasKV && k == "Report location":
			rep["location"] = v
		case rep != nil && hasKV && k == "Report time":
			rep["time"] = v
		case rep != nil && k == "Text" && v == "":
			inText = true
		}
	}
	flush()
}
