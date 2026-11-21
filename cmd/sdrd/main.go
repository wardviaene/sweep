// sdrd owns the RTL-SDR (through an rtl_tcp subprocess) and serves spectrum
// frames and controls over HTTP and WebSocket. See docs/protocol.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/server"
	"github.com/wardviaene/sweep/internal/source"
)

func main() {
	var cfg radio.Config
	addr := flag.String("addr", "127.0.0.1:8073", "HTTP listen address")
	web := flag.String("web", "web/dist", "directory with the built web UI (served at /), empty to disable")
	freq := flag.Float64("freq", 433.92e6, "initial center frequency in Hz")
	rate := flag.Uint("rate", 2_048_000, "initial sample rate")
	gain := flag.String("gain", "29.7", "initial tuner gain in dB, or auto (auto lifts the noise floor on the R820T)")
	cfgPath := flag.String("config", config.DefaultPath(), "local config file (receiver location); keep it out of the repo")
	recDir := flag.String("record-dir", "recordings", "directory for IQ recordings (POST /api/record)")
	iqFreq := flag.Float64("iq-freq", 0, "with -iq: center frequency of the recording in Hz (default: from the file name)")
	flag.StringVar(&cfg.File, "iq", "", "play back a cu8 recording (looping) instead of using the dongle")
	flag.BoolVar(&cfg.Synth, "synth", false, "use a synthetic IQ source instead of the dongle")
	var synthFM []string
	flag.Func("synth-fm", "with -synth: FREQ:file.wav, loop a 16-bit WAV as FM at FREQ (e.g. 144.39M:aprs.wav); repeatable", func(v string) error {
		synthFM = append(synthFM, v)
		return nil
	})
	flag.StringVar(&cfg.RtlTcpBin, "rtl-tcp", "rtl_tcp", "rtl_tcp binary")
	flag.IntVar(&cfg.RtlTcpPort, "rtl-tcp-port", 1235, "port for the private rtl_tcp instance")
	flag.IntVar(&cfg.FFTSize, "fft", 2048, "FFT size (bins per frame)")
	flag.IntVar(&cfg.FPS, "fps", 20, "spectrum frames per second")
	sbsPort := flag.Int("sbs-port", 30903, "private 127.0.0.1 port for dump1090's SBS output (ADS-B decoder)")
	direct := flag.Uint("direct-branch", 2, "direct-sampling branch for HF below 24 MHz: 1 = I, 2 = Q")
	flag.Float64Var(&cfg.MinDB, "min-db", -90, "dB mapped to 0 in frames")
	flag.Float64Var(&cfg.MaxDB, "max-db", -20, "dB mapped to 255 in frames")
	flag.Parse()
	cfg.CenterHz, cfg.SampleRate = uint32(*freq), uint32(*rate)
	cfg.DirectQ = uint32(*direct)
	if cfg.File != "" {
		f, sr, err := source.ParseRecordingName(cfg.File)
		if *iqFreq > 0 {
			f, sr, err = uint32(*iqFreq), cfg.SampleRate, nil
		}
		if err != nil {
			log.Fatal(err)
		}
		cfg.CenterHz, cfg.SampleRate = f, sr
	}
	for _, v := range synthFM {
		f, path, ok := strings.Cut(v, ":")
		if !ok {
			log.Fatalf("-synth-fm %q: want FREQ:file.wav", v)
		}
		hz, err := parseFreq(f)
		if err != nil {
			log.Fatalf("-synth-fm: %v", err)
		}
		pcm, rate, err := source.ReadWAV(path)
		if err != nil {
			log.Fatalf("-synth-fm: %v", err)
		}
		cfg.SynthFM = append(cfg.SynthFM, radio.SynthFM{FreqHz: hz, PCM: pcm, Rate: rate})
	}
	if *gain != "auto" {
		g, err := strconv.ParseFloat(*gain, 64)
		if err != nil {
			log.Fatalf("-gain: %v", err)
		}
		cfg.GainDB = &g
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := server.NewHub()
	audio := server.NewAudioHub()
	r := radio.New(cfg, hub, audio)
	go r.Run(ctx)

	store, err := config.Open(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	events := decoder.NewEvents()
	decoders := decoder.NewManager(r, events)
	decoders.SBSPort = *sbsPort
	decoders.Receiver = func() (float64, float64, bool) {
		if r := store.Get().Receiver; r != nil {
			return r.Lat, r.Lon, true
		}
		return 0, 0, false
	}
	defer decoders.Stop()
	deps := server.Deps{Radio: r, Hub: hub, Audio: audio, Decoders: decoders, Events: events, RecordDir: *recDir, Config: store}
	if *web != "" {
		if st, err := os.Stat(*web); err == nil && st.IsDir() {
			deps.Static = os.DirFS(*web)
		}
	}
	h := server.Handler(deps)
	srv := &http.Server{Addr: *addr, Handler: h}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("sdrd listening on http://%s", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// parseFreq parses 144.39M, 100k, 1.09G or plain Hz.
func parseFreq(s string) (float64, error) {
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "G"):
		mult, s = 1e9, s[:len(s)-1]
	case strings.HasSuffix(s, "M"):
		mult, s = 1e6, s[:len(s)-1]
	case strings.HasSuffix(s, "k"):
		mult, s = 1e3, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("bad frequency %q", s)
	}
	return v * mult, nil
}
