.PHONY: envcheck build test dev dev-synth web decoders

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
