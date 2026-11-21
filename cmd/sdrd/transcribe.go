package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/wardviaene/sweep/internal/config"
	"github.com/wardviaene/sweep/internal/decoder"
	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/transcribe"
)

type transcribeFlags struct{ mode, model, bin *string }

// addTranscribeFlags registers the transcription flags (docs/transcription.md).
func addTranscribeFlags() transcribeFlags {
	home, _ := os.UserHomeDir()
	return transcribeFlags{
		mode:  flag.String("transcribe", "auto", "speech transcription: auto (on when the model file exists), on or off; PUT /api/transcribe overrides"),
		model: flag.String("whisper-model", filepath.Join(home, ".local/share/sweep/models/ggml-small.en.bin"), "whisper.cpp model for transcription (make whisper-model)"),
		bin:   flag.String("whisper-bin", "whisper-server", "whisper-server binary (brew install whisper-cpp)"),
	}
}

// startTranscribe opens the transcript store and starts the worker; it returns
// nil (transcription unavailable) when the store cannot be opened.
func startTranscribe(ctx context.Context, f transcribeFlags, tee *transcribe.Tee, store *config.Store, events *decoder.Events, r *radio.Radio) *transcribe.Transcriber {
	dir := dataDirPath()
	tx, err := transcribe.New(transcribe.Options{Dir: filepath.Join(dir, "clips"), DataDir: dir, Model: *f.model, Bin: *f.bin,
		Mode: *f.mode, Config: store, Events: events, Tee: tee,
		VFO: func() (uint32, string) { d := r.Status().Demod; return d.FreqHz, d.Mode }})
	if err != nil {
		log.Printf("transcribe: disabled: %v", err)
		return nil
	}
	go tx.Run(ctx)
	if tx.Enabled() {
		log.Printf("transcribe: on (model %s, clips in %s)", *f.model, filepath.Join(dir, "clips"))
	} else {
		log.Printf("transcribe: off (-transcribe=%s, model %s)", *f.mode, *f.model)
	}
	return tx
}
