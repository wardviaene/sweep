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

export interface Catalog {
  version?: number;
  default_region: string;
  regions: Record<string, string>;
  bands: Band[];
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
