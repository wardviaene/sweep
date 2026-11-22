// Client-side auto-ranging for the waterfall (and optionally the spectrum trace).
// Frames arrive quantized over the server's fixed min_db..max_db; this tracks the noise
// floor and the top of the signal in dB and remaps the colormap between them.

export interface RangeSettings {
  auto: boolean;
  min: number; // manual dB
  max: number;
  contrast: number; // -1..1, 0 = linear; >0 darkens the floor further
  trace: boolean; // apply the range to the spectrum trace's dB axis
}

const KEY = "sweep.range";
const DEFAULTS: RangeSettings = { auto: true, min: -90, max: -20, contrast: 0, trace: false };

export function loadRange(): RangeSettings {
  try {
    const s = JSON.parse(localStorage.getItem(KEY) ?? "null") as Partial<RangeSettings> | null;
    if (s && typeof s === "object") return { ...DEFAULTS, ...s };
  } catch {
    /* ignore */
  }
  return { ...DEFAULTS };
}

export function saveRange(s: RangeSettings): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* ignore */
  }
}

const FLOOR_PCT = 0.2;
const TOP_PCT = 0.995;
const FLOOR_TAU = 2; // s
const TOP_RISE_TAU = 0.3;
const TOP_DECAY_TAU = 6;
const MIN_SPAN_DB = 20;
const FLOOR_OFFSET_DB = 4; // max-pooled noise sits above the 20th percentile, so put "black" above it
const TOP_MARGIN_DB = 3;

/** Tracks floor/top in dB from a stream of uint8 frames. No allocation per frame. */
export class AutoRange {
  private hist = new Uint32Array(256);
  private floor = NaN; // dB
  private top = NaN;
  private last = 0;

  reset(): void {
    this.floor = NaN;
    this.top = NaN;
  }

  private pct(n: number, p: number): number {
    const target = n * p;
    let acc = 0;
    for (let i = 0; i < 256; i++) {
      acc += this.hist[i]!;
      if (acc >= target) return i;
    }
    return 255;
  }

  update(frame: Uint8Array, minDb: number, maxDb: number, now: number): void {
    const h = this.hist;
    h.fill(0);
    for (let i = 0; i < frame.length; i++) h[frame[i]!]!++;
    const k = (maxDb - minDb) / 255;
    const f = minDb + this.pct(frame.length, FLOOR_PCT) * k;
    const t = minDb + this.pct(frame.length, TOP_PCT) * k;
    const dt = Math.min(1, Math.max(0.001, now - this.last));
    this.last = now;
    if (Number.isNaN(this.floor)) {
      this.floor = f;
      this.top = t;
      return;
    }
    this.floor += (f - this.floor) * (1 - Math.exp(-dt / FLOOR_TAU));
    const tau = t > this.top ? TOP_RISE_TAU : TOP_DECAY_TAU;
    this.top += (t - this.top) * (1 - Math.exp(-dt / tau));
  }

  /** Current display range in dB, or null before the first frame. */
  range(): [number, number] | null {
    if (Number.isNaN(this.floor)) return null;
    const lo = this.floor + FLOOR_OFFSET_DB;
    const hi = Math.max(this.top + TOP_MARGIN_DB, lo + MIN_SPAN_DB);
    return [lo, hi];
  }
}

/**
 * Fill `lut` (256 entries indexed by the frame's uint8 value) with colormap entries so that
 * dB `lo` maps to colormap 0 and `hi` to 255. Contrast bends the curve (t^gamma).
 */
export function buildRemap(
  lut: Uint32Array,
  cmap: Uint32Array,
  minDb: number,
  maxDb: number,
  lo: number,
  hi: number,
  contrast: number,
): void {
  const k = (maxDb - minDb) / 255;
  const span = Math.max(1, hi - lo);
  const gamma = 2 ** contrast;
  for (let v = 0; v < 256; v++) {
    let t = (minDb + v * k - lo) / span;
    t = t < 0 ? 0 : t > 1 ? 1 : t;
    if (gamma !== 1) t = t ** gamma;
    lut[v] = cmap[Math.round(t * 255)]!;
  }
}
