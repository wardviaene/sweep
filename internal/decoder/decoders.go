package decoder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
)

func u32(v uint32) *uint32 { return &v }

// runRTL433 pipes raw IQ into rtl_433 and publishes its JSON lines.
func (m *Manager) runRTL433(ctx context.Context, st *Status) error {
	bin, err := lookTool("rtl_433")
	if err != nil {
		return err
	}
	center := m.r.Status().CenterHz
	inISM := func(f uint32) bool {
		return (f >= 433_050_000 && f <= 434_790_000) || (f >= 863_000_000 && f <= 870_000_000) || (f >= 902_000_000 && f <= 928_000_000)
	}
	if !inISM(center) {
		center = 433_920_000
	}
	const rate = 1_024_000
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(center), SampleRate: u32(rate)}); err != nil {
		return err
	}
	m.setFreq(st, center)

	cmd := exec.CommandContext(ctx, bin, rtl433Args(rate, center)...)
	return m.runPiped(ctx, "rtl_433", cmd, func(line string) {
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) == nil {
			m.ev.Publish("rtl_433", "message", obj)
		}
	}, func(l string) bool {
		return strings.Contains(strings.ToLower(l), "error") || strings.Contains(l, "fail")
	})
}

// rtl433Args reads cu8 IQ from stdin and prints one JSON object per decode.
func rtl433Args(rate int, center uint32) []string {
	return []string{"-r", "cu8:-", "-s", strconv.Itoa(rate), "-f", strconv.Itoa(int(center)),
		"-F", "json", "-M", "level", "-M", "time:unix"}
}

// runPiped starts cmd with an IQ tap on stdin, handles stdout lines with onLine
// and stderr with keep, and waits for exit.
func (m *Manager) runPiped(ctx context.Context, name string, cmd *exec.Cmd, onLine func(string), keep func(string) bool) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	id, iq := m.r.AddIQTap()
	defer m.r.RemoveTap(id)
	go pumpIQ(ctx, iq, stdin)
	go m.logLines(name, stderr, keep)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		if onLine != nil {
			onLine(sc.Text())
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("%s exited", name)
	}
	return err
}

// runADSB pipes IQ at 1090 MHz / 2.4 MS/s into dump1090 and reads its SBS output.
func (m *Manager) runADSB(ctx context.Context, st *Status) error {
	bin, err := lookTool("dump1090", "dump1090-fa")
	if err != nil {
		return err
	}
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(1_090_000_000), SampleRate: u32(2_400_000)}); err != nil {
		return err
	}
	m.setFreq(st, 1_090_000_000)
	port := strconv.Itoa(m.SBSPort)
	cmd := exec.CommandContext(ctx, bin, "--ifile", "-", "--iformat", "UC8", "--quiet",
		"--net", "--net-bind-address", "127.0.0.1", "--net-sbs-port", port,
		"--net-ri-port", "0", "--net-ro-port", "0", "--net-bi-port", "0", "--net-bo-port", "0")
	if m.Receiver != nil {
		if lat, lon, ok := m.Receiver(); ok {
			cmd.Args = append(cmd.Args, "--lat", strconv.FormatFloat(lat, 'f', 5, 64), "--lon", strconv.FormatFloat(lon, 'f', 5, 64))
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go m.readSBS(cctx, "127.0.0.1:"+port)
	return m.runPiped(ctx, "adsb", cmd, nil, func(l string) bool {
		return strings.Contains(strings.ToLower(l), "error") || strings.Contains(l, "fail")
	})
}

// readSBS connects to dump1090's BaseStation port and publishes a snapshot per second.
func (m *Manager) readSBS(ctx context.Context, addr string) {
	var conn net.Conn
	for ctx.Err() == nil {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if conn == nil {
		return
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()

	tbl := newAircraftTable()
	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-lines:
			if !ok {
				return
			}
			tbl.applySBS(l, time.Now())
		case now := <-tick.C:
			m.ev.Publish("adsb", "snapshot", map[string]any{"aircraft": tbl.snapshot(now)})
		}
	}
}

var direwolfLine = regexp.MustCompile(`^\[[^\]]*\]\s+(.*)$`)
var aivdm = regexp.MustCompile(`![A-Z]{2}VD[MO],[^\s<]*`)

// runDirewolf demodulates NFM with a private demod and pipes the audio into
// direwolf (1200 baud APRS or AIS).
func (m *Manager) runDirewolf(ctx context.Context, st *Status, kind, region string) error {
	bin, err := lookTool("direwolf")
	if err != nil {
		return err
	}
	var freq uint32
	var bw float64
	raw := false
	modem := "1200"
	switch kind {
	case "aprs":
		freq, bw = 144_390_000, 12500
		if region == "eu" {
			freq = 144_800_000
		}
	case "ais":
		freq, bw, raw, modem = 161_975_000, 25000, true, "AIS"
	}
	cur := m.r.Status()
	// Put the channel a quarter of the span above center, away from the DC spike.
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(freq - cur.SampleRate/4)}); err != nil {
		return err
	}
	m.setFreq(st, freq)

	dir, cleanup, err := tempDir(kind)
	if err != nil {
		return err
	}
	defer cleanup()
	conf := filepath.Join(dir, "direwolf.conf")
	if err := os.WriteFile(conf, []byte("ADEVICE null null\nCHANNEL 0\nAGWPORT 0\nKISSPORT 0\n"), 0o600); err != nil {
		return err
	}
	id, pcm, rate := m.r.AddAudioTap(freq, dsp.ModeNFM, bw, raw)
	defer m.r.RemoveTap(id)

	cmd := exec.CommandContext(ctx, bin, "-c", conf, "-r", strconv.Itoa(rate), "-n", "1", "-b", "16",
		"-B", modem, "-t", "0", "-q", "hd", "-")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start direwolf: %w", err)
	}
	go pumpPCM(ctx, pcm, stdin)
	ais := newAISAssembler()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if kind == "ais" {
			if s := aivdm.FindString(line); s != "" {
				if msg, ok := ais.add(s); ok {
					m.ev.Publish("ais", "message", msg)
				}
				continue
			}
		}
		if mm := direwolfLine.FindStringSubmatch(line); mm != nil && kind == "aprs" {
			if p, ok := parseAPRS(mm[1]); ok {
				m.ev.Publish("aprs", "message", p)
				continue
			}
		}
		if isDirewolfProblem(line) {
			m.ev.Publish(kind, "log", map[string]string{"line": line})
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("direwolf exited")
	}
	return err
}

func isDirewolfProblem(l string) bool {
	ll := strings.ToLower(l)
	return strings.Contains(ll, "error") || strings.Contains(ll, "fatal")
}
