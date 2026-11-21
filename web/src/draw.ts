// Shared helpers for resampling bins to pixels and frequency axis math.

/** Resample uint8 bins to `width` pixels. Max when bins > width, nearest/linear otherwise. */
export function resample(bins: Uint8Array, width: number, out: Float32Array | Uint8Array): void {
  const n = bins.length;
  if (n >= width) {
    for (let x = 0; x < width; x++) {
      const a = Math.floor((x * n) / width);
      const b = Math.max(a + 1, Math.floor(((x + 1) * n) / width));
      let m = 0;
      for (let i = a; i < b; i++) {
        const v = bins[i]!;
        if (v > m) m = v;
      }
      out[x] = m;
    }
  } else {
    for (let x = 0; x < width; x++) {
      const p = (x * (n - 1)) / Math.max(1, width - 1);
      const i = Math.floor(p);
      const f = p - i;
      const v0 = bins[i]!;
      const v1 = bins[Math.min(n - 1, i + 1)]!;
      out[x] = v0 + (v1 - v0) * f;
    }
  }
}

/** "Nice" tick step (1/2/5 * 10^k) giving roughly `target` ticks across `span`. */
export function niceStep(span: number, target: number): number {
  const raw = span / Math.max(1, target);
  const p = 10 ** Math.floor(Math.log10(raw));
  const f = raw / p;
  return (f < 1.5 ? 1 : f < 3.5 ? 2 : f < 7.5 ? 5 : 10) * p;
}

export function fmtMHz(hz: number, decimals: number): string {
  return (hz / 1e6).toFixed(decimals);
}
