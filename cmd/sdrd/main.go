// sdrd owns the RTL-SDR (through an rtl_tcp subprocess) and serves spectrum
// frames and controls over HTTP and WebSocket. See docs/protocol.md.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/server"
)

func main() {
	var cfg radio.Config
	addr := flag.String("addr", "127.0.0.1:8073", "HTTP listen address")
	web := flag.String("web", "web/dist", "directory with the built web UI (served at /), empty to disable")
	freq := flag.Float64("freq", 433.92e6, "initial center frequency in Hz")
	rate := flag.Uint("rate", 2_048_000, "initial sample rate")
	gain := flag.String("gain", "29.7", "initial tuner gain in dB, or auto (auto lifts the noise floor on the R820T)")
	flag.BoolVar(&cfg.Synth, "synth", false, "use a synthetic IQ source instead of the dongle")
	flag.StringVar(&cfg.RtlTcpBin, "rtl-tcp", "rtl_tcp", "rtl_tcp binary")
	flag.IntVar(&cfg.RtlTcpPort, "rtl-tcp-port", 1235, "port for the private rtl_tcp instance")
	flag.IntVar(&cfg.FFTSize, "fft", 2048, "FFT size (bins per frame)")
	flag.IntVar(&cfg.FPS, "fps", 20, "spectrum frames per second")
	flag.Float64Var(&cfg.MinDB, "min-db", -90, "dB mapped to 0 in frames")
	flag.Float64Var(&cfg.MaxDB, "max-db", -20, "dB mapped to 255 in frames")
	flag.Parse()
	cfg.CenterHz, cfg.SampleRate = uint32(*freq), uint32(*rate)
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
	r := radio.New(cfg, hub)
	go r.Run(ctx)

	h := server.Handler(r, hub, nil)
	if *web != "" {
		if st, err := os.Stat(*web); err == nil && st.IsDir() {
			h = server.Handler(r, hub, os.DirFS(*web))
		}
	}
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
