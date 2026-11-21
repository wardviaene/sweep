package autopilot

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/dsp"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/scan"
	"github.com/wardviaene/sweep/internal/source"
)

// prior is the radio state the autopilot restores after the pass.
type prior struct {
	st      radio.Status
	decName string
	decOpt  decoder.Options
	decRun  bool
}

// curPass is the pass being handled. The counters are guarded by Autopilot.mu.
type curPass struct {
	pl     Planned
	ap     config.Autopilot
	manual bool
	cancel context.CancelFunc
	done   chan struct{}
	prior  prior

	dir     string
	packets int
	images  int
	doppler int
	elev    float64
	az      float64
}

func (c *curPass) current() Current {
	p := c.pl.Pass
	return Current{Norad: p.Norad, Name: p.Name, AOS: p.AOS, LOS: p.LOS, MaxElev: p.MaxElevDeg,
		DownlinkHz: c.pl.Choice.DownlinkHz, Decoder: c.pl.Choice.Decoder(), DopplerHz: c.doppler,
		ElevDeg: c.elev, AzDeg: c.az, Packets: c.packets, Images: c.images, Dir: c.dir}
}

// runPass drives one pass from acquisition to restoring the radio. The caller
// (begin) holds scan.Busy and set a.cur.
func (a *Autopilot) runPass(ctx context.Context, c *curPass) {
	p, ch := c.pl.Pass, c.pl.Choice
	started := a.now()
	t := time.Unix(p.AOS, 0)
	if c.manual {
		t = started
	}
	e := Entry{Norad: p.Norad, Name: p.Name, AOS: p.AOS, LOS: p.LOS, MaxElev: p.MaxElevDeg, DownlinkHz: ch.DownlinkHz,
		Decoder: ch.Decoder(), State: "running", Started: float64(started.UnixMilli()) / 1e3, Manual: c.manual}
	if ch.Kind == KindPacket {
		e.Baud = ch.Baud
	}
	a.logf("AOS %s, %.3f MHz %s", p.Name, float64(ch.DownlinkHz)/1e6, ch.Decoder())

	var state, reason string
	dir, err := makePassDir(a.passesDir(), t, p.Norad, p.Name)
	if err != nil {
		state, reason = "error", "pass directory: "+err.Error()
	} else {
		e.Dir = dir
		a.mu.Lock()
		c.dir = dir
		a.mu.Unlock()
		if err := writePassJSON(a.passesDir(), e); err != nil {
			a.logf("pass.json: %v", err)
		}
		state, reason = a.track(ctx, c, &e)
	}

	// restore the radio, then publish the result
	if rerr := a.restore(c.prior); rerr != nil && reason == "" {
		reason = rerr.Error()
	}
	scan.Busy.Unlock()

	e.State, e.Reason = state, reason
	e.Ended = float64(a.now().UnixMilli()) / 1e3
	a.mu.Lock()
	e.Packets, e.Images = c.packets, c.images
	a.mu.Unlock()
	if e.Dir != "" {
		if err := writePassJSON(a.passesDir(), e); err != nil {
			a.logf("pass.json: %v", err)
		}
	}
	a.mu.Lock()
	a.history = append([]Entry{e}, a.history...)
	if len(a.history) > historyLen {
		a.history = a.history[:historyLen]
	}
	a.cur = nil
	a.mu.Unlock()

	switch ch.Kind {
	case KindSSTV:
		a.logf("LOS %s: %s, %s", p.Name, plural(e.Images, "image"), plural(e.Packets, "packet"))
	case KindFM:
		a.logf("LOS %s: recorded %s", p.Name, e.Audio)
	default:
		a.logf("LOS %s: %s, %s", p.Name, plural(e.Images, "image"), plural(e.Packets, "packet"))
	}
	if state != "done" && reason != "" {
		a.logf("%s %s: %s", p.Name, state, reason)
	}
	a.d.Decoders.Events().Publish("autopilot", "pass", e)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// restore puts back the center, rate, gain, VFO and decoder from before the pass.
func (a *Autopilot) restore(p prior) error {
	d := a.d
	d.Decoders.Stop()
	var errs []error
	if _, err := d.Radio.Tune(radio.TuneRequest{FreqHz: &p.st.CenterHz, SampleRate: &p.st.SampleRate, SetGain: true, GainDB: p.st.GainDB}); err != nil {
		errs = append(errs, fmt.Errorf("restore tuning: %w", err))
	}
	dm := p.st.Demod
	req := radio.DemodRequest{Mode: &dm.Mode, SetSquelch: true, SquelchDB: dm.SquelchDB}
	if dm.Mode != "off" {
		req.FreqHz = &dm.FreqHz
		if dm.BandwidthHz > 0 {
			req.BandwidthHz = &dm.BandwidthHz
		}
	}
	if _, err := d.Radio.SetDemod(req); err != nil {
		errs = append(errs, fmt.Errorf("restore demod: %w", err))
	}
	name := "none"
	if p.decRun {
		name = p.decName
	}
	if err := d.Decoders.Start(name, p.decOpt); err != nil {
		errs = append(errs, fmt.Errorf("restart %s: %w", name, err))
	}
	if len(errs) == 0 {
		return nil
	}
	err := fmt.Errorf("%v", errs)
	a.logf("restore: %v", err)
	return err
}

// track tunes, starts the decoder or recorders and follows the satellite
// until LOS+tail or ctx ends. It leaves the radio tuned; the caller restores.
func (a *Autopilot) track(ctx context.Context, c *curPass, e *Entry) (state, reason string) {
	d := a.d
	p, ch := c.pl.Pass, c.pl.Choice
	nominal := float64(ch.DownlinkHz)
	passDir := filepath.Join(a.passesDir(), e.Dir)

	d.Decoders.Stop() // a decoder that was running (preempt)
	sr := d.Radio.Status().SampleRate
	center := uint32(ch.DownlinkHz) - sr/4
	if _, err := d.Radio.Tune(radio.TuneRequest{FreqHz: &center}); err != nil {
		return "error", "tune: " + err.Error()
	}

	// VFO: follows the downlink when listening, silent otherwise
	mode := "nfm"
	if c.ap.Listen == nil || *c.ap.Listen {
		f := uint32(ch.DownlinkHz)
		if _, err := d.Radio.SetDemod(radio.DemodRequest{Mode: &mode, FreqHz: &f, SetSquelch: true}); err != nil {
			a.logf("vfo: %v", err)
		}
	} else {
		off := "off"
		if _, err := d.Radio.SetDemod(radio.DemodRequest{Mode: &off}); err != nil {
			a.logf("vfo off: %v", err)
		}
	}
	listening := c.ap.Listen == nil || *c.ap.Listen

	evCh, cancelSub := d.Decoders.Events().Subscribe(0)
	defer cancelSub()

	var (
		wg        sync.WaitGroup
		tapID     int
		decoding  bool
		packets   *os.File
		wav       *wavFile
		iqFile    *os.File
		iqTap     int
		decErr    string
		retuneErr bool
	)
	defer func() {
		if tapID != 0 {
			d.Radio.RemoveTap(tapID)
		}
		if iqTap != 0 {
			d.Radio.RemoveTap(iqTap)
		}
		wg.Wait()
		if wav != nil {
			if err := wav.close(); err != nil {
				a.logf("wav: %v", err)
			}
		}
		if iqFile != nil {
			iqFile.Close()
		}
		if packets != nil {
			packets.Close()
		}
	}()

	switch ch.Kind {
	case KindPacket, KindSSTV:
		opt := decoder.Options{FreqHz: uint32(ch.DownlinkHz)}
		if ch.Kind == KindPacket {
			opt.Baud = ch.Baud
		}
		if err := d.Decoders.Start(ch.Kind, opt); err != nil {
			return "error", fmt.Sprintf("start %s: %v", ch.Kind, err)
		}
		decoding = true
	case KindFM:
		id, audio, rate := d.Radio.AddAudioTap(uint32(ch.DownlinkHz), dsp.ModeNFM, 0, false)
		tapID = id
		w, err := newWAV(filepath.Join(passDir, "pass.wav"), rate)
		if err != nil {
			return "error", "wav: " + err.Error()
		}
		wav = w
		a.mu.Lock()
		e.Audio = "pass.wav"
		a.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pcm := range audio {
				if err := w.write(pcm); err != nil {
					a.logf("wav: %v", err)
					for range audio { // drain until the tap is removed
					}
					return
				}
			}
		}()
	}
	if c.ap.RecordIQ {
		name := source.RecordingName(a.now(), center, sr)
		f, err := os.Create(filepath.Join(passDir, name))
		if err != nil {
			a.logf("iq: %v", err)
		} else {
			iqFile = f
			e.IQ = name
			id, iq := d.Radio.AddIQTap()
			iqTap = id
			wg.Add(1)
			go func() {
				defer wg.Done()
				for b := range iq {
					if _, err := f.Write(b); err != nil {
						return
					}
				}
			}()
		}
	}
	if err := writePassJSON(a.passesDir(), *e); err != nil {
		a.logf("pass.json: %v", err)
	}
	if ch.Kind == KindPacket {
		if f, err := os.OpenFile(filepath.Join(passDir, "packets.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			packets = f
		}
	}

	handle := func(b []byte) {
		var ev decoder.Event
		if json.Unmarshal(b, &ev) != nil || ev.Decoder != ch.Kind {
			return
		}
		switch {
		case ch.Kind == KindPacket && ev.Kind == "message":
			a.mu.Lock()
			c.packets++
			a.mu.Unlock()
			if packets != nil {
				_, _ = packets.Write(append(append([]byte(nil), b...), '\n'))
			}
		case ch.Kind == KindSSTV && ev.Kind == "image":
			a.mu.Lock()
			c.images++
			n := c.images
			a.mu.Unlock()
			if m, ok := ev.Data.(map[string]any); ok {
				if src, _ := m["path"].(string); src != "" {
					if err := copyFile(src, filepath.Join(passDir, fmt.Sprintf("image-%02d%s", n, filepath.Ext(src)))); err != nil {
						a.logf("copy image: %v", err)
					}
				}
			}
		}
	}

	end := time.Unix(p.LOS, 0).Add(d.Tail)
	tk := time.NewTicker(d.Tick)
	defer tk.Stop()
	var lastDop float64
	retune := func() {
		dop, el, az, visible := a.lookup(p.Norad, ch.DownlinkHz)
		if visible {
			lastDop = dop
		}
		target := uint32(math.Round(nominal + lastDop))
		var errs []error
		if decoding {
			if st := d.Decoders.Status(); !st.Running {
				if st.Error != "" && decErr == "" {
					decErr = "decoder stopped: " + st.Error
					a.logf("%s", decErr)
				}
			} else if err := d.Decoders.RetuneChannel(target); err != nil {
				errs = append(errs, err)
			}
		}
		if tapID != 0 {
			if err := d.Radio.SetTapFreq(tapID, target); err != nil {
				errs = append(errs, err)
			}
		}
		if listening {
			f := target
			if _, err := d.Radio.SetDemod(radio.DemodRequest{FreqHz: &f}); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 && !retuneErr {
			retuneErr = true
			a.logf("retune %s: %v", p.Name, errs)
		}
		a.mu.Lock()
		c.doppler = int(math.Round(lastDop))
		if visible {
			c.elev, c.az = el, az
		}
		a.mu.Unlock()
	}
	retune()
	for {
		select {
		case <-ctx.Done():
			state = "stopped"
		case b := <-evCh:
			handle(b)
			continue
		case <-tk.C:
			if !a.now().Before(end) {
				state = "done"
				break
			}
			retune()
			continue
		}
		break
	}
	if decoding {
		d.Decoders.Stop()
	}
	for drained := false; !drained; { // events published just before the stop
		select {
		case b := <-evCh:
			handle(b)
		default:
			drained = true
		}
	}
	if decErr != "" {
		return "error", decErr
	}
	return state, ""
}

// lookup returns the Doppler shift (Hz, at the downlink), elevation and azimuth
// of satellite norad now, from the metadata. visible is false when the
// satellite is not above the horizon (or unknown).
func (a *Autopilot) lookup(norad int, downlinkHz int64) (dop, el, az float64, visible bool) {
	if a.d.Source == nil {
		return 0, 0, 0, false
	}
	sats, ok := a.d.Source.Overhead()
	if !ok {
		return 0, 0, 0, false
	}
	for _, s := range sats {
		if s.Norad != norad {
			continue
		}
		best := -1
		for i, dl := range s.Downlinks {
			if dl.DopplerHz != nil && (best < 0 || abs64(dl.FreqHz-downlinkHz) < abs64(s.Downlinks[best].FreqHz-downlinkHz)) {
				best = i
			}
		}
		if best >= 0 && s.Downlinks[best].FreqHz != 0 {
			dl := s.Downlinks[best]
			// the shift scales with the carrier frequency
			dop = float64(*dl.DopplerHz) * float64(downlinkHz) / float64(dl.FreqHz)
		}
		return dop, s.ElevationD, s.AzimuthD, true
	}
	return 0, 0, 0, false
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
