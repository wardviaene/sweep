// Band catalog types (GET /api/bands) and region helpers.
import type { DecoderName } from "./api";

export type BandMode = "nfm" | "am" | "usb" | "lsb" | "none";

export interface Channel {
  mhz: number;
  name: string;
  mode: BandMode;
  regions?: string[];
  decoder?: DecoderName | null;
}

export interface Band {
  id: string;
  name: string;
  regions: string[];
  lo_mhz: number;
  hi_mhz: number;
  edges?: Record<string, [number, number]>;
  tune_mhz: number;
  hf: boolean;
  mode: BandMode;
  decoder: DecoderName | null;
  on_air: string;
  decode_with: string[];
  antenna: string;
  tx_license: string;
  starter: string;
  channels: Channel[];
}

export interface Antenna {
  id: string;
  name: string;
  good: [number, number][];
  fair: [number, number][];
}

export interface Catalog {
  version?: number;
  default_region: string;
  regions: Record<string, string>;
  bands: Band[];
  antennas?: Antenna[];
}

export function bandRange(b: Band, region: string): [number, number] {
  return b.edges?.[region] ?? [b.lo_mhz, b.hi_mhz];
}

export function bandChannels(b: Band, region: string): Channel[] {
  return b.channels.filter((c) => !c.regions || c.regions.includes(region));
}

/** Precomputed (Hz) view of one band for the current region, used by the spectrum overlay. */
export interface BandView {
  id: string;
  name: string;
  lo_hz: number;
  hi_hz: number;
  channels: { hz: number; name: string }[];
}

export function bandViews(cat: Catalog | null, region: string): BandView[] {
  if (!cat) return [];
  return cat.bands
    .filter((b) => b.regions.includes(region))
    .map((b) => {
      const [lo, hi] = bandRange(b, region);
      return {
        id: b.id,
        name: b.name,
        lo_hz: lo * 1e6,
        hi_hz: hi * 1e6,
        channels: bandChannels(b, region).map((c) => ({ hz: c.mhz * 1e6, name: c.name })),
      };
    });
}

const REGION_KEY = "sweep.region";
export function loadRegion(): string | null {
  try {
    return localStorage.getItem(REGION_KEY);
  } catch {
    return null;
  }
}
export function saveRegion(r: string): void {
  try {
    localStorage.setItem(REGION_KEY, r);
  } catch {
    /* storage unavailable */
  }
}

export type Fit = "good" | "fair" | "poor";

/** Fit of a frequency (MHz) for an antenna: good range, fair range, else poor. */
export function antennaFit(a: Antenna, mhz: number): Fit {
  const inAny = (rs: [number, number][]) => rs.some(([lo, hi]) => mhz >= lo && mhz <= hi);
  return inAny(a.good) ? "good" : inAny(a.fair) ? "fair" : "poor";
}

export const FIT_TEXT: Record<Fit, string> = { good: "fits antenna", fair: "off-tune", poor: "needs other antenna" };

export interface PlanStep {
  id: string;
  kind: "scan" | "decoder";
  decoder?: DecoderName;
  band: Band;
  label: string;
  /** estimated seconds */
  seconds: number;
  /** set for steps that will not run */
  skipped?: string;
}

/** Client-side copy of the sdrd survey plan rules (docs/protocol.md "Survey"). */
export function planSurvey(cat: Catalog, region: string, ant: Antenna, includeFair: boolean, seconds: number): PlanStep[] {
  const eligible = cat.bands.filter((b) => {
    if (!b.regions.includes(region)) return false;
    if (b.hf && ant.id !== "hfwire") return false;
    const f = antennaFit(ant, b.tune_mhz);
    return f === "good" || (includeFair && f === "fair");
  });
  // Same id rule as sdrd: `<decoder>@<band>` when the decoder appears in more
  // than one band of the region's catalog (not just of this plan), so ids stay
  // stable whatever the antenna or ticked steps.
  const counts = new Map<string, number>();
  for (const b of cat.bands) {
    if (b.decoder && b.regions.includes(region) && !(b.hf && ant.id !== "hfwire")) {
      counts.set(b.decoder, (counts.get(b.decoder) ?? 0) + 1);
    }
  }
  const out: PlanStep[] = [];
  for (const b of eligible) {
    const [lo, hi] = bandRange(b, region);
    out.push({
      id: `scan:${b.id}`,
      kind: "scan",
      band: b,
      label: `${b.name}: band scan`,
      seconds: Math.max(2, Math.ceil((hi - lo) / 1.6) * 0.5 + 1), // ~0.5 s dwell per 1.6 MHz step, rough
    });
    if (b.decoder) {
      const d = b.decoder;
      out.push({
        id: (counts.get(d) ?? 0) > 1 ? `${d}@${b.id}` : d,
        kind: "decoder",
        decoder: d,
        band: b,
        label: `${b.name}: ${d}`,
        seconds: seconds,
        skipped: d === "packet" ? "needs a satellite pass" : undefined,
      });
    }
  }
  return out;
}
