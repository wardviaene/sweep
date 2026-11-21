package radio

import (
	"fmt"
	"math"
	"time"

	"github.com/wardviaene/sweep/internal/dsp"
)

// hfLimit: below this center frequency the R820T can't tune, so sdrd uses direct sampling.
const hfLimit = 24_000_000

// DemodInfo is the listening demodulator state (status.demod).
type DemodInfo struct {
	Mode        string   `json:"mode"`
	FreqHz      uint32   `json:"freq_hz"`
	BandwidthHz float64  `json:"bandwidth_hz"`
	SquelchDB   *float64 `json:"squelch_db"`
	AudioRate   int      `json:"audio_rate"`
	LevelDB     float64  `json:"level_db"`
}

// AudioSink receives listening audio.
type AudioSink interface {
	AudioMeta(rate int, mode string)
	Audio(pcm []int16)
	Level(db float64, open bool)
}

// DemodRequest is the POST /api/demod body. Nil fields are unchanged;
// SetSquelch with nil SquelchDB disables squelch.
type DemodRequest struct {
	Mode        *string
	FreqHz      *uint32
	BandwidthHz *float64
	SetSquelch  bool
	SquelchDB   *float64
}

// SetDemod changes the listening demodulator. A frequency outside the middle
// 80% of the span retunes the center to freq - sample_rate/4.
func (r *Radio) SetDemod(req DemodRequest) (Status, error) {
	r.mu.Lock()
	dm := r.dm
	rebuild := false
	if req.Mode != nil {
		if !dsp.ValidMode(*req.Mode) {
			r.mu.Unlock()
			return Status{}, fmt.Errorf("mode %q: want off, nfm, am, usb or lsb", *req.Mode)
		}
		if *req.Mode != dm.Mode && req.BandwidthHz == nil {
			dm.BandwidthHz = dsp.DefaultBandwidth(dsp.Mode(*req.Mode))
		}
		rebuild = rebuild || dm.Mode != *req.Mode
		dm.Mode = *req.Mode
	}
	if req.BandwidthHz != nil {
		if *req.BandwidthHz < 500 || *req.BandwidthHz > 200000 {
			r.mu.Unlock()
			return Status{}, fmt.Errorf("bandwidth_hz must be 500..200000")
		}
		rebuild = rebuild || dm.BandwidthHz != *req.BandwidthHz
		dm.BandwidthHz = *req.BandwidthHz
	}
	if req.SetSquelch {
		dm.SquelchDB = req.SquelchDB
	}
	if dm.FreqHz == 0 {
		dm.FreqHz = r.st.CenterHz + r.st.SampleRate/4
	}
	var retune *uint32
	if req.FreqHz != nil {
		dm.FreqHz = *req.FreqHz
		if !r.inSpanLocked(dm.FreqHz) {
			c := dm.FreqHz - r.st.SampleRate/4
			retune = &c
		}
	}
	r.dm = dm
	if !rebuild && retune == nil && r.demod != nil && dm.Mode != "off" && dm.Mode != "" {
		// Only the frequency moved inside the span: shift the channel without
		// resetting the filters (Doppler tracking calls this every second).
		r.demod.SetOffset(float64(dm.FreqHz) - float64(r.st.CenterHz))
	} else {
		r.rebuildDemodLocked()
	}
	r.mu.Unlock()
	if retune != nil {
		if _, err := r.Tune(TuneRequest{FreqHz: retune}); err != nil {
			return Status{}, err
		}
	}
	return r.Status(), nil
}

func (r *Radio) inSpanLocked(f uint32) bool {
	off := float64(f) - float64(r.st.CenterHz)
	return math.Abs(off) < 0.4*float64(r.st.SampleRate)
}

// retuneDemodLocked keeps the VFO inside the span after a center change and
// rebuilds the demodulator (rate changes need new filters).
func (r *Radio) retuneDemodLocked(rateChanged bool) {
	for _, t := range r.taps {
		if t.demod != nil {
			t.rebuild(r)
		}
	}
	if r.dm.Mode == "off" || r.dm.Mode == "" {
		return
	}
	if !r.inSpanLocked(r.dm.FreqHz) {
		r.dm.FreqHz = r.st.CenterHz + r.st.SampleRate/4
	}
	if rateChanged || r.demod == nil {
		r.rebuildDemodLocked()
		return
	}
	r.demod.SetOffset(float64(r.dm.FreqHz) - float64(r.st.CenterHz))
}

func (r *Radio) rebuildDemodLocked() {
	if r.dm.Mode == "off" || r.dm.Mode == "" {
		r.demod = nil
		r.dm.AudioRate = 0
		if r.audio != nil {
			r.audio.AudioMeta(0, "off")
		}
		return
	}
	d := dsp.NewDemod(dsp.Mode(r.dm.Mode), float64(r.st.SampleRate),
		float64(r.dm.FreqHz)-float64(r.st.CenterHz), r.dm.BandwidthHz)
	r.demod = d
	r.dm.AudioRate = int(d.Rate())
	if r.audio != nil {
		r.audio.AudioMeta(r.dm.AudioRate, r.dm.Mode)
	}
}

// processLocked feeds a block of IQ to the listening demod and the taps.
func (r *Radio) processLocked(b []byte) {
	if len(b) == 0 {
		return
	}
	if r.demod != nil {
		r.audioBuf = r.demod.Process(b, r.audioBuf[:0])
		lv := r.demod.Level()
		r.dm.LevelDB = math.Round(lv*10) / 10
		open := r.dm.SquelchDB == nil || lv >= *r.dm.SquelchDB
		if !open {
			clear(r.audioBuf)
		}
		if r.audio != nil {
			if len(r.audioBuf) > 0 {
				r.audio.Audio(append([]int16(nil), r.audioBuf...))
			}
			if time.Since(r.lastLevel) >= 100*time.Millisecond {
				r.lastLevel = time.Now()
				r.audio.Level(r.dm.LevelDB, open)
			}
		}
	}
	for _, t := range r.taps {
		t.feed(b)
	}
}

// tap is a decoder's view of the IQ stream: raw IQ, or audio from its own demod.
type tap struct {
	iq     chan []byte  // raw cu8 IQ copies
	pcm    chan []int16 // audio from demod
	demod  *dsp.Demod
	freqHz uint32
	mode   dsp.Mode
	bw     float64
	raw    bool
	drops  int
}

func (t *tap) rebuild(r *Radio) {
	t.demod = dsp.NewDemod(t.mode, float64(r.st.SampleRate), float64(t.freqHz)-float64(r.st.CenterHz), t.bw)
	t.demod.Raw = t.raw
}

func (t *tap) feed(b []byte) {
	if t.demod != nil {
		pcm := t.demod.Process(b, nil)
		select {
		case t.pcm <- pcm:
		default:
			t.drops++
		}
		return
	}
	select {
	case t.iq <- append([]byte(nil), b...):
	default:
		t.drops++
	}
}

// AddIQTap subscribes to raw IQ. The channel is buffered; blocks are dropped
// when the consumer falls behind.
func (r *Radio) AddIQTap() (id int, iq <-chan []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextTap++
	t := &tap{iq: make(chan []byte, 64)}
	r.taps[r.nextTap] = t
	return r.nextTap, t.iq
}

// AddAudioTap runs a private demodulator at freqHz and delivers its audio.
// bw 0 uses the mode default; raw skips the NFM audio filter (data modems).
// It returns the audio rate.
func (r *Radio) AddAudioTap(freqHz uint32, mode dsp.Mode, bw float64, raw bool) (id int, pcm <-chan []int16, rate int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextTap++
	t := &tap{pcm: make(chan []int16, 64), freqHz: freqHz, mode: mode, bw: bw, raw: raw}
	t.rebuild(r)
	r.taps[r.nextTap] = t
	return r.nextTap, t.pcm, int(t.demod.Rate())
}

// SetTapFreq moves an audio tap's channel to freqHz without restarting it:
// the filters keep their state and the audio channel stays open (Doppler
// tracking). The frequency must lie inside the current span.
func (r *Radio) SetTapFreq(id int, freqHz uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.taps[id]
	if !ok {
		return fmt.Errorf("tap %d: not found", id)
	}
	if t.demod == nil {
		return fmt.Errorf("tap %d: raw IQ tap has no channel to retune", id)
	}
	if !r.inSpanLocked(freqHz) {
		return fmt.Errorf("tap %d: %d Hz is outside the %d Hz span at %d Hz", id, freqHz, r.st.SampleRate, r.st.CenterHz)
	}
	t.freqHz = freqHz
	t.demod.SetOffset(float64(freqHz) - float64(r.st.CenterHz))
	return nil
}

// RemoveTap unsubscribes a tap and closes its channel.
func (r *Radio) RemoveTap(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.taps[id]; ok {
		delete(r.taps, id)
		if t.iq != nil {
			close(t.iq)
		}
		if t.pcm != nil {
			close(t.pcm)
		}
	}
}

// SetDecoderStatus registers a function reporting decoder state in /api/status.
func (r *Radio) SetDecoderStatus(fn func() any) {
	r.mu.Lock()
	r.decoderFn = fn
	r.mu.Unlock()
}
