import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface Vessel {
  mmsi: number;
  name: string;
  callsign: string;
  shiptype: number | null;
  lat: number | null;
  lon: number | null;
  sog: number | null;
  cog: number | null;
  heading: number | null;
  ts: number;
}

interface Model {
  m: Map<number, Vessel>;
}
const MAX_VESSELS = 500;
const n = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : null);

function apply(model: Model, e: DecEvent): boolean {
  if (e.kind !== "message") return false;
  const d = e.data;
  if (typeof d.mmsi !== "number") return false;
  let v = model.m.get(d.mmsi);
  if (!v) {
    v = { mmsi: d.mmsi, name: "", callsign: "", shiptype: null, lat: null, lon: null, sog: null, cog: null, heading: null, ts: e.ts };
    model.m.set(d.mmsi, v);
  }
  v.ts = e.ts;
  if (typeof d.name === "string" && d.name.trim()) v.name = d.name.trim();
  if (typeof d.callsign === "string" && d.callsign.trim()) v.callsign = d.callsign.trim();
  v.shiptype = n(d.shiptype) ?? v.shiptype;
  const lat = n(d.lat);
  const lon = n(d.lon);
  if (lat !== null && lon !== null) {
    v.lat = lat;
    v.lon = lon;
  }
  v.sog = n(d.sog) ?? v.sog;
  v.cog = n(d.cog) ?? v.cog;
  v.heading = n(d.heading) ?? v.heading;
  if (model.m.size > MAX_VESSELS) {
    let oldest: Vessel | undefined;
    for (const x of model.m.values()) if (!oldest || x.ts < oldest.ts) oldest = x;
    if (oldest) model.m.delete(oldest.mmsi);
  }
  return true;
}

export function useAis(events: DecEvent[]): Vessel[] {
  return useIncremental<Model, Vessel[]>(events, "ais", () => ({ m: new Map() }), apply, (m) =>
    [...m.m.values()].map((v) => ({ ...v })),
  );
}
