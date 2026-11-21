# sdrd protocol

`sdrd` listens on `127.0.0.1:8073` by default (`-addr`). The web UI dev server proxies `/api` and `/ws` to it.

## REST

### `GET /api/status`
```json
{
  "device": "Nooelec, NESDR SMArt v5, SN: 28139917",
  "tuner": "R820T",
  "gains_db": [0.0, 0.9, 1.4],
  "center_hz": 433920000,
  "sample_rate": 2048000,
  "gain_db": null,
  "fft_size": 2048,
  "fps": 20,
  "mode": "spectrum",
  "connected": true
}
```
`gain_db: null` means automatic gain.

### `POST /api/tune`
Body: any subset of `{"freq_hz": 433920000, "sample_rate": 2048000, "gain_db": 20.7}`. Send `"gain_db": null` for auto gain. Returns the new status.

### `GET /api/spectrum?bins=256&frames=20&agg=avg`
The last `frames` spectrum frames (20 = 1 s, max 40) aggregated (`avg` or `max`), max-pooled to `bins` bins, in dB. For agents that want to "see" the band without a WebSocket.
```json
{"center_hz": 452500000, "sample_rate": 2048000, "start_hz": 451539500, "bin_hz": 128000,
 "frames": 20, "agg": "avg", "floor_db": -73.3, "db": [-74.6, -73.0, ...]}
```
`db[i]` is centred at `start_hz + i * bin_hz`.

### `GET /api/peaks?n=10&min_snr=10&frames=20&agg=max`
Carriers at least `min_snr` dB above the median floor, strongest first, over recent frames (`max` by default so bursts are caught). The outer 5% at each edge is ignored (filter roll-off). `spur: true` marks peaks within 2 bins of a multiple of 28.8 MHz, the RTL2832U crystal, which are usually generated inside the dongle.
```json
{"center_hz": 452500000, "sample_rate": 2048000, "frames": 20, "agg": "max", "floor_db": -72.2,
 "peaks": [{"freq_hz": 452738000, "db": -43.1, "snr_db": 29.1}]}
```

## WebSocket `/ws/spectrum`

Server to client only (for now).

1. **Text frame** (JSON), sent on connect and again whenever tuning or scaling changes:
   ```json
   {"type": "meta", "center_hz": 433920000, "sample_rate": 2048000,
    "bins": 2048, "min_db": -40, "max_db": 60, "fps": 20}
   ```
2. **Binary frames**: exactly `bins` bytes, one `uint8` per FFT bin.
   - Bin `i` is at `center_hz - sample_rate/2 + i * sample_rate/bins` Hz (ascending frequency, DC in the middle).
   - Value `v` maps to `dB = min_db + v * (max_db - min_db) / 255`. dB is relative (dBFS-ish), not calibrated.
   - The DC bin is already masked by the server.

Clients should drop binary frames until they have received a `meta` frame.

## Band catalog: `GET /api/bands`

Returns `data/bands.json` verbatim (embedded in sdrd). Top level: `default_region`, `regions` (id → name), `bands[]`.
Each band: `id, name, regions[], lo_mhz, hi_mhz, edges?{region: [lo, hi]}` (region-specific edges override lo/hi),
`tune_mhz, hf` (true = needs HF direct sampling), `mode` (nfm|am|usb|lsb|none), `decoder` (rtl_433|adsb|aprs|ais|null),
`on_air, decode_with[], antenna, tx_license, starter, channels[]`. Each channel: `mhz, name, mode, regions?[], decoder?`.
A channel without `regions` applies to every region of its band.

## Demodulator (audio)

One listening demodulator ("VFO") runs on the IQ stream alongside the waterfall.

### `POST /api/demod`
Body: any subset of
```json
{"mode": "nfm", "freq_hz": 446006250, "bandwidth_hz": 12500, "squelch_db": -60}
```
- `mode`: `off | nfm | am | usb | lsb`. Defaults for `bandwidth_hz`: nfm 12500, am 10000, usb/lsb 2800 (applied when the mode changes and no bandwidth is given).
- `freq_hz`: the frequency to listen to (for usb/lsb: the suppressed-carrier dial frequency). If it is outside the middle 80% of the current span, sdrd retunes the center to `freq_hz - sample_rate/4` so the VFO never sits on the DC spike.
- `squelch_db`: channel power below this mutes audio; `null` disables squelch.
Returns the status. `GET /api/status` includes:
```json
"demod": {"mode": "nfm", "freq_hz": 446006250, "bandwidth_hz": 12500, "squelch_db": null, "audio_rate": 32000, "level_db": -71.3}
```
Tuning the center away so that the VFO falls outside the span moves the VFO to keep the same offset from center.

### WebSocket `/ws/audio`
Server to client.
- Text frames (JSON):
  - `{"type": "audio", "rate": 32000, "mode": "nfm"}` on connect and when rate or mode changes.
  - `{"type": "level", "db": -71.3, "open": true}` about 10 times per second (`open` = squelch open).
- Binary frames: mono signed 16-bit little-endian PCM at `rate`, about 20 ms per frame. No frames while mode is `off`.

## Decoders

At most one decoder runs at a time. Starting one retunes the radio to what it needs; the waterfall keeps running (IQ is fanned out to the decoder). Retuning the center while a decoder runs is allowed but will usually break decoding.

| decoder | tunes to | how |
|---|---|---|
| `rtl_433` | current center if inside 433.05-434.79 / 863-870 / 902-928 MHz, else 433.92 MHz; 1.024 MS/s | IQ piped to `rtl_433 -r cu8:-` |
| `adsb` | 1090 MHz, 2.4 MS/s | IQ piped to `dump1090 --ifile -` |
| `aprs` | 144.39 MHz (US) / 144.8 MHz (EU) via an internal NFM demod | audio piped to `direwolf` |
| `ais` | 161.975 MHz via an internal NFM demod | audio piped to `direwolf -B AIS` |

### `POST /api/decoder`
Body: `{"decoder": "rtl_433" | "adsb" | "aprs" | "ais" | "none", "region": "us"}` (`region` only matters for aprs). Returns the status. `GET /api/status` includes
```json
"decoder": {"name": "rtl_433", "running": true, "freq_hz": 433920000, "started": 1795286400, "events": 12, "error": ""}
```
(`name: "none"` when idle.)

### `GET /api/events?since=0&limit=200`
Recent decoded events (ring buffer of 1000), oldest first, with `id > since`.

### WebSocket `/ws/events`
Server to client, one JSON text frame per event. On connect the last 100 events are replayed.

Event envelope:
```json
{"id": 17, "ts": 1795286400.12, "decoder": "rtl_433", "kind": "message", "data": {}}
```
`data` by decoder:
- `rtl_433`, kind `message`: rtl_433's JSON object as-is (`model`, `id`, `channel`, `temperature_C`, `humidity`, `rssi`, ...).
- `adsb`, kind `snapshot`, once per second: `{"aircraft": [{"hex": "a1b2c3", "flight": "UAL123", "alt_baro": 35000, "gs": 450.2, "track": 270.1, "lat": 40.1, "lon": -74.2, "seen": 0.4, "rssi": -20.1, "messages": 120}]}` (fields absent when unknown).
- `aprs`, kind `message`: `{"raw": "N0CALL-9>APRS,WIDE2-1:!4903.50N/07201.75W>...", "from": "N0CALL-9", "to": "APRS", "path": ["WIDE2-1"], "info": "!4903.50N/07201.75W>...", "lat": 49.058, "lon": -72.029, "comment": "..."}` (`lat`/`lon`/`comment` only for uncompressed position reports).
- `ais`, kind `message`: `{"nmea": "!AIVDM,1,1,,A,...", "type": 1, "mmsi": 366123456, "lat": 37.8, "lon": -122.4, "sog": 12.3, "cog": 87.0, "heading": 88, "name": "..." , "callsign": "...", "shiptype": 70}` (fields present when the message type carries them).
- any decoder, kind `log`: `{"line": "..."}` for tool stderr worth showing (startup errors etc).
