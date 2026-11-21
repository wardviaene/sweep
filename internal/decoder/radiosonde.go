package decoder

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
)

// sondeDecoders run in parallel on the same FM audio; whichever matches the
// sonde type produces JSON frames.
var sondeDecoders = [][]string{
	{"rs41mod", "--ecc2", "--ptu", "--json"},
	{"dfm09mod", "--ecc", "--ptu", "--json"},
	{"m10mod", "--ptu", "--json"},
	{"lms6Xmod", "--ecc", "--json"},
}

// findSonde scans 400-406 MHz and returns the strongest non-spur carrier.
func (m *Manager) findSonde(ctx context.Context) (uint32, error) {
	const rate = 2_400_000
	type cand struct {
		f   float64
		snr float64
	}
	var cands []cand
	for _, c := range []uint32{401_000_000, 403_000_000, 405_000_000} {
		if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(c), SampleRate: u32(rate)}); err != nil {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
		db, start, bin, ok := m.r.RecentSpectrum()
		if !ok {
			continue
		}
		for _, p := range dsp.FindPeaks(db, start, bin, 5, 12, 5, 0.1) {
			if !p.Spur && p.FreqHz >= 400e6 && p.FreqHz <= 406e6 {
				cands = append(cands, cand{p.FreqHz, p.SNR})
			}
		}
	}
	if len(cands) == 0 {
		return 0, errors.New("no carrier found in 400-406 MHz; sondes transmit around launch times (00 and 12 UTC) or give freq_hz")
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].snr > cands[j].snr })
	// sonde channels are on 10 kHz steps
	return uint32(math.Round(cands[0].f/10e3) * 10e3), nil
}

// runRadiosonde demodulates FM at freq (or the strongest carrier in 400-406
// MHz) and feeds the audio to the rs1729 decoders.
func (m *Manager) runRadiosonde(ctx context.Context, st *Status, freq uint32) error {
	var bins [][]string
	for _, d := range sondeDecoders {
		p, err := lookTool(d[0])
		if err != nil {
			continue
		}
		bins = append(bins, append([]string{p}, d[1:]...))
	}
	if len(bins) == 0 {
		return errors.New("no radiosonde decoders found (rs41mod, dfm09mod, ...); run scripts/install-decoders.sh")
	}
	if freq == 0 {
		f, err := m.findSonde(ctx)
		if err != nil {
			return err
		}
		freq = f
		m.ev.Publish("radiosonde", "log", map[string]string{"line": fmt.Sprintf("strongest carrier: %.3f MHz", float64(f)/1e6)})
	}
	if _, err := m.r.Tune(radio.TuneRequest{FreqHz: u32(freq - 600_000), SampleRate: u32(2_400_000)}); err != nil {
		return err
	}
	m.setFreq(st, freq)
	id, pcm, rate := m.r.AddAudioTap(freq, dsp.ModeNFM, 15000, true)
	defer m.r.RemoveTap(id)

	var stdins []io.WriteCloser
	var wg sync.WaitGroup
	errc := make(chan error, len(bins))
	for _, b := range bins {
		cmd := exec.CommandContext(ctx, b[0], b[1:]...)
		in, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start %s: %w", b[0], err)
		}
		io.Copy(in, bytesReader(wavHeader(rate)))
		stdins = append(stdins, in)
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if !strings.HasPrefix(line, "{") {
					continue
				}
				var obj map[string]any
				if json.Unmarshal([]byte(line), &obj) == nil {
					obj["freq_hz"] = freq
					m.ev.Publish("radiosonde", "message", obj)
				}
			}
			errc <- cmd.Wait()
		}(b[0])
	}
	// fan the audio out to every decoder
	buf := make([]byte, 0, 8192)
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case a, ok := <-pcm:
			if !ok {
				break loop
			}
			buf = buf[:0]
			for _, v := range a {
				buf = binary.LittleEndian.AppendUint16(buf, uint16(v))
			}
			for _, in := range stdins {
				in.Write(buf)
			}
		case err := <-errc:
			if ctx.Err() == nil {
				return fmt.Errorf("radiosonde decoder exited: %v", err)
			}
			break loop
		}
	}
	for _, in := range stdins {
		in.Close()
	}
	wg.Wait()
	return nil
}

// wavHeader is a streaming 16-bit mono WAV header (sizes set to the maximum).
func wavHeader(rate int) []byte {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 0xFFFFFFFF-8)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], 0xFFFFFFFF-44)
	return h
}

type byteReader struct {
	b []byte
}

func bytesReader(b []byte) io.Reader { return &byteReader{b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
