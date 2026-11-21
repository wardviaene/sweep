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
