# sdrd API reference

`sdrd` is the daemon of **sweep**, a local RTL-SDR control station. It owns the dongle, computes spectrum frames, runs one listening demodulator and at most one data decoder, and exposes all of it over a small HTTP + WebSocket API. This document is for people who want to write their own client, script or integration. The same API drives the bundled web UI and the `sdrctl` command line tool.

A machine-readable description of the REST endpoints is in [`openapi.yaml`](openapi.yaml) (OpenAPI 3.1; the WebSockets are described there in the `x-websockets` extension).

Contents: [Overview](#overview) · [Security](#security) · [Endpoint index](#endpoint-index) · [REST endpoints](#rest-endpoints) · [WebSocket streams](#websocket-streams) · [Decoders and event shapes](#decoders-and-event-shapes) · [Metadata](#metadata) · [Scanner](#scanner) · [SSTV and files](#sstv-and-files) · [Pass autopilot](#pass-autopilot) · [Transcription](#transcription) · [Survey](#survey-contract) · [Client examples](#client-examples) · [Privacy](#privacy-notes)

## Overview

- **Base URL**: `http://127.0.0.1:8073` by default. Change it with `sdrd -addr host:port`. WebSockets use `ws://` on the same address.
- **Format**: every REST request body and response is JSON (`Content-Type: application/json`). Request bodies are limited to 4096 bytes. `GET /api/bands` returns the embedded catalog verbatim.
- **Units**: frequencies and sample rates in **Hz** (integers), power in **dB** (relative, not calibrated: roughly dBFS), durations in **seconds** unless a field name ends in `_ms`, timestamps as **Unix seconds** (floating point for event `ts` and survey `started`).
- **Errors**: any failure returns a JSON body `{"error": "human readable message"}`. Status codes used:

  | code | meaning |
  |---|---|
  | 200 | success |
  | 202 | accepted, work continues in the background (`POST /api/survey` only) |
  | 400 | invalid request body or parameter, or the requested action is not possible in the current state (for example scanning while a decoder runs) |
  | 404 | unknown path (plain text from Go's HTTP server, not JSON) |
  | 405 | wrong HTTP method for a known path (plain text, with an `Allow` header) |
  | 409 | the radio is owned by another long-running operation (a survey, scan or recording) |
  | 500 | server-side failure, for example the recording file could not be written |

- **Single-owner model**. There is one dongle, and `sdrd` is its only client.
  - There is one center frequency / sample rate / gain for everybody. A tune by any client affects every client and the waterfall.
  - **One decoder at a time.** Starting a decoder stops the running one and retunes the radio to what the new one needs. Retuning the center frequency while a decoder runs is allowed but usually breaks decoding.
  - **Scan, record and survey are exclusive.** They share one lock. A second one fails with 409 (record, survey) or 400 (scan). A scan also refuses to start while a decoder runs; a survey stops the running decoder and restores it afterwards.
  - **The scanner and the pass autopilot also own the radio** while they run. Manual `POST /api/tune`, `/api/demod` and `/api/decoder` then return **409** (`scanner running`, or `autopilot pass in progress`); `POST /api/survey` returns 409 while an autopilot pass runs, and a scan or recording is refused (400 and 409 respectively).
  - **One listening demodulator** (the "VFO") runs alongside the spectrum. Its audio is broadcast to every `/ws/audio` client.
- Spectrum frames keep flowing while a decoder, a demodulator or a scan runs.

## Daemon flags used in this document

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:8073` | HTTP listen address |
| `-db-dir` | `~/.local/share/sweep/db` | Metadata datasets, read offline |
| `-data-dir` | `~/.local/share/sweep` | Files sdrd produces: `sstv/` pictures and `passes/` records (served via `/api/files/{kind}/{name}`) |
| `-record-dir` | `recordings` | IQ files of `POST /api/record` |
| `-config` | `~/.config/sweep/config.json` | Local config file |
| `-synth`, `-synth-fm FREQ:file.wav`, `-synth-burst FREQ:ON:OFF[:AMP]` | off | Testing without a dongle. `-synth` uses a synthetic IQ source; `-synth-fm` loops a 16-bit WAV as FM at FREQ; **`-synth-burst`** adds a carrier at FREQ keyed on for ON seconds and off for OFF seconds (`OFF` 0 = continuous), optional amplitude AMP; repeatable. Useful for exercising the scanner. |
| `-transcribe`, `-whisper-model`, `-whisper-bin` | `auto` | Local speech transcription, see [Transcription](#transcription) |
| `-iq FILE` | | Play back a `.cu8` recording instead of the dongle |

## Security

> **There is no authentication or authorization.** Anyone who can reach the port can tune the radio, start and stop decoders, listen to audio, read decoded data (aircraft, sensor readings, packets, calls, ...) and **read and write the local configuration file** (receiver location, your own utility meter IDs, antenna). They can also make the daemon write IQ recordings to its disk.

- `sdrd` binds to `127.0.0.1` by default, so only processes on the same machine can reach it. Keep it that way unless you have a reason not to.
- If you set `-addr 0.0.0.0:8073` (or any non-loopback address) you are publishing the radio and your configuration to that network. Put `sdrd` behind a reverse proxy that authenticates (and ideally uses TLS) and keep `sdrd` itself on loopback. The proxy must forward WebSocket upgrades on `/ws/*`.
- **WebSocket origin checks.** The three WebSocket endpoints accept browser connections only from these `Origin` hosts: `localhost:*`, `127.0.0.1:*` and `[::1]:*` (any port). Also accepted:
  - clients that send **no `Origin` header** (command line tools, Python/Go/Node clients, `curl`-style tools), and
  - a page whose origin host equals the `Host` of the request (for example the web UI that `sdrd` serves itself).

  A browser page served from any other origin (`https://dashboard.example.com`, a file shared on your LAN, ...) is **rejected with HTTP 403** when it opens a WebSocket. There is no flag to widen this. To use a remote browser client you must proxy the WebSocket so that the request seen by `sdrd` comes from a same-host or localhost origin, or use a non-browser client. This is a cross-site protection, not an authentication mechanism: it does not stop a local process or a non-browser client.
- REST endpoints do not check `Origin` at all and send no CORS headers. A web page on another origin cannot read the responses in a browser (no CORS), but simple cross-origin `POST`s with a JSON body from a page are possible in principle; this is another reason to keep the daemon on loopback or behind an authenticating proxy.
- `sdrd` is **receive-only**. There is no transmit code anywhere in the project. The worst an unauthorized client can do is change what the dongle listens to, consume CPU, fill the recording directory and read or alter the local config.

## Endpoint index

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/status` | Radio, demodulator and decoder state |
| POST | `/api/tune` | Set center frequency, sample rate, gain |
| POST | `/api/demod` | Control the listening demodulator (VFO) |
| GET | `/api/spectrum` | Aggregated recent spectrum as JSON |
| GET | `/api/peaks` | Carriers above the noise floor |
| GET | `/api/bands` | Band and antenna catalog |
| POST | `/api/decoder` | Start or stop a data decoder |
| GET | `/api/events` | Recent decoded events (polling) |
| POST | `/api/scan` | Sweep a frequency range and list carriers (blocking) |
| POST | `/api/record` | Record raw IQ to a file on the server (blocking) |
| GET | `/api/config` | Read the local configuration |
| PUT | `/api/config` | Merge changes into the local configuration |
| POST | `/api/survey` | Start a "what is on the air?" survey |
| GET | `/api/survey` | Survey progress and results |
| POST | `/api/survey/stop` | Stop the survey after the current step |
| POST | `/api/scanner` | Start (or reconfigure) the transmission scanner |
| GET | `/api/scanner` | Scanner state |
| POST | `/api/scanner/stop` | Stop the scanner and restore the radio |
| POST | `/api/scanner/lockout` | Add or remove a locked-out channel |
| GET | `/api/files/{kind}/{name}` | Read-only access to SSTV pictures and pass records |
| GET | `/api/files/passes/{dir}/{name}` | A file inside one autopilot pass directory |
| GET | `/api/transcripts` | Recent voice transcripts |
| GET | `/api/transcribe` | Transcription status |
| PUT | `/api/transcribe` | Switch transcription on or off |
| POST | `/api/transcribe` | Transcribe the live audio or an existing WAV now |
| GET | `/api/files/clips/{date}/{name}` | A recorded voice clip |
| GET | `/api/autopilot` | Pass autopilot state, upcoming passes, history |
| PUT | `/api/autopilot` | Change autopilot settings |
| POST | `/api/autopilot/run` | Handle a satellite's current pass now |
| POST | `/api/autopilot/stop` | End the current pass early |
| GET | `/api/meta` | Which metadata datasets are loaded |
| GET | `/api/meta/aircraft/{hex}` | Offline lookup: country, registration, type, year of a Mode S address |
| GET | `/api/meta/callsign/{callsign}` | Offline lookup: airline and flight number of a callsign |
| GET | `/api/satellites/match` | Satellites with a downlink near a frequency |
| GET | `/api/satellites/passes` | Upcoming satellite passes (needs receiver location) |
| GET | `/api/satellites/overhead` | Satellites above the horizon now (needs receiver location) |
| WS | `/ws/spectrum` | Live spectrum frames |
| WS | `/ws/audio` | Live demodulated audio and squelch level |
| WS | `/ws/events` | Live decoder events |

`GET /` and other paths serve the optional web UI when `sdrd` is started with a built UI directory (`-web`, default `web/dist`). That is not part of the API.

## REST endpoints

### GET /api/status

Returns the current state.

```json
{
  "device": "Example Vendor, Example SDR v1, SN: 00000001",
  "tuner": "R820T",
  "gains_db": [0.0, 0.9, 1.4, 2.7, 3.7, 7.7, 8.7, 12.5, 14.4, 15.7, 16.6, 19.7, 20.7, 22.9, 25.4, 28.0, 29.7, 32.8, 33.8, 36.4, 37.2, 38.6, 40.2, 42.1, 43.4, 43.9, 44.5, 48.0, 49.6],
  "center_hz": 433920000,
  "sample_rate": 2048000,
  "gain_db": 29.7,
  "fft_size": 2048,
  "fps": 20,
  "mode": "spectrum",
  "direct_sampling": false,
  "demod": {"mode": "off", "freq_hz": 0, "bandwidth_hz": 0, "squelch_db": null, "audio_rate": 0, "level_db": 0},
  "decoder": {"name": "none", "running": false, "events": 0, "last_event_ts": 0},
  "connected": true
}
```

| Field | Type | Notes |
|---|---|---|
| `device`, `tuner` | string | Empty until the dongle is connected. `tuner` is `R820T` or `R828D` for the supported tuners. |
| `gains_db` | number[] | Gain steps the tuner supports. |
| `center_hz`, `sample_rate` | integer | Current tuning. The visible span is `center_hz ± sample_rate/2`. |
| `gain_db` | number or null | `null` means automatic gain. |
| `fft_size`, `fps` | integer | Bins per spectrum frame and frames per second (fixed at daemon start: `-fft`, `-fps`). |
| `mode` | string | Always `"spectrum"` today. |
| `direct_sampling` | boolean | `true` below 24 MHz: HF via direct sampling. |
| `demod` | object | Listening demodulator, see [POST /api/demod](#post-apidemod). |
| `decoder` | object | Decoder status, see [POST /api/decoder](#post-apidecoder). |
| `connected` | boolean | `false` while `sdrd` is (re)starting its connection to the dongle; it retries with back-off. |
| `error` | string | Present only when the last connection attempt failed. |

When `sdrd` plays back a recording (`-iq`), the dongle fields describe the recording and tuning is disabled.

### POST /api/tune

Change tuning. Send any subset of the fields; absent fields stay unchanged. Returns the new [status](#get-apistatus).

| Field | Type | Required | Range | Notes |
|---|---|---|---|---|
| `freq_hz` | integer | no | 500000 to 1766000000 | Center frequency. Below 24 MHz `sdrd` switches to direct sampling automatically (HF). |
| `sample_rate` | integer | no | 225001-300000 or 900001-3200000 | 2.4 MS/s is the highest reliable rate. |
| `gain_db` | number or null | no | any | Snapped to the nearest tuner gain step. `null` selects automatic gain. Absent leaves the gain unchanged. |

```bash
curl -s -X POST http://127.0.0.1:8073/api/tune \
  -d '{"freq_hz": 433920000, "gain_db": 29.7}'
curl -s -X POST http://127.0.0.1:8073/api/tune -d '{"gain_db": null}'   # auto gain
```

Errors (400): body is not valid JSON or has a wrongly typed field; `freq_hz` or `sample_rate` out of range; tuning while playing a recording (`playing a recording: tuning is disabled`); dongle error.

Side effects: the spectrum history is reset, a new `meta` frame is sent on `/ws/spectrum`, and the VFO is kept inside the new span (it moves so it keeps the same offset from center).

### POST /api/demod

Control the single listening demodulator ("VFO"). Its audio is available on [`/ws/audio`](#ws-audio). Any subset of fields; returns the [status](#get-apistatus) (`demod` holds the result).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `mode` | string | no | unchanged | `off`, `nfm`, `am`, `usb`, `lsb`. |
| `freq_hz` | integer | no | center + sample_rate/4 the first time | Frequency to listen to. For `usb`/`lsb` this is the suppressed-carrier dial frequency. If it lies outside the middle 80% of the current span, the **center is retuned** to `freq_hz - sample_rate/4` so the VFO does not sit on the DC spike. |
| `bandwidth_hz` | number | no | per mode | 500 to 200000. When the mode changes and no bandwidth is given: `nfm` 12500, `am` 10000, `usb`/`lsb` 2800. |
| `squelch_db` | number or null | no | unchanged | Channel power below this mutes the audio. `null` disables squelch. Absent leaves it unchanged. |

`status.demod`:

```json
{"mode": "nfm", "freq_hz": 446006250, "bandwidth_hz": 12500, "squelch_db": null, "audio_rate": 32000, "level_db": -71.3}
```

`audio_rate` is the sample rate of the PCM on `/ws/audio` (0 while `off`); `level_db` is the current channel power.

```bash
curl -s -X POST http://127.0.0.1:8073/api/demod \
  -d '{"mode":"nfm","freq_hz":446006250,"squelch_db":-60}'
curl -s -X POST http://127.0.0.1:8073/api/demod -d '{"mode":"off"}'
```

Errors (400): invalid JSON, unknown `mode`, `bandwidth_hz` out of range, or a failed retune. While the `dsd` decoder runs, the VFO is switched off and decoded voice replaces the audio stream. Setting a `mode` other than `off` **stops a running `dsd` decoder** (dsd owns the audio stream); `{"mode": "off"}` leaves it running.

### GET /api/spectrum

The last few spectrum frames, aggregated and max-pooled, in dB. For clients that want to "see" the band without a WebSocket.

| Query | Type | Default | Range | Notes |
|---|---|---|---|---|
| `bins` | integer | 256 | 16 to `fft_size` | Output bins. Max-pooled so narrow carriers survive. |
| `frames` | integer | 20 | 1 to 40 | Number of recent frames (20 frames = 1 s at 20 fps). Fewer are used if less history exists (for example just after a retune). |
| `agg` | string | `avg` | `avg`, `max` | Aggregation across frames. |

```json
{"center_hz": 452500000, "sample_rate": 2048000, "start_hz": 451539500, "bin_hz": 128000,
 "frames": 20, "agg": "avg", "floor_db": -73.3, "db": [-74.6, -73.0, -72.4]}
```

`db[i]` is centered on `start_hz + i * bin_hz`. `floor_db` is the median noise floor. (The `bin_hz` value above is illustrative; with 16 bins over 2.048 MHz it is 128000.) Errors (400): bad query value, or `no spectrum data yet` right after start or a retune.

### GET /api/peaks

Carriers at least `min_snr` dB above the median floor, strongest first. The outer 5% at each edge of the span is ignored (filter roll-off).

| Query | Type | Default | Range |
|---|---|---|---|
| `n` | integer | 10 | 1 to 100 |
| `min_snr` | number | 10 | any |
| `frames` | integer | 20 | 1 to 40 |
| `agg` | string | `max` | `avg`, `max` (`max` catches bursts) |

```json
{"center_hz": 452500000, "sample_rate": 2048000, "frames": 20, "agg": "max", "floor_db": -72.2,
 "peaks": [{"freq_hz": 452738000, "db": -43.1, "snr_db": 29.1},
           {"freq_hz": 460800000, "db": -50.2, "snr_db": 22.0, "spur": true}]}
```

`spur: true` marks a peak within 2 bins of a multiple of 28.8 MHz (the RTL2832U crystal); these are usually generated inside the dongle. The field is **omitted** when false. `label` (string, omitted when unknown) names the frequency when a band-catalog channel lies within 5 kHz (for example "NOAA Weather Radio: WX2") or a satellite with a matching downlink is currently above the horizon ("EXAMPLESAT-1 downlink"); see [Metadata](#metadata). `peaks` is `[]` when nothing qualifies. Errors (400): bad query value or no data yet.

### GET /api/bands

Returns the band and antenna catalog (`data/bands.json`, embedded in `sdrd`) verbatim. Takes no parameters (filter by region on the client).

Top level:

| Field | Notes |
|---|---|
| `version` | Catalog format version (integer). |
| `default_region` | `"us"` or `"eu"`. |
| `regions` | Object: region id to display name. |
| `fields` | Object documenting the band fields (informational). |
| `bands[]` | Bands, see below. |
| `antennas[]` | Antenna presets, see below. |

Band:

| Field | Type | Notes |
|---|---|---|
| `id`, `name` | string | `id` is stable, for example `2m`, `air`, `ism433`. |
| `regions` | string[] | Regions where the band applies. |
| `lo_mhz`, `hi_mhz` | number | Band edges in MHz. |
| `edges` | object, optional | Region id to `[lo, hi]` MHz; overrides `lo_mhz`/`hi_mhz` for that region. |
| `tune_mhz` | number | Where "jump to band" tunes. |
| `hf` | boolean | `true`: needs HF direct sampling (below 24 MHz). |
| `mode` | string | Default demodulator: `nfm`, `am`, `usb`, `lsb` or `none` (digital only). |
| `decoder` | string or null | Decoder that handles the band: `rtl_433`, `meters`, `adsb`, `uat`, `aprs`, `ais`, `packet`, `radiosonde`, `dsd`, or `null`. |
| `on_air`, `decode_with`, `antenna`, `tx_license`, `starter` | string / string[] | Human-readable notes. |
| `channels[]` | object[] | `{mhz, name, mode, regions?, decoder?}`. A channel without `regions` applies to every region of its band. |

Antenna: `{"id": "jpole", "name": "...", "good": [[140, 165], [425, 460]], "fair": [[118, 140]]}`. Ranges are in MHz. A frequency is a **good** fit if it lies in a `good` range, **fair** if in a `fair` range, otherwise **poor**. Antenna ids at the time of writing: `logper`, `jpole`, `wideband`, `hfwire`, `none`. Treat the catalog as data and read the ids from it.

### POST /api/decoder

Start a decoder (stopping any running one) or stop it with `"none"`. Returns the [status](#get-apistatus).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `decoder` | string | yes | | `rtl_433`, `meters`, `adsb`, `uat`, `aprs`, `ais`, `packet`, `radiosonde`, `dsd`, `sstv`, or `none`. An empty string also stops. |
| `region` | string | no | | `us` or `eu`. Only used by `aprs` (144.39 MHz in `us`, 144.8 MHz in `eu`; anything but `eu` is treated as US). |
| `freq_hz` | integer | no | the VFO frequency | Used by `packet`, `dsd`, `radiosonde`, `sstv` (default 145800000). |
| `baud` | integer | no | 1200 | `packet` only: 1200 or 9600. |
| `mode` | string | no | `auto` | `sstv` only: `auto` (detect from the VIS header) or `Robot36`, `PD90`, `PD120`, `PD180`, `Martin1`, `Scottie1`. Case, spaces and dashes are ignored (`robot 36`, `martin-1`); aliases `r36`, `m1`, `s1`. An unknown mode is a 400. |

What each decoder tunes to and runs is in [Decoders and event shapes](#decoders-and-event-shapes). The call returns after about 1.5 seconds (up to about 8 seconds for `radiosonde` without `freq_hz`, which searches 400-406 MHz first), or earlier if the decoder fails immediately. A decoder that survives that window is reported as started; later failures are visible in `status.decoder.error` and as `log` events.

`status.decoder`:

```json
{"name": "rtl_433", "running": true, "freq_hz": 433920000, "started": 1795286400, "events": 12, "last_event_ts": 1795286410.5, "error": ""}
```

| Field | Notes |
|---|---|
| `name` | The decoder, or `"none"` when idle. |
| `running` | `false` after the decoder exited; the last run's `name`, `events` and `error` stay visible until the next start. |
| `freq_hz` | Present when the decoder has a fixed frequency. |
| `started` | Unix seconds, present while/after running. |
| `events` | Events published by this run. |
| `last_event_ts` | Unix time (seconds) of the latest event from this run, `0` if none. |
| `error` | Present when the last run ended with an error. |

Errors: 400 for an unknown decoder name (the message lists the valid names), a missing external tool (`rtl_433 not found ...`), `packet` with a `baud` other than 1200/9600 or without a frequency and no VFO, `dsd` without a frequency or **outside the amateur bands** (50-54, 144-148, 222-225, 420-450, 902-928, 1240-1300 MHz), a radiosonde search that finds no carrier, or a tune failure. 409 while a survey is running, while the scanner runs, or during an autopilot pass.

```bash
curl -s -X POST http://127.0.0.1:8073/api/decoder -d '{"decoder":"rtl_433"}'
curl -s -X POST http://127.0.0.1:8073/api/decoder -d '{"decoder":"packet","freq_hz":437800000,"baud":9600}'
curl -s -X POST http://127.0.0.1:8073/api/decoder -d '{"decoder":"none"}'
```

### GET /api/events

Recent decoded events from a ring buffer of the last 1000, for clients that prefer polling over [`/ws/events`](#ws-events).

| Query | Type | Default | Range | Notes |
|---|---|---|---|---|
| `since` | integer | 0 | | Only events with `id > since`. A non-numeric value is treated as 0. |
| `limit` | integer | 200 | 1 to 1000 | If more events match, the **newest** `limit` are returned. |

Returns a JSON array of [event envelopes](#event-envelope), oldest first (`[]` when empty). Poll by remembering the largest `id` you saw. Errors (400): bad `limit`.

### POST /api/scan

Sweep a frequency range and list carriers. **Blocks** until the scan finishes: about `steps * (dwell_ms + 100)` milliseconds, where one step covers 80% of the sample rate and there are at most 400 steps. Set a long client timeout. The original center frequency is restored afterwards.

| Field | Type | Required | Default | Range |
|---|---|---|---|---|
| `start_hz` | integer | yes | | below `stop_hz` |
| `stop_hz` | integer | yes | | above `start_hz` |
| `dwell_ms` | integer | no | 500 | 100 to 5000 |
| `min_snr` | number | no | 10 | dB above the median floor |
| `max` | integer | no | 50 | maximum peaks returned |

```json
{"start_hz": 430000000, "stop_hz": 440000000, "duration_s": 7.2,
 "steps": [{"center_hz": 430819200, "floor_db": -72.1}],
 "peaks": [{"freq_hz": 433920000, "db": -40.2, "snr_db": 31.0}]}
```

`peaks` are strongest first; duplicates from overlapping steps (within 5 kHz) are merged. `spur: true` is added when a peak sits on a 28.8 MHz crystal harmonic (omitted when false). `label` is added as for `/api/peaks`.

Errors: **400** for invalid input, `stop_hz` not above `start_hz`, `dwell_ms` out of range, more than 400 steps (narrow the range), a decoder running (`stop the decoder before scanning`), or **another scan, recording or survey running** (this one is 400, not 409). 

### POST /api/record

Record raw IQ to a file on the machine running `sdrd`. **Blocks** for the length of the recording.

| Field | Type | Required | Range |
|---|---|---|---|
| `seconds` | number | yes | 0.1 to 60 |

```json
{"path": "/abs/path/recordings/20261121-130038_433920000Hz_2048000sps.cu8", "bytes": 20480000, "seconds": 5, "center_hz": 433920000, "sample_rate": 2048000}
```

The file holds interleaved unsigned 8-bit I/Q samples (`cu8`, the `rtl_sdr` format) at the current center and rate. `path` is the absolute path on the **server**; there is no endpoint to download it. Play it back with `sdrd -iq <path>` (center and rate are parsed from the file name). The directory is set with `sdrd -record-dir` (default `recordings`).

Errors: 400 for invalid JSON or `seconds` out of range, **409** if a scan, recording or survey is already running, 500 if the file cannot be created or written.

### GET /api/config

```json
{"receiver": {"lat": 12.34, "lon": 56.78, "alt_m": 100}, "meter_ids": ["12345678"], "antenna": "jpole"}
```

| Field | Type | Notes |
|---|---|---|
| `receiver` | object or null | `{lat, lon, alt_m?}` in degrees and meters. `null` until set. |
| `meter_ids` | string[] | IDs of utility meters **you own**. Omitted when empty. |
| `antenna` | string | An antenna id from the catalog. Omitted when unknown. |

Stored by `sdrd` in a local JSON file (default `~/.config/sweep/config.json`, mode 0600, flag `-config`). See [Privacy notes](#privacy-notes).

### PUT /api/config

Merge update: only the fields **present** in the body change; send the field with `null` (or `[]` for `meter_ids`) to clear it. Returns the full config.

| Field | Type | Notes |
|---|---|---|
| `receiver` | object or null | `{lat, lon, alt_m}`. `lat` -90..90, `lon` -180..180, `alt_m` optional (meters; `null` clears it). `null` clears the whole receiver. A `receiver` object replaces the previous one entirely. |
| `meter_ids` | string[] or null | Replaces the list. |
| `antenna` | string or null | Must be an antenna id from `GET /api/bands`. `null` clears. |

```bash
curl -s -X PUT http://127.0.0.1:8073/api/config \
  -d '{"receiver": {"lat": 12.34, "lon": 56.78, "alt_m": 100}, "meter_ids": ["12345678"]}'
curl -s -X PUT http://127.0.0.1:8073/api/config -d '{"antenna": "jpole"}'
```

Errors (400): invalid JSON or wrong types, latitude/longitude out of range, unknown antenna (the message lists valid ids), or the file could not be written. When a receiver location is set, the ADS-B decoder passes it to `dump1090` (`--lat/--lon`) to enable local position decoding; restart the decoder for a change to apply.

### POST /api/survey

Start a "what is on the air?" survey. See the [survey contract](#survey-contract). The body is optional (empty body = all defaults).

| Field | Type | Default | Notes |
|---|---|---|---|
| `antenna` | string | configured antenna, else `none` | Antenna id. |
| `region` | string | catalog default | `us` or `eu`. |
| `seconds` | integer | 20 | Listening time per decoder step, 5 to 120. |
| `include_fair` | boolean | false | Also survey bands the antenna fits only "fair". |
| `listen` | boolean | true | After the scan of each voice band, play the strongest non-spur carrier for a few seconds (see [listen holds](#listen-holds)). |
| `listen_seconds` | integer | 8 | Hold per voice band, 2 to 30. Other values: 400. |
| `steps` | string[] | built plan | Explicit step ids, e.g. `["aprs", "scan:2m"]`. |

Returns **202** with the survey state. Errors: 400 for invalid JSON, unknown antenna/region/step id (the message lists valid ids), `seconds` or `listen_seconds` out of range, or no band fitting the antenna; **409** if a survey, scan or recording is already running. Also: `POST /api/decoder` returns 409 while a survey runs.

### GET /api/survey

Current or last survey state (shape in the [survey contract](#survey-contract)). Before the first survey: `{"id": 0, "state": "idle", "elapsed_s": 0, "estimated_s": 0, "steps": []}`. The last finished survey stays readable until the next one starts.

### POST /api/survey/stop

Ends a running survey after its current step (state becomes `stopped`) and returns the final state. Takes no body. Returns 200 with the current state also when nothing is running. It can take several seconds to return (up to 20 s) while the current step ends.

## Metadata

sdrd enriches decoded data with names from public datasets: airline names, aircraft registration and type, country, satellite names and labels for known frequencies. Every dataset is **optional**: missing data means missing fields, never an error.

**Privacy rule:** nothing is looked up online per aircraft, per station or per satellite. The datasets are downloaded in bulk by `make metadata` (`scripts/fetch-metadata.sh`, needs `curl`, `unzip` and `jq`) into a local directory and queried offline. `sdrd` itself makes no outbound requests. Bulk downloads do not reveal what you receive or where you are. The receiver location is used only locally, for satellite passes and range/bearing.

The directory is `~/.local/share/sweep/db` by default (`sdrd -db-dir`, or `SWEEP_DB` for the fetch script); it is never inside the repository. Re-running the script is safe; each dataset records its download time. Datasets load in the background at startup: restart `sdrd` after fetching.

| Dataset (`name`) | Files | Source | License | Used for |
|---|---|---|---|---|
| `airlines` | `airlines.dat` | OpenFlights (`raw.githubusercontent.com/jpatokal/openflights`) | ODbL 1.0 | ICAO airline designator to airline name and radio callsign |
| `faa` | `faa/MASTER.txt`, `faa/ACFTREF.txt` | FAA releasable aircraft registry (`registry.faa.gov`) | US public domain | US Mode S hex to N-number, manufacturer/model, year. **Owner names and addresses are never loaded.** |
| `satnogs` | `satnogs/satellites.json`, `satnogs/transmitters.json` | SatNOGS DB API (`db.satnogs.org`) | CC BY-SA 4.0 | satellite names, downlink frequencies, modes, baud rates |
| `tle` | `tle/amateur.txt`, `tle/weather.txt` | CelesTrak GP (`celestrak.org`) | public | orbits, for passes and "which satellite is overhead" |
| `icao_countries` | bundled in `sdrd` (`data/icao_countries.json`) | ICAO Annex 10 Vol III allocations | n/a | country from the 24-bit Mode S address |

The US N-number is also derived algorithmically from the hex code (A00001-ADF7C7), without data. Attribution is required by the ODbL and CC BY-SA datasets if you republish derived data.

### GET /api/meta

Which datasets are loaded.

```json
{"db_dir": "/home/user/.local/share/sweep/db",
 "datasets": [
   {"name": "airlines", "loaded": true, "records": 6000, "fetched": "2026-11-21T20:00:00Z", "license": "ODbL 1.0", "attribution": "OpenFlights"},
   {"name": "faa", "loaded": false, "records": 0, "license": "US public domain", "attribution": "FAA aircraft registry", "error": "not downloaded: run make metadata"}]}
```

Always five entries, in the order `airlines`, `faa`, `satnogs`, `tle`, `icao_countries`. `fetched` and `error` are omitted when empty; `error` is `loading` for a short time after startup.

### GET /api/meta/aircraft/{hex}

Looks up a 6-digit Mode S (ICAO 24-bit) address, case-insensitive.

```json
{"hex": "a1b2c3", "country": "United States", "registration": "N123AB", "type": "EXAMPLE 737-800", "year": 2015}
```

Fields other than `hex` are present only when known (an address from an unallocated block returns just `hex`). Errors: 400 when `hex` is not 6 hex digits.

### GET /api/meta/callsign/{callsign}

```json
{"callsign": "TST123", "airline": {"icao": "TST", "name": "Example Airways", "radio": "EXAMPLE", "country": "Exampleland"}, "flight_number": "123"}
```

A callsign that looks like a registration returns `{"callsign": "N123AB", "registration": "N123AB"}`. An unknown callsign returns just `callsign`. Errors: 400 unless the callsign is 1 to 12 characters. Note that the `airline` object inside decoder events has only `icao`, `name` and `radio`; this endpoint adds `country`.

### GET /api/satellites/match

Satellites with a downlink near a frequency, best first: satellites currently above the horizon first (needs the receiver location), then by frequency distance.

| Query | Type | Required | Default | Range |
|---|---|---|---|---|
| `freq_hz` | number | yes | | 1e6 to 6e9 |
| `tolerance_hz` | number | no | 15000 (room for Doppler) | 0 to 1000000 |

```json
{"freq_hz": 436500000, "receiver_set": true,
 "candidates": [{"norad": 12345, "name": "EXAMPLESAT-1", "downlink_hz": 436500000, "mode": "FM", "baud": 9600,
                 "transmitter": "Telemetry", "visible": true, "elevation_deg": 34.2, "azimuth_deg": 210.5,
                 "range_km": 1020, "doppler_hz": -4300}]}
```

`visible` is `false` for satellites below the horizon and for satellites that have a downlink but no orbit (no TLE); then `elevation_deg`, `azimuth_deg`, `range_km` and `doppler_hz` are omitted. `mode`, `baud`, `transmitter` are omitted when unknown. `receiver_set` is `false` when no receiver location is configured (nothing is then visible). `candidates` is `[]` when nothing matches. Errors: 400 for a missing or out-of-range `freq_hz` or `tolerance_hz`.

### GET /api/satellites/passes

Upcoming passes of satellites that have at least one downlink inside the tuner range (24-1766 MHz) and an orbit in a TLE group. Satellites that are above the horizon for the entire window (geostationary) are not listed.

| Query | Type | Default | Range |
|---|---|---|---|
| `hours` | integer | 24 | 1 to 72 |
| `min_elevation` | number | 10 | 0 to 90 (degrees, minimum peak elevation) |

```json
{"passes": [{"norad": 12345, "name": "EXAMPLESAT-1", "aos": 1795300000, "los": 1795300600,
             "max_elevation_deg": 62, "max_time": 1795300300,
             "downlinks": [{"freq_hz": 436500000, "mode": "FM", "baud": 9600, "decoder": "packet"}]}]}
```

`aos` / `los` / `max_time` are Unix seconds (acquisition of signal, loss of signal, time of maximum elevation). Per downlink, `decoder` is `packet` for AFSK/GMSK/FSK 1200/9600 AX.25 transmitters (use it as the `decoder` of [POST /api/decoder](#post-apidecoder) with `freq_hz`), `nfm` for FM voice (listen with `POST /api/demod`), otherwise `null`. Errors: 400 for bad parameters, or **when no receiver location is set** (`PUT /api/config` first).

### GET /api/satellites/overhead

Satellites above the horizon right now.

```json
{"satellites": [{"norad": 12345, "name": "EXAMPLESAT-1", "visible": true, "elevation_deg": 34.2, "azimuth_deg": 210.5,
                 "range_km": 1020,
                 "downlinks": [{"freq_hz": 436500000, "mode": "FM", "baud": 9600, "decoder": "packet", "doppler_hz": -4300}]}]}
```

`doppler_hz` is the current Doppler shift of each downlink. Errors: 400 when no receiver location is set.

### Enrichment of existing data

- `adsb` and `uat` snapshots: each aircraft gains `country`, `registration`, `type`, `year`, `airline` (`{icao, name, radio}`) and `flight_number` when known.
- `packet` events gain `satellite` (`{norad, name}`).
- Peaks from `/api/peaks`, `/api/scan` and survey scan steps gain `label`.

## Scanner

Voice traffic (air traffic control, repeaters, walkie-talkies, marine) comes in short bursts on otherwise silent channels. The scanner watches a frequency range and points the listening VFO at any carrier that **newly** appears, holds it while it lasts (plus a hang time), then waits silently for the next. Always-on carriers (interference, birdies, continuous broadcasts) become part of the baseline and are ignored. The audio goes out on [`/ws/audio`](#ws-audio).

- **Windows.** The range is covered by windows of 80% of the **current sample rate** (about 1.64 MHz at 2.048 MS/s). A range that fits one window is watched from that window; otherwise the scanner cycles through the windows, staying `dwell_ms` in each while idle (double dwell for 60 s after activity in a window).
- **Baseline.** Per window and per bin, a power baseline that **falls fast (time constant about 3 s) and rises slowly (about 20 s)**, updated only while the bin is not active. It is kept per window across visits, and seeded on the first visit of a window from the minimum of the first 500 ms (so a transmission already on air does not become baseline). Bins at least `margin_db` above their baseline are active; an active peak at a channel frequency (rounded to `step_hz`) is a **transmission start**.
- **Lock.** On a start the scanner sets the VFO to that channel (`mode`, `bandwidth_hz`) with squelch at baseline + `margin_db`/2, stops cycling, and stays while the channel stays active. After `hang_s` of quiet the transmission ends, the VFO goes silent and watching resumes. While idle the VFO stays on with the squelch held closed (+10 dB), so you hear nothing instead of a parked frequency.
- **Priority.** While locked, a stronger new transmission in the same window does not interrupt, except on channels in `priority_hz`.
- **Ignored.** Crystal spurs (multiples of 28.8 MHz), carriers within 10 kHz of the window center (DC), `lockout_hz` entries (plus or minus `step_hz`/2), and bins active longer than `max_tx_s`, which are folded into the baseline as continuous signals.
- **Exclusive.** Like a survey: starting it stops a running decoder, and it is refused (409) while a survey, scan or recording runs. Stopping restores the previous tuning, VFO and decoder. While it runs, manual tune/demod/decoder requests return 409 `scanner running`.

### POST /api/scanner

Starts the scanner, or reconfigures a running one (the saved radio state is kept). Empty body: 400 unless a range is given. Request body limit is 16 KiB.

| Field | Type | Default | Range / notes |
|---|---|---|---|
| `band` | string | | Catalog band id (see [GET /api/bands](#get-apibands)). Its region edges and `mode` are used unless overridden. |
| `region` | string | catalog default | `us` or `eu`; selects the band's region edges. |
| `lo_hz`, `hi_hz` | integer | from `band` | Give a band **or** both. Within 24-1766 MHz, `hi_hz` above `lo_hz`, at most 30 MHz wide. Explicit values override band edges. |
| `mode` | string | band mode | `nfm`, `am`, `usb`, `lsb`. Required without a band. |
| `bandwidth_hz` | number | mode default | 500 to 200000. |
| `step_hz` | integer | `am` 25000, `nfm` 12500 (`pmr446` band: 6250), `usb`/`lsb` 1000 | 100 to 1000000. Channel raster. |
| `margin_db` | number | 10 | 3 to 40. Dynamic detection threshold above baseline. |
| `hang_s` | number | 2 | 0.2 to 60. |
| `dwell_ms` | integer | 1500 | 200 to 30000. |
| `max_tx_s` | number | 120 | 5 to 3600. |
| `lockout_hz` | integer[] | `[]` | Channels to skip; merged with the lockouts remembered for the band. |
| `priority_hz` | integer[] | `[]` | Channels that may interrupt a lock. |

```bash
curl -s -X POST http://127.0.0.1:8073/api/scanner -d '{"band":"air"}'
curl -s -X POST http://127.0.0.1:8073/api/scanner -d '{"lo_hz":462550000,"hi_hz":462725000,"mode":"nfm"}'
```

Returns **202** with the state. Errors: 400 for invalid fields or a range outside the limits (the message names the field); 409 if a survey, scan or recording runs (`another scan, recording or survey is running`) or an autopilot pass is in progress.

### GET /api/scanner

```json
{"state": "watching", "band": "air", "lo_hz": 118000000, "hi_hz": 137000000, "mode": "am", "bandwidth_hz": 8000,
 "step_hz": 25000, "margin_db": 10, "hang_s": 2, "dwell_ms": 1500, "max_tx_s": 120,
 "window": {"center_hz": 118820000, "lo_hz": 118000000, "hi_hz": 119640000, "index": 0, "count": 12},
 "active": {"freq_hz": 119250000, "label": "", "since": 1795300000.4, "level_db": -41.2},
 "recent": [{"freq_hz": 119250000, "label": "", "start": 1795299990.1, "duration_s": 4.2, "peak_db": -38.0}],
 "channels": [{"freq_hz": 119250000, "label": "", "count": 7, "total_s": 21.5, "last": 1795300000.4}],
 "lockout_hz": [132150000], "priority_hz": [121500000], "started": 1795299000}
```

- `state`: `idle` (never started) | `watching` | `locked` (a transmission is playing) | `stopped`. The parameter fields (`band` through `max_tx_s`) echo the effective settings (omitted when empty); `band` is omitted for a custom range. `error` is set when restoring the radio failed.
- `window` is the span being watched; `active` is present only while `locked`.
- `recent` is the last 100 transmissions, newest first. `channels` is the 50 most active channels of this run, by count. `label` comes from the band catalog or [metadata](#metadata) and is `""` when unknown.
- `lockout_hz` and `priority_hz` are always arrays.

### POST /api/scanner/stop

Stops the scanner, restores the previous tuning, VFO and decoder, and returns the final state (`stopped`). Also 200 when nothing runs.

### POST /api/scanner/lockout

`{"freq_hz": 132150000}` adds a lockout, `{"freq_hz": 132150000, "remove": true}` removes it. Applies to the running scanner and is remembered per band in the local config (`scanner_lockouts`). Returns the state. Errors: 400 for invalid JSON or frequency, **409 `scanner not running`** when no scanner runs.

## SSTV and files

Slow-scan TV turns FM audio into pictures; the ISS (145.800 MHz) runs SSTV events and some CubeSats send images. sdrd decodes it natively in Go, with no external tools. Start it with `POST /api/decoder {"decoder": "sstv"}`; it stays armed and decodes every picture it hears.

- **Modes:** Robot 36, PD 90, PD 120, PD 180, Martin 1, Scottie 1 (names `Robot36`, `PD90`, `PD120`, `PD180`, `Martin1`, `Scottie1`). The mode is normally detected from the VIS header; with `mode` set it decodes without a VIS and syncs on line pulses.
- **Input:** a private 25 kHz raw NFM demodulator, resampled to 11025 Hz; luminance maps linearly from 1500-2300 Hz and lines are re-synced on the 1200 Hz pulse with slant correction.
- **Events:** see [event shapes](#decoders-and-event-shapes) (`progress`, `image`, `log`). The `url` of an event is a path on the same server, served by the endpoint below. The PNG is rewritten atomically as lines arrive, so you can poll it for a live preview.
- Pictures are written to `<data dir>/sstv/` (`sdrd -data-dir`, default `~/.local/share/sweep`; never inside the repository).

### GET /api/files/{kind}/{name}

Read-only access to files sdrd writes.

| Path part | Values |
|---|---|
| `kind` | `sstv` (SSTV pictures) or `passes` (plain files directly in the autopilot's `passes/` directory; per-pass files use the next endpoint). Anything else: 404 JSON error. |
| `name` | A plain file name matching `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$` (no separators, no leading dot, no `..`). Otherwise 400 `invalid file name`. |

Only **regular files directly in the kind's directory** are served (symbolic links are not followed, subdirectories are not reachable through this route). A missing file gives 404 `no such file`. The `Content-Type` comes from the extension: `.png` image/png, `.jpg`/`.jpeg` image/jpeg, `.json` application/json, `.txt` text/plain, `.wav` audio/wav, anything else application/octet-stream. Responses carry `X-Content-Type-Options: nosniff` and `Cache-Control: no-cache`, and support range requests.

```bash
curl -s -o picture.png http://127.0.0.1:8073/api/files/sstv/20261121-151200_pd120.png
```

### GET /api/files/passes/{dir}/{name}

A regular file inside one autopilot pass directory: `pass.json`, `pass.wav`, `image-NN.png`, `packets.jsonl` (and the `.cu8` IQ file). `dir` is the pass directory name (the `dir` field of the autopilot API) and `name` the file; **both** must match the plain-name regex above. An invalid `dir` or `name` gives 400 (`invalid pass directory` / `invalid file name`); a missing pass or file gives 404 (`no such pass` / `no such file`). Same content types and headers as above; symbolic links are not followed.

```bash
curl -s http://127.0.0.1:8073/api/files/passes/20261121-120000_12345_EXAMPLESAT-1/pass.json
```

## Pass autopilot

When enabled, sdrd watches upcoming satellite passes and handles each qualifying pass by itself. It needs the receiver location (`PUT /api/config`) and the `satnogs` and `tle` [metadata](#metadata) datasets.

- **Qualifying pass:** maximum elevation at least `min_elevation` (default 20 degrees), the satellite listed in `satellites` (NORAD ids; empty means all), and at least one downlink sdrd can use: a `packet` transmitter (AFSK 1200 or GFSK/GMSK/FSK 9600 AX.25), SSTV (mode `SSTV`, or the ISS on 145.800 MHz, using the `sstv` decoder) or FM voice (`nfm`, recorded). With several usable downlinks it prefers packet, then SSTV, then FM. Of overlapping passes it takes the highest maximum elevation; the others are logged as skipped.
- **Radio ownership.** If the radio is idle (no survey, scanner, scan or recording, no decoder; a VFO the user is listening on is fine) the autopilot takes it from AOS minus 30 s to LOS plus 10 s, then restores the previous tuning, VFO and decoder. If something is running, the pass is skipped with a reason, unless `preempt` is true: then the autopilot stops the survey, scanner or decoder and **restores only the tuning, VFO and decoder** afterwards; a preempted survey or scanner is **not restarted**. During a pass, manual tune/demod/decoder, survey and scanner starts return 409 `autopilot pass in progress`.
- **Doppler.** The center frequency is fixed at `downlink - sample_rate/4` for the whole pass; the decoder channel and the VFO (when listening) are retuned every second to `downlink + doppler(t)`, computed with SGP4 for the receiver.
- **Recordings.** Each pass goes to `<data dir>/passes/<YYYYmmdd-HHMMSS>_<norad>_<name>/` (a numeric suffix on collisions) with:
  - `pass.json`: the history entry, plus `started`, `ended` (Unix seconds), `manual` (true for `POST /api/autopilot/run`), `iq` (file name), `downlink_hz` and `baud`;
  - `packets.jsonl`: decoded packets (packet passes), one JSON event `data` per line;
  - `pass.wav`: FM audio (voice downlinks);
  - `image-NN.png`: SSTV pictures (copied from the SSTV directory);
  - a `.cu8` raw IQ file when `record_iq` is on (large, off by default).

  Fetch these files with [`GET /api/files/passes/{dir}/{name}`](#get-apifilespassesdirname); the `dir` fields of API responses name the directory relative to `passes/`. SSTV pictures decoded during a pass are also available under `/api/files/sstv/`.
- **Live audio.** With `listen` (default true) the VFO follows the downlink with Doppler correction (NFM, squelch off), so you hear the pass on `/ws/audio`.

### GET /api/autopilot

```json
{"enabled": true, "min_elevation": 20, "satellites": [], "preempt": false, "listen": true, "record_iq": false,
 "receiver_set": true, "datasets_ok": true,
 "current": {"norad": 12345, "name": "EXAMPLESAT-1", "aos": 1795300000, "los": 1795300600, "max_elevation_deg": 62,
             "downlink_hz": 145800000, "decoder": "sstv", "doppler_hz": -2310, "elevation_deg": 41.2, "azimuth_deg": 201.0,
             "packets": 0, "images": 1, "dir": "20261121-151200_12345_EXAMPLESAT-1"},
 "next": [{"norad": 12345, "name": "EXAMPLESAT-1", "aos": 1795303600, "los": 1795304200, "max_elevation_deg": 35,
           "downlink_hz": 436500000, "decoder": "packet", "baud": 9600, "will_run": true, "reason": ""}],
 "history": [{"norad": 12345, "name": "EXAMPLESAT-1", "aos": 1795290000, "los": 1795290600, "max_elevation_deg": 48,
              "downlink_hz": 436500000, "decoder": "packet", "baud": 9600, "packets": 14, "images": 0, "audio": "pass.wav",
              "dir": "20261121-120000_12345_EXAMPLESAT-1", "state": "done", "reason": "",
              "started": 1795289970.2, "ended": 1795290610.5}]}
```

- `datasets_ok` is true when both the `satnogs` and `tle` datasets are loaded; `receiver_set` when a receiver location is configured. Without both, `next` is empty.
- `current` is `null` unless a pass is being handled. `decoder` is `packet`, `sstv` or `nfm`.
- `next` covers the next 12 hours and **includes passes that will not run** (`will_run: false`, with a `reason` such as "autopilot disabled", "overlaps EXAMPLESAT-2 (max elevation 71°)" or "radio busy: decoder rtl_433 running"). `will_run: true` entries have an empty `reason`.
- `history` keeps the last 50 passes and survives restarts (it is read back from the pass directories). Entry `state`: `running` | `done` | `stopped` | `error`; `reason` explains skips and errors. `audio` and `iq` are file names inside the pass directory (empty or omitted when absent); `started`, `ended`, `manual` are omitted when 0/false.

### PUT /api/autopilot

Merge update of any of `enabled` (bool), `min_elevation` (number), `satellites` (integer array), `preempt` (bool), `listen` (bool), `record_iq` (bool). Persisted in the local config under `autopilot`. Returns the state. Errors: 400 for invalid JSON or wrongly typed values.

### POST /api/autopilot/run

`{"norad": 12345}` handles that satellite's **current** pass right now (it must be above the horizon), regardless of elevation or the satellite list. For trying things out. Returns **202** with the `current` object. Errors: 400 for a missing `norad` (`norad is required`), a satellite that is not above the horizon, one without a usable downlink, or missing satellite data (`no satellite data (set the receiver location and fetch the metadata)`); 409 when the radio is busy (`radio busy`, or a message naming what is running) and `preempt` is off.

### POST /api/autopilot/stop

Ends the current pass early (history state `stopped`) and returns the autopilot state. Also 200 when no pass runs.

## Transcription

sdrd can turn voice transmissions into text with [whisper.cpp](https://github.com/ggerganov/whisper.cpp) running on the same machine. No audio or text leaves the computer.

**Setup.** `brew install whisper-cpp`, then `make whisper-model` (downloads `ggml-small.en.bin`, about 490 MB, to `~/.local/share/sweep/models`). `sdrd` starts `whisper-server` as a child process on `127.0.0.1` (private port, `-t 4 -sns -l en`, model loaded once) **on demand** when the first clip arrives, and stops it on exit. Daemon flags:

| Flag | Default | Meaning |
|---|---|---|
| `-transcribe` | `auto` | `auto` (on when the model file exists), `on`, `off`. `PUT /api/transcribe` overrides it and is saved in the config. |
| `-whisper-model` | `~/.local/share/sweep/models/ggml-small.en.bin` | Model file |
| `-whisper-bin` | `whisper-server` | Server binary |

**What is transcribed.**
- Every transmission the **scanner** locks onto. The clip starts 0.3 s before the transmission's (back-dated) start and runs to its end without the hang time. The audio before the lock comes from about 2 s of buffered raw IQ (kept only while the scanner runs with transcription on, dropped on a retune, so a transmission right after a window change has less pre-roll), demodulated with the scanner's mode and bandwidth, so the first words are no longer cut off.
- **Survey listen holds** (`listened` steps).
- On request through `POST /api/transcribe`.

Clips shorter than 0.5 s and near-silent clips are skipped.

**Processing.** Clips are resampled to 16 kHz mono 16-bit, band-passed 300-3400 Hz, normalized, and stored as `<data-dir>/clips/<YYYYmmdd>/<HHMMSS>_<freq>.wav` (`-2`, `-3`... on a name collision). Each clip is POSTed to the local whisper-server `/inference` (multipart `file`, `response_format=json`, `temperature=0`) with an initial prompt of band-specific vocabulary (aviation, ham, FRS, marine). Clips are transcribed one at a time in order; the queue holds 50 and drops the oldest, logging it. A filter marks results `empty: true` when they are known Whisper hallucinations on noise ("Thank you."), bracketed sound tags, repetition loops, or fewer than 0.4 characters per second for clips of 3 s or more. **Retention:** the newest 500 clips or 7 days, whichever is smaller; older clips and transcripts are deleted. Transcripts are kept in memory and in `clips/index.jsonl`.

**Events.** `/ws/events`, `decoder: "transcribe"`, `kind: "message"`:

```json
{"id": "20261121-151248_122900000", "source": "scanner", "freq_hz": 122900000, "label": "MULTICOM",
 "mode": "am", "start": 1795295568.2, "duration_s": 3.6, "text": "Example traffic, left downwind runway one seven",
 "empty": false, "model": "small.en", "latency_s": 0.6, "url": "/api/files/clips/20261121/151248_122900000.wav"}
```

`source` is `scanner`, `survey` or `manual`.

### GET /api/transcripts

Returns a JSON **array** of transcript entries (shape above), newest first. Always 200, with `[]` when transcription is unavailable or nothing matches.

| Query | Type | Default | Notes |
|---|---|---|---|
| `since` | number | 0 | Only entries whose `start` (Unix seconds) is greater. Not an event id. 400 if not a number. |
| `limit` | integer | 100 | 1 to 1000. |
| `freq_hz` | integer | 0 (any) | Exact frequency match, 0 to 2e9. |
| `q` | string | | Case-insensitive substring match on `text` and `label`. |

### GET /api/transcribe

```json
{"enabled": true, "model": "small.en", "server": "stopped", "queue": 0, "last_error": ""}
```

`server` is `running`, `stopped` or `error`. When transcription is not available at all: `enabled` false, `server` `stopped`, `last_error` `transcription is not available`.

### PUT /api/transcribe

`{"enabled": true|false}` (required). Saved in the config under `transcribe`. Returns the status. Errors: 400 without a boolean `enabled`, or if switching on fails (for example the model is missing); 409 when transcription is not available.

### POST /api/transcribe

Transcribes now and **blocks** until the result is ready. Give one of:

| Field | Type | Notes |
|---|---|---|
| `seconds` | number | 1 to 60. Records the current listening VFO (its frequency and mode) for that long. Needs the VFO on (`POST /api/demod`). |
| `path` | string | A WAV file inside the data directory (absolute, or relative to it, for example `clips/20261121/151248_122900000.wav`). Anything outside it is a 400. |

Returns the transcript entry (200), or `{"text": "", "empty": true, "skipped": "<reason>"}` when nothing was transcribed (`shorter than 0.5 s`, `near-silent audio`, `queue full`). Errors: 400 for neither field or an invalid one, or a bad `path` (outside the data dir, missing, not a usable WAV); 409 when transcription is off or not available, the VFO is off, or the audio changed while recording.

### GET /api/files/clips/{date}/{name}

A recorded clip as `audio/wav` (with `nosniff`). `date` must be 8 digits (`YYYYmmdd`) and `name` must match `HHMMSS_<freq>[-N].wav`; otherwise (or when transcription is unavailable) a plain-text 404. A missing file gives a JSON 404. Symbolic links are not followed.

**sdrctl:** `sdrctl transcripts [-q text] [-freq 122.9M] [-limit 100] [-follow]`, `sdrctl transcribe <seconds>`, `sdrctl transcribe <WAV path>`, `sdrctl transcribe on|off`, `sdrctl transcribe status`.

**Privacy.** Clips can contain other people's transmissions. They stay in the data directory on this machine, are subject to the retention rule, and are never uploaded by sdrd. Remember that the API serves them to anyone who can reach it (see [Security](#security)).

## WebSocket streams

All three endpoints are `GET` upgrades to WebSocket, server to client only. Messages sent by the client are read and discarded. Use `ws://127.0.0.1:8073/ws/...`. See [Security](#security) for origin rules.

General behavior:

- **Slow clients lose data, they do not slow the daemon.** Each client has a small send queue (8 messages for spectrum, 64 for audio, 256 for events). When it is full, new binary frames (or events) are dropped for that client. Control (`meta`) messages replace the oldest queued message instead. Write timeout is 5 seconds; a client that stops reading is disconnected.
- Reconnect on close; state is re-sent on connect.
- No application-level ping is required. Standard WebSocket ping/pong is handled by the library.

### WS /ws/spectrum

1. **Text frame** (JSON), sent right after connecting (if the daemon already has one) and again whenever tuning or scaling changes:

   ```json
   {"type": "meta", "center_hz": 433920000, "sample_rate": 2048000,
    "bins": 2048, "min_db": -90, "max_db": -20, "fps": 20}
   ```

2. **Binary frames**, about `fps` per second. Each frame is exactly `bins` bytes:

   | Offset | Length | Meaning |
   |---|---|---|
   | `i` (0 to `bins-1`) | 1 byte | `uint8` power of FFT bin `i` |

   - Bin `i` is centered at `center_hz - sample_rate/2 + i * sample_rate/bins` Hz. Ascending frequency, DC in the middle (bin `bins/2` is `center_hz`).
   - Value `v` maps to `dB = min_db + v * (max_db - min_db) / 255`. Values clip at 0 and 255. dB is relative, not calibrated. `min_db`/`max_db` are daemon settings (`-min-db`, `-max-db`; defaults -90 and -20).
   - The DC bin is already masked by the server.

**Clients must drop binary frames until they have received a `meta` frame**, and must re-read the `meta` whenever a new one arrives: a frame is interpreted with the most recent `meta` (frames in flight during a retune can still belong to the old tuning for a moment). The number of bytes in a frame always equals `bins`; if it does not, ignore the frame.

### WS /ws/audio

Audio from the listening demodulator (see [POST /api/demod](#post-apidemod)), or decoded digital voice while the `dsd` decoder runs.

- **Text frames** (JSON):
  - `{"type": "audio", "rate": 32000, "mode": "nfm"}` on connect and whenever the rate or mode changes. Before any demodulator is set: `{"type":"audio","rate":0,"mode":"off"}`. While the `dsd` decoder runs: `mode: "dsd"`, `rate: 8000`.
  - `{"type": "level", "db": -71.3, "open": true}` about 10 times per second while the demodulator runs. `db` is the channel power, `open` is whether the squelch is open.
- **Binary frames**: mono **signed 16-bit little-endian PCM** at `rate` samples per second, about 20 ms per frame (for example 640 samples = 1280 bytes at 32 kHz). Byte `2k` and `2k+1` hold sample `k`, low byte first. No frames are sent while the mode is `off`. Frames are silent (zeros) while the squelch is closed.

Treat `rate` from the latest `audio` message as authoritative; it can change when you change mode or the sample rate. Buffer about 100-200 ms before playing to ride out jitter.

### WS /ws/events

One JSON **text** frame per decoder event, the same [envelope](#event-envelope) as `GET /api/events`. On connect the **last 100 events** from the ring buffer are replayed (oldest first), then new events follow live. Use `id` to de-duplicate if you also poll. Events from the `survey` pseudo-decoder (progress lines) are included.

## Decoders and event shapes

At most one decoder runs at a time. Starting one retunes the radio to what it needs; the spectrum keeps running (IQ is fanned out to the decoder). The decoders call external open-source tools that must be installed on the machine running `sdrd`; if one is missing, `POST /api/decoder` returns 400 with a message naming it.

| decoder | tunes to | how |
|---|---|---|
| `rtl_433` | current center if inside 433.05-434.79, 863-870 or 902-928 MHz, else 433.92 MHz; 1.024 MS/s | IQ piped to `rtl_433` |
| `adsb` | 1090 MHz, 2.4 MS/s | IQ piped to `dump1090` |
| `aprs` | 144.39 MHz (US) or 144.8 MHz (EU) via an internal NFM demod | audio piped to `direwolf` |
| `ais` | 161.975 MHz via an internal NFM demod | audio piped to `direwolf -B AIS` |
| `meters` | 914 MHz, 2.4 MS/s | IQ piped to `rtl_433` with the ERT SCM/SCM+/IDM and Neptune R900 protocols; only your `meter_ids` are published |
| `uat` | 978 MHz, 2.083334 MS/s | `dump978`; aircraft via `uat2json`, FIS-B text via `uat2text` |
| `radiosonde` | `freq_hz`, or the strongest non-spur carrier found in 400-406 MHz; 2.4 MS/s, 15 kHz raw NFM | audio fanned out to the rs1729 decoders `rs41mod`, `dfm09mod`, `m10mod`, `lms6Xmod` |
| `dsd` | `freq_hz` (default: the VFO), **amateur bands only**; 2.4 MS/s | audio piped to `dsd-fme`; decoded voice replaces `/ws/audio` (`mode: "dsd"`, `rate: 8000`) and the VFO is turned off |
| `packet` | `freq_hz` (default: the VFO) via an internal 25 kHz NFM demod | audio piped to `direwolf -B 1200` or `-B 9600` (AX.25: packet radio, amateur satellites) |
| `sstv` | `freq_hz` (default 145.8 MHz, the ISS downlink) via a private 25 kHz raw NFM demod | native Go slow-scan TV decoder, no external tool; see [SSTV and files](#sstv-and-files) |

### Event envelope

```json
{"id": 17, "ts": 1795286400.12, "decoder": "rtl_433", "kind": "message", "data": {}}
```

| Field | Type | Notes |
|---|---|---|
| `id` | integer | Increasing, starting at 1 per `sdrd` process. |
| `ts` | number | Unix seconds with microsecond fraction. |
| `decoder` | string | A decoder name, or `survey`. |
| `kind` | string | `message` (one decoded item), `snapshot` (periodic table), `log` (diagnostics). |
| `data` | object | Shape depends on decoder and kind, below. |

Fields marked "when known" are absent otherwise. Unknown extra fields may appear; ignore what you do not use.

- **any decoder, kind `log`**: `{"line": "..."}`. Tool stderr worth showing, startup problems, and `stopped: <error>` when a decoder dies.
- **`rtl_433`, `message`**: the JSON object `rtl_433` printed, as is (`model`, `id`, `channel`, `temperature_C`, `humidity`, `rssi`, ...). Fields vary by sensor model.
- **`adsb`, `snapshot`**, once per second: `{"aircraft": [{"hex": "a1b2c3", "flight": "TEST123", "alt_baro": 35000, "gs": 450.2, "track": 270.1, "lat": 12.34, "lon": 56.78, "squawk": "1200", "seen": 0.4, "messages": 120}]}`. `hex` is the ICAO address; `alt_baro` in feet, `gs` in knots, `track` in degrees, `seen` in seconds since the last message. `flight`, `alt_baro`, `gs`, `track`, `lat`, `lon`, `squawk` only when known. [Metadata](#metadata) adds, when known, `country`, `registration`, `type` and `year` (from the hex address) and `airline` (`{"icao": "TST", "name": "Example Airways", "radio": "EXAMPLE"}`) plus `flight_number` (from the callsign).
- **`uat`, `snapshot`**, once per second: `{"aircraft": [...]}` with the same field names as `adsb` (`hex`, `flight`, `alt_baro`, `gs`, `track`, `lat`, `lon`, `squawk`, `seen`, `messages`), plus `vert_rate` and `addrtype` when present, and the same metadata enrichment as `adsb`.
- **`uat`, `message`**: FIS-B text report `{"kind": "fisb", "product": "413 (...)", "report_type": "METAR", "location": "KXYZ", "time": "211953Z", "text": "METAR KXYZ 211953Z ..."}`. `report_type` is `METAR`, `TAF`, `WINDS`, `NOTAM`, ...
- **`aprs`, `message`**: `{"raw": "N0CALL-9>APRS,WIDE2-1:!1234.50N/05646.75E>...", "from": "N0CALL-9", "to": "APRS", "path": ["WIDE2-1"], "info": "!1234.50N/05646.75E>...", "lat": 12.575, "lon": 56.779, "comment": "..."}`. `lat`, `lon`, `comment` only for uncompressed position reports.
- **`packet`, `message`**: same shape as `aprs`, plus `satellite` (`{"norad": 12345, "name": "EXAMPLESAT-1"}`) when a satellite with a downlink within 15 kHz of the decoder frequency is above the horizon (needs the receiver location and the metadata datasets).
- **`ais`, `message`**: `{"nmea": "!AIVDM,1,1,,A,...", "type": 1, "mmsi": 123456789, "lat": 12.34, "lon": 56.78, "sog": 12.3, "cog": 87.0, "heading": 88, "name": "EXAMPLE", "callsign": "N0CALL", "shiptype": 70}`. `nmea` and `type` and `mmsi` are always present; the rest only for message types that carry them (positions: `lat`, `lon`, `sog`, `cog`, `heading`; static data: `name`, `callsign`, `shiptype`).
- **`meters`, `message`**: `rtl_433`'s JSON for **your own meters only** (`model`, `id`, `consumption_data`, ...). Kind `snapshot` every 5 seconds: `{"own_ids": 1, "other_meters_heard": 23}`. Other meters are only counted.
- **`radiosonde`, `message`**: the rs1729 decoder JSON plus `freq_hz`. Common fields: `type` (`RS41`, `DFM`, `M10`, `LMS6`), `id` (serial), `frame`, `datetime`, `lat`, `lon`, `alt` (m), `vel_h` (m/s), `heading` (deg), `vel_v` (m/s climb rate), `sats`, and when available `temp` (degC), `humidity` (%), `pressure` (hPa), `batt` (V).
- **`sstv`**: `progress` about once per second while receiving: `{"mode": "PD120", "lines": 112, "total_lines": 496, "url": "/api/files/sstv/20261121-151200_pd120.png"}` (the PNG is rewritten as lines arrive). `image` when a picture is finished: `{"mode": "PD120", "width": 640, "height": 496, "lines": 496, "url": "/api/files/sstv/...png", "path": "/abs/path/...png", "started": 1795300000, "duration_s": 126.4, "freq_hz": 145800000, "satellite": {"norad": 12345, "name": "EXAMPLESAT-1"}}` (`satellite` only when a matching satellite is above the horizon). `log` lines report "listening on ..." and "receiving PD120".
- **`scanner`, `message`**: one per finished transmission, `{"freq_hz": 119250000, "label": "", "start": 1795300000.4, "duration_s": 4.2, "peak_db": -38.0, "mode": "am"}`.
- **`autopilot`**: `log` lines ("AOS ...", "LOS ...", and skipped passes with their reason), and `pass` at LOS with the history entry of [GET /api/autopilot](#get-apiautopilot).
- **`dsd`, `message`**: one event per call (repeats of the same call within 3 s are merged): `{"mode": "DMR", "tgt": 3100, "src": 1234567, "color_code": 1, "encrypted": true, "line": "..."}`. Fields appear when `dsd-fme` printed them; `line` is always present, `encrypted` only when `true`. Encrypted traffic is never decrypted.

## Survey contract

A survey runs a list of short steps with the current antenna and reports what each found. It owns the radio while it runs: a running decoder is stopped, scan/record/another survey are refused with 409, and `POST /api/decoder` returns 409. Afterwards `sdrd` restores the previous center frequency, sample rate, gain, VFO and decoder.

**State** (`GET /api/survey`, and the 202 response of `POST /api/survey`):

```json
{"id": 3, "state": "running", "antenna": "jpole", "region": "us", "started": 1795290000.5,
 "elapsed_s": 41.2, "estimated_s": 260, "listen": true, "listen_seconds": 8,
 "steps": [
   {"id": "scan:2m", "kind": "scan", "band": "2m", "label": "2 m amateur: band scan",
    "freq_lo_hz": 144000000, "freq_hi_hz": 148000000,
    "state": "done", "seconds": 4.1, "found": 6, "summary": "6 carriers, strongest 146.520 MHz (+31 dB)",
    "peaks": [{"freq_hz": 146520000, "db": -41.0, "snr_db": 31.0}],
    "listened": {"freq_hz": 146520000, "mode": "nfm", "seconds": 8.0, "level_db": -52.3}},
   {"id": "aprs", "kind": "decoder", "decoder": "aprs", "band": "2m", "label": "APRS (144.390 MHz)",
    "freq_hz": 144390000, "state": "running", "seconds": 12.0, "found": 3, "summary": "3 stations",
    "samples": [{"raw": "...", "from": "N0CALL-9"}]},
   {"id": "ais", "kind": "decoder", "decoder": "ais", "label": "AIS (161.975 MHz)", "freq_hz": 161975000, "state": "pending"}
 ]}
```

- Survey `state`: `idle` (never run) | `running` | `done` | `stopped` | `error` (`error` carries a message).
- Step `state`: `pending` | `running` | `done` | `skipped` | `error`. `skipped` has `reason`; `error` has `error`.
- Step `kind`: `scan` (scans the band edges, dwell 500 ms) or `decoder`. Decoder steps have `decoder`; scan steps have `freq_lo_hz`/`freq_hi_hz` and `peaks` (crystal spurs are left out).
- `found` is the number of distinct things heard: aircraft (adsb/uat: unique `hex`), sensors (rtl_433: unique model+id+channel), stations (aprs/packet: unique `from`), vessels (ais: unique `mmsi`), sondes (unique `id`), calls (dsd: unique mode+tgt+src), carriers (scan). FIS-B weather reports are counted separately in the summary text.
- `samples` holds up to 3 representative event `data` objects (the latest per distinct thing). For `meters` it contains your own meters only.
- **Plan building.** When `steps` is absent, `sdrd` builds the plan: for every band in the region whose `tune_mhz` fits the antenna as `good` (or `fair` with `include_fair`) and is not `hf` (unless the antenna is `hfwire`): one `scan:<band id>` step, and, if the band has a `decoder`, one decoder step with id `<decoder>`, or `<decoder>@<band id>` when the same decoder appears for several bands (`rtl_433@ism433`, `rtl_433@ism915`). `dsd` steps use the strongest non-spur carrier found by the preceding scan of that band. `packet` steps are always `skipped` (they need a satellite pass). `meters` runs always; without configured `meter_ids` it only reports how many other meters were heard. Read the `steps` of the 202 response to learn the ids and the plan.
- <a id="listen-holds"></a>**Listen holds.** With `listen` on (the default), a `scan` step on a voice band (band `mode` is `nfm`, `am`, `usb` or `lsb`) that found a non-spur carrier is followed by a hold: the listening demodulator is set to the strongest carrier for `listen_seconds`, so the audio goes out on [`/ws/audio`](#ws-audio) for any connected client. The carrier frequency is rounded to a channel grid: 100 Hz for `usb`/`lsb`; for `nfm`/`am`, 1 kHz below 30 MHz and 5 kHz at 30 MHz and above (6.25 kHz when the band's catalog channels lie on that raster). While `listen` is on, the demodulator is off during the survey except during holds (silence rather than a parked frequency); the previous VFO is restored at the end like everything else. Related fields:
  - `GET /api/survey` echoes `listen` and `listen_seconds` (the latter omitted when 0), and has `listening` only **during** a hold: `{"step": "scan:2m", "freq_hz": 146520000, "mode": "nfm", "label": "...", "seconds": 8, "started": 1795290012.5}` (`label` omitted when unknown, `seconds` is the planned hold).
  - After the hold, the scan step gains `listened`: `{"freq_hz": 146520000, "mode": "nfm", "seconds": 8.0, "level_db": -52.3, "label": "..."}`. `level_db` is the mean demodulator channel level over the hold and `seconds` the actual hold (a stop can cut it short). The step's own `seconds` **excludes** the hold.
  - `estimated_s` adds `listen_seconds` for every voice-band scan step when `listen` is on.
- Each finished step is also published on `/ws/events` as `{"decoder": "survey", "kind": "log", "data": {"line": "<label>: <summary>"}}`.
- Poll `GET /api/survey` about once per second until `state` is no longer `running`. `POST /api/survey/stop` ends it early.

## Client examples

### curl

```bash
BASE=http://127.0.0.1:8073

curl -s $BASE/api/status
curl -s -X POST $BASE/api/tune  -d '{"freq_hz": 446000000, "sample_rate": 2048000, "gain_db": 29.7}'
curl -s "$BASE/api/peaks?n=5&min_snr=12"
curl -s "$BASE/api/spectrum?bins=64&agg=max"
curl -s -X POST $BASE/api/demod -d '{"mode":"nfm","freq_hz":446006250,"squelch_db":-60}'
curl -s -X POST $BASE/api/decoder -d '{"decoder":"rtl_433"}'
curl -s "$BASE/api/events?since=0&limit=20"
curl -s -m 120 -X POST $BASE/api/scan -d '{"start_hz":430000000,"stop_hz":440000000,"dwell_ms":500}'
curl -s -m 90  -X POST $BASE/api/record -d '{"seconds":5}'
curl -s -X PUT $BASE/api/config -d '{"receiver":{"lat":12.34,"lon":56.78}}'
curl -s -X POST $BASE/api/survey -d '{"seconds":20,"steps":["scan:2m","aprs"]}'
curl -s $BASE/api/survey
```

Add `-w '%{http_code}\n'` to see the status code; error bodies are `{"error": "..."}`.

### JavaScript (browser): log the peak frequency

Run this from a page served from `localhost` / `127.0.0.1` (or by `sdrd` itself), see [Security](#security). It draws nothing; it logs the strongest bin about once per second.

```js
const ws = new WebSocket("ws://127.0.0.1:8073/ws/spectrum");
ws.binaryType = "arraybuffer";
let meta = null;
let n = 0;

ws.onmessage = (ev) => {
  if (typeof ev.data === "string") {          // text frame: JSON
    const m = JSON.parse(ev.data);
    if (m.type === "meta") meta = m;           // re-read on every retune
    return;
  }
  if (!meta) return;                           // drop frames until we have a meta
  const bins = new Uint8Array(ev.data);
  if (bins.length !== meta.bins) return;       // stale frame from before a retune
  if (++n % meta.fps !== 0) return;            // roughly once per second

  let best = 0;
  for (let i = 1; i < bins.length; i++) if (bins[i] > bins[best]) best = i;
  const hz = meta.center_hz - meta.sample_rate / 2 + best * (meta.sample_rate / meta.bins);
  const db = meta.min_db + bins[best] * (meta.max_db - meta.min_db) / 255;
  console.log(`peak ${(hz / 1e6).toFixed(4)} MHz at ${db.toFixed(1)} dB`);
};
ws.onclose = () => console.log("closed; reconnect if needed");
```

### Python: tune, start a decoder, print events

Uses only the standard library for REST (`urllib`) and the third-party **`websockets`** package (`pip install websockets`) for the event stream. Requires the `rtl_433` tool on the machine running `sdrd`.

```python
#!/usr/bin/env python3
"""Tune to 433.92 MHz, start the rtl_433 decoder and print events."""
import asyncio
import json
import urllib.error
import urllib.request

import websockets  # third-party dependency: pip install websockets

BASE = "http://127.0.0.1:8073"
WS = "ws://127.0.0.1:8073"


def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        raise SystemExit(f"{method} {path}: HTTP {e.code}: {e.read().decode()}")


async def main():
    call("POST", "/api/tune", {"freq_hz": 433_920_000, "gain_db": 29.7})
    print(call("POST", "/api/decoder", {"decoder": "rtl_433"})["decoder"])
    async with websockets.connect(WS + "/ws/events") as ws:
        async for message in ws:
            ev = json.loads(message)
            print(ev["id"], ev["decoder"], ev["kind"], json.dumps(ev["data"]))


try:
    asyncio.run(main())
except KeyboardInterrupt:
    call("POST", "/api/decoder", {"decoder": "none"})
```

### sdrctl equivalents

`sdrctl` is a thin JSON client for the same API (`go build ./cmd/sdrctl`, binary in `bin/` after `make`). It talks to `127.0.0.1:8073` unless `SDRD_ADDR` or `-addr host:port` says otherwise. Frequencies accept suffixes: `146.52M`, `100k`, `1.09G`.

| Task | REST | sdrctl |
|---|---|---|
| Status | `GET /api/status` | `sdrctl status` |
| Tune | `POST /api/tune` | `sdrctl tune 446.1M -gain 29.7` (also `-rate 2.4M`, `-gain auto`) |
| Gain only / rate only | `POST /api/tune` | `sdrctl gain auto`, `sdrctl rate 2.4M` |
| Listen | `POST /api/demod` | `sdrctl listen 162.55M -mode nfm -squelch -60`; `sdrctl demod off` |
| Peaks | `GET /api/peaks` | `sdrctl peaks -n 5 -min-snr 10` |
| Spectrum | `GET /api/spectrum` | `sdrctl spectrum -bins 64` |
| Bands | `GET /api/bands` | `sdrctl bands -region eu`; `sdrctl band air` (tunes and starts the band's default mode) |
| Decoder | `POST /api/decoder` | `sdrctl decoder rtl_433`; `sdrctl decoder packet -freq 437.8M -baud 9600`; `sdrctl decoder none` |
| Events | `GET /api/events` | `sdrctl events -decoder rtl_433 -limit 20` |
| Scan | `POST /api/scan` | `sdrctl scan 430M 440M -dwell 500` |
| Record | `POST /api/record` | `sdrctl record 5` |
| Location, meters, antenna | `PUT /api/config` | `sdrctl location 12.34 56.78`; `sdrctl meters 12345678`; `sdrctl antenna jpole` |
| Metadata | `GET /api/meta`, `/api/meta/aircraft/{hex}`, `/api/meta/callsign/{cs}` | `sdrctl meta`; `sdrctl meta aircraft a1b2c3`; `sdrctl meta callsign TST123` |
| Satellites | `GET /api/satellites/...` | `sdrctl sats passes -hours 24 -min-el 10`; `sdrctl sats overhead`; `sdrctl sats match 436.5M -tol 15k` |
| Scanner | `POST /api/scanner`, `GET /api/scanner`, `/api/scanner/stop`, `/api/scanner/lockout` | `sdrctl scanner air`; `sdrctl scanner 118M 137M -mode am`; `sdrctl scanner status`; `sdrctl scanner lockout 132.15M [-remove]`; `sdrctl scanner stop` (`-follow` prints one line per transmission) |
| Transcription | `GET /api/transcripts`, `/api/transcribe` | `sdrctl transcripts -q runway -follow`; `sdrctl transcribe 10`; `sdrctl transcribe on` |
| Autopilot | `GET/PUT /api/autopilot`, `/api/autopilot/run`, `/api/autopilot/stop` | `sdrctl autopilot on -min-el 20 -sats 12345,67890`; `sdrctl autopilot status`; `sdrctl autopilot run 12345`; `sdrctl autopilot stop` |
| Survey | `POST /api/survey` and polling | `sdrctl survey -seconds 20 scan:2m aprs` (also `-listen=false`, `-listen-seconds 8`); `sdrctl survey status`; `sdrctl survey stop` |

`sdrctl` prints the response JSON on stdout and exits non-zero on an HTTP error (body on stderr). The WebSocket streams have no CLI equivalent.

## Privacy notes

- **Receiver location and meter IDs live only in the local config file** (default `~/.config/sweep/config.json`, written with mode 0600 in a 0700 directory). `sdrd` makes no outbound network calls and never sends them anywhere. They are readable and writable over the unauthenticated API (see [Security](#security)), so treat network exposure accordingly.
- **The `meters` decoder never publishes other meters.** Only the IDs in your `meter_ids` list appear in `message` events and survey samples. Other meters are counted by a salted, in-memory hash (so the `other_meters_heard` count works) and their IDs are never stored or published.
- **Encrypted digital voice is never decrypted.** The `dsd` decoder mutes encrypted calls and reports only the metadata it printed, with `"encrypted": true`. It is limited to amateur radio bands.
- **Metadata is looked up offline.** `sdrd` makes no per-aircraft, per-station or per-satellite requests; datasets are bulk downloads you fetch yourself (see [Metadata](#metadata)). The FAA registry is loaded without owner names or addresses. Your receiver location is used only locally for satellite geometry.
- **Data directory.** SSTV pictures and autopilot pass records (decoded packets, FM audio, optional raw IQ) are written under `sdrd -data-dir` (default `~/.local/share/sweep`) and are readable through `GET /api/files/...` by anyone who can reach the API.
- Decoded data in the event stream (aircraft positions, AIS vessels, APRS stations, sensor readings) is whatever was received over the air; consider what your own application stores and republishes.
- `POST /api/record` reveals the server's filesystem path in its response and writes raw IQ files that can contain third-party transmissions.
- The examples in this document use obviously fake values (coordinates 12.34 / 56.78, callsign N0CALL, meter ID 12345678).
