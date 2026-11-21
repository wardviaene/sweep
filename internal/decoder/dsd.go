package decoder

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
)

// Digital voice (DMR, D-STAR, YSF, NXDN, P25 phase 1) for amateur repeaters
// via dsd-fme. dsd-fme needs 48 kHz discriminator audio, so the radio runs at
// 2.4 MS/s (2.4M / 50 = 48k). Decoded voice comes back over UDP at 8 kHz and
// goes to the listening audio stream.

var (
	dsdSync = regexp.MustCompile(`Sync:\s*[+-]?([A-Z0-9-]+(?: [A-Z0-9]+)?)`)
	dsdTGT  = regexp.MustCompile(`\b(?:TGT=|Target: )(\d+)`)
	dsdSRC  = regexp.MustCompile(`\b(?:SRC=|Source: )(\d+)`)
	dsdCC   = regexp.MustCompile(`Color Code[=: ]+(\d+)`)
	ansi    = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

// parseDSDLine extracts call metadata from a dsd-fme console line.
func parseDSDLine(line string) (map[string]any, bool) {
	line = strings.TrimSpace(ansi.ReplaceAllString(line, ""))
	sync := dsdSync.FindStringSubmatch(line)
	tgt := dsdTGT.FindStringSubmatch(line)
	src := dsdSRC.FindStringSubmatch(line)
	if sync == nil && tgt == nil && src == nil {
		return nil, false
	}
	out := map[string]any{"line": line}
	if sync != nil {
		out["mode"] = strings.TrimSpace(sync[1])
	}
	if tgt != nil {
		out["tgt"], _ = strconv.Atoi(tgt[1])
	}
	if src != nil {
		out["src"], _ = strconv.Atoi(src[1])
	}
	if cc := dsdCC.FindStringSubmatch(line); cc != nil {
		out["color_code"], _ = strconv.Atoi(cc[1])
	}
	if strings.Contains(line, "ENC") || strings.Contains(strings.ToLower(line), "encrypt") {
		out["encrypted"] = true // dsd-fme mutes these; sweep never decrypts
	}
	return out, true
}

func (m *Manager) runDSD(ctx context.Context, st *Status, freq uint32) error {
	bin, err := lookTool("dsd-fme")
	if err != nil {
		return err
	}
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(freq - 600_000), SampleRate: u32(2_400_000)}); err != nil {
		return err
	}
	m.setFreq(st, freq)
	off := "off"
	m.r.SetDemod(radio.DemodRequest{Mode: &off}) // the voice stream carries dsd audio instead
	id, pcm, rate := m.r.AddAudioTap(freq, dsp.ModeNFM, 12500, true)
	defer m.r.RemoveTap(id)
	if rate != 48000 {
		return fmt.Errorf("dsd needs 48 kHz audio, got %d", rate)
	}

	// voice output: dsd-fme's UDP "socket blaster" (8 kHz, s16le, stereo in auto mode)
	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer uc.Close()
	port := uc.LocalAddr().(*net.UDPAddr).Port
	if m.Audio != nil {
		m.Audio.AudioMeta(8000, "dsd")
		defer m.Audio.AudioMeta(0, "off")
		go func() {
			b := make([]byte, 65536)
			for {
				n, _, err := uc.ReadFromUDP(b)
				if err != nil {
					return
				}
				// stereo (two TDMA slots) -> mono by summing
				out := make([]int16, 0, n/4)
				for i := 0; i+3 < n; i += 4 {
					l := int32(int16(binary.LittleEndian.Uint16(b[i:])))
					r := int32(int16(binary.LittleEndian.Uint16(b[i+2:])))
					s := l + r
					if s > 32767 {
						s = 32767
					} else if s < -32768 {
						s = -32768
					}
					out = append(out, int16(s))
				}
				m.Audio.Audio(out)
			}
		}()
	}

	cmd := exec.CommandContext(ctx, bin, "-fa", "-i", "-", "-o", "udp:127.0.0.1:"+strconv.Itoa(port))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dsd-fme: %w", err)
	}
	go pumpPCM(ctx, pcm, stdin)

	last := map[string]time.Time{}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		ev, ok := parseDSDLine(sc.Text())
		if !ok {
			continue
		}
		// one event per call: dedupe repeated frames of the same call for 3 s
		key := fmt.Sprint(ev["mode"], ev["tgt"], ev["src"])
		if t, seen := last[key]; seen && time.Since(t) < 3*time.Second {
			last[key] = time.Now()
			continue
		}
		last[key] = time.Now()
		m.ev.Publish("dsd", "message", ev)
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("dsd-fme exited")
	}
	return err
}
