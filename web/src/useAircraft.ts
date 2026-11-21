import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface Aircraft {
  hex: string;
  flight: string;
  alt: number | "ground" | null;
  gs: number | null;
  track: number | null;
  lat: number | null;
  lon: number | null;
  seen: number | null;
  messages: number | null;
  rssi: number | null;
  /** +1 climbing, -1 descending, 0 level/unknown */
  trend: -1 | 0 | 1;
  /** last [lat, lon] positions (client side) */
  trail: [number, number][];
}

export interface AircraftView {
  aircraft: Aircraft[];
  withPos: number;
}

interface Rec extends Aircraft {
  snapTs: number; // ts of the last snapshot that contained it
  lastAlt: number | null;
  still: number;
}
interface Model {
  m: Map<string, Rec>;
}

const TRAIL = 60;
const EXPIRE_S = 60;
const numOrNull = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : null);

function apply(model: Model, e: DecEvent): boolean {
  if (e.kind !== "snapshot" || !Array.isArray(e.data.aircraft)) return false;
  const seenNow = new Set<string>();
  for (const raw of e.data.aircraft as Record<string, unknown>[]) {
    if (typeof raw.hex !== "string") continue;
    const hex = raw.hex.toLowerCase();
    seenNow.add(hex);
    let r = model.m.get(hex);
    if (!r) {
      r = {
        hex, flight: "", alt: null, gs: null, track: null, lat: null, lon: null, seen: null, messages: null, rssi: null,
        trend: 0, trail: [], snapTs: e.ts, lastAlt: null, still: 0,
      };
      model.m.set(hex, r);
    }
    r.snapTs = e.ts;
    if (typeof raw.flight === "string" && raw.flight.trim()) r.flight = raw.flight.trim();
    const alt = raw.alt_baro === "ground" ? "ground" : numOrNull(raw.alt_baro);
    r.alt = alt ?? r.alt;
    if (typeof alt === "number") {
      if (r.lastAlt !== null && Math.abs(alt - r.lastAlt) >= 50) {
        r.trend = alt > r.lastAlt ? 1 : -1;
        r.lastAlt = alt;
        r.still = 0;
      } else {
        if (r.lastAlt === null) r.lastAlt = alt;
        if (++r.still > 8) r.trend = 0;
      }
    }
    const vr = numOrNull(raw.vert_rate); // UAT: ft/min
    if (vr !== null && Math.abs(vr) >= 128) r.trend = vr > 0 ? 1 : -1;
    r.gs = numOrNull(raw.gs);
    r.track = numOrNull(raw.track) ?? r.track;
    r.seen = numOrNull(raw.seen);
    r.messages = numOrNull(raw.messages);
    r.rssi = numOrNull(raw.rssi);
    const lat = numOrNull(raw.lat);
    const lon = numOrNull(raw.lon);
    if (lat !== null && lon !== null) {
      r.lat = lat;
      r.lon = lon;
      const t = r.trail[r.trail.length - 1];
      if (!t || t[0] !== lat || t[1] !== lon) {
        r.trail.push([lat, lon]);
        if (r.trail.length > TRAIL) r.trail.shift();
      }
    }
  }
  for (const [hex, r] of model.m) if (!seenNow.has(hex) && e.ts - r.snapTs > EXPIRE_S) model.m.delete(hex);
  return true;
}

export function useAircraft(events: DecEvent[], decoder: "adsb" | "uat" = "adsb"): AircraftView {
  return useIncremental<Model, AircraftView>(
    events,
    decoder,
    () => ({ m: new Map() }),
    apply,
    (model) => {
      // Fresh objects so consumers can compare by reference; trails are copied (bounded at 60).
      const aircraft = [...model.m.values()].map((r): Aircraft => ({
        hex: r.hex, flight: r.flight, alt: r.alt, gs: r.gs, track: r.track, lat: r.lat, lon: r.lon,
        seen: r.seen, messages: r.messages, rssi: r.rssi, trend: r.trend, trail: r.trail.slice(),
      }));
      return { aircraft, withPos: aircraft.filter((a) => a.lat !== null).length };
    },
  );
}
