# sweep

A local RTL-SDR control station for macOS: live spectrum and waterfall, click-to-tune, a band catalog (US and EU), NFM/AM/SSB audio in the browser, and one-click decoders (rtl_433 sensors, ADS-B aircraft, APRS packets, AIS ships). Everything the UI does is also available over HTTP and through the `sdrctl` CLI, so an agent can drive the radio.

**Receive only.** There is no transmit code.

Built and tested with a Nooelec NESDR SMArt v5 (RTL2832U + R820T, 0.5 ppm TCXO) on an Apple Silicon Mac.

## Quick start

```bash
brew install librtlsdr rtl_433 dump1090-fa direwolf sox   # sox is only for the starter commands
make envcheck                                              # dongle, tuner and tools
cd web && npm install && cd ..
make dev                                                   # sdrd + Vite; open http://localhost:5173
```

No dongle? `make dev-synth` runs everything against a synthetic IQ source.

## Layout

```
cmd/sdrd         daemon: owns the dongle through a private rtl_tcp, FFT, demod, decoders, HTTP/WS API
cmd/sdrctl       CLI client (JSON output)
internal/rtltcp  rtl_tcp protocol client and process supervisor
internal/dsp     spectrum (FFT), peak finding, NFM/AM/USB/LSB demodulator
internal/radio   tuning state, IQ pipeline, listening VFO, decoder taps
internal/decoder rtl_433 / dump1090 / direwolf workers, APRS/AIS/SBS parsers, event store
internal/server  REST and WebSocket API
internal/source  synthetic IQ (with optional FM-modulated WAV) and cu8 playback
data/bands.json  band catalog, embedded in sdrd
web/             Vite + React + TypeScript UI
docs/protocol.md the API
```

## How it works

`sdrd` starts `rtl_tcp` on a private port and is its only client, so it is the single owner of the dongle. IQ (`cu8`, 2.048 MS/s by default) feeds three consumers at once:

- **Spectrum**: Blackman-Harris FFT (2048 bins), power-averaged and sent as 20 fps `uint8` dB frames on `/ws/spectrum`.
- **Listening VFO**: a two-stage decimating channel filter and an NFM/AM/SSB demodulator in Go. Its audio goes out on `/ws/audio`.
- **Decoders** (one at a time): raw IQ piped to `rtl_433 -r cu8:-` or `dump1090 --ifile -`, or audio from a private NFM demod piped to `direwolf`. The waterfall keeps running while a decoder runs.

Below 24 MHz, `sdrd` switches the v5 to direct sampling (Q branch; `-direct-branch 1` for I) for HF.

## sdrctl

`SDRD_ADDR` (default `127.0.0.1:8073`) selects the daemon. Output is JSON.

```bash
sdrctl status
sdrctl tune 446.1M -gain 29.7          # gain snaps to the tuner's steps; -gain auto
sdrctl peaks -n 5                      # carriers above the noise floor (crystal spurs flagged)
sdrctl spectrum -bins 64
sdrctl listen 162.55M -mode nfm -squelch -60
sdrctl demod off
sdrctl bands -region eu
sdrctl band air                        # tune to a band and start its default mode
sdrctl decoder rtl_433                 # rtl_433 | adsb | aprs | ais | none
sdrctl events -decoder rtl_433 -limit 20
sdrctl scan 430M 440M -dwell 500       # step across a range, return the strongest carriers
sdrctl record 5                        # cu8 recording in ./recordings
```

Play a recording back with `bin/sdrd -iq recordings/<file>.cu8`. The center frequency and rate come from the file name.

## Testing

```bash
make test
```

The Go tests need no hardware. They cover the rtl_tcp protocol, FFT bin placement, peak finding, demodulators on synthetic signals (tone recovery, adjacent-channel and sideband rejection), the APRS/AIS/SBS parsers, a band catalog validation, and (when rtl_433 is installed) a synthetic 433 MHz remote burst decoded through rtl_433 with the decoder's exact arguments.

Verified on air with the NESDR v5 and a 400 MHz-1 GHz log-periodic: spectrum, tuning, NFM audio and squelch, and ADS-B (11 aircraft tracked within 100 s).

For an end-to-end APRS check without RF, generate packets with direwolf's `gen_packets` and FM-modulate them in the synthetic source:

```bash
echo 'N0CALL-9>APRS,WIDE2-1:!4903.50N/07201.75W>test' > msg.txt
gen_packets -a 100 -o aprs.wav msg.txt
bin/sdrd -synth -synth-fm 144.39M:aprs.wav
sdrctl decoder aprs && sleep 5 && sdrctl events -decoder aprs
```

## Notes and known limits

- **Crystal spurs.** Peaks at multiples of 28.8 MHz (460.8, 518.4, 633.6 MHz, ...) come from the dongle itself. `/api/peaks` marks them with `spur: true`.
- **Gain.** Auto gain on the R820T raises the noise floor a lot. `sdrd` starts at 29.7 dB.
- **Mode switching.** Starting a decoder retunes the radio. Retuning while a decoder runs usually breaks the decoding.
- **Weather satellites.** NOAA-15/18/19 APT were retired in 2025. The catalog points to Meteor-M LRPT, recorded with `rtl_sdr` and decoded offline in SatDump.
- **Antennas.** What you can hear depends on the antenna. Each band in the catalog lists a suitable one.
