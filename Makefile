.PHONY: envcheck build test dev dev-synth web decoders metadata whisper-model

envcheck:
	@bash scripts/envcheck.sh

build:
	go build -o bin/sdrctl ./cmd/sdrctl
	go build -o bin/sdrd ./cmd/sdrd

test:
	go test ./...

web:
	cd web && npm install && npm run build

# sdrd (real dongle) + Vite dev server; Ctrl-C stops both.
dev: build
	@trap 'kill 0' INT TERM EXIT; bin/sdrd $(SDRD_ARGS) & (cd web && npm run dev) & wait

# same, with the synthetic IQ source (no dongle needed)
dev-synth: SDRD_ARGS = -synth
dev-synth: dev

# build decoders not in Homebrew (radiosonde, UAT 978, dsd-fme) into ~/.local/share/sweep/bin
decoders:
	@bash scripts/install-decoders.sh

# bulk-download the public datasets used to name aircraft, airlines and satellites (docs/metadata.md)
metadata:
	@bash scripts/fetch-metadata.sh

# download the whisper.cpp model used for transcription (docs/transcription.md), ~490 MB, skipped if present
WHISPER_MODEL ?= $(HOME)/.local/share/sweep/models/ggml-small.en.bin
whisper-model:
	@if [ -f "$(WHISPER_MODEL)" ]; then echo "$(WHISPER_MODEL) exists"; else \
	  mkdir -p "$(dir $(WHISPER_MODEL))" && \
	  curl -fL -A "Mozilla/5.0 (sweep make whisper-model)" -o "$(WHISPER_MODEL).part" \
	    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin && \
	  mv "$(WHISPER_MODEL).part" "$(WHISPER_MODEL)"; fi
