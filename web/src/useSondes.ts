import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface SondePt {
  alt: number;
  lat: number | null;
  lon: number | null;
  temp: number | null;
  hum: number | null;
}

export interface Sonde {
  id: string;
  type: string;
  freq_hz: number | null;
  frame: number | null;
  datetime: string;
  ts: number;
  lat: number | null;
  lon: number | null;
  alt: number | null;
  vel_h: number | null;
  heading: number | null;
  vel_v: number | null;
  temp: number | null;
  humidity: number | null;
  pressure: number | null;
  batt: number | null;
  sats: number | null;
  sawUp: boolean;
  frames: number;
  hist: SondePt[]; // <= 2000, decimated (every 2nd point dropped) when full so the whole flight is kept
}

interface Rec extends Sonde {
  stride: number;
  skip: number;
}
interface Model {
  m: Map<string, Rec>;
}
const MAX_SONDES = 20;
const MAX_HIST = 2000;
const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

function apply(model: Model, e: DecEvent): boolean {
  if (e.kind !== "message") return false;
  const d = e.data;
  if (d.id === undefined || d.id === null || String(d.id) === "") return false;
  const id = String(d.id);
  let r = model.m.get(id);
  if (!r) {
    r = {
      id, type: "", freq_hz: null, frame: null, datetime: "", ts: e.ts, lat: null, lon: null, alt: null, vel_h: null,
      heading: null, vel_v: null, temp: null, humidity: null, pressure: null, batt: null, sats: null, sawUp: false,
      frames: 0, hist: [], stride: 1, skip: 0,
    };
    model.m.set(id, r);
  }
  r.ts = e.ts;
  r.frames++;
  if (typeof d.type === "string") r.type = d.type;
  r.freq_hz = num(d.freq_hz) ?? r.freq_hz;
  r.frame = num(d.frame) ?? r.frame;
  if (typeof d.datetime === "string") r.datetime = d.datetime;
  const lat = num(d.lat);
  const lon = num(d.lon);
  if (lat !== null && lon !== null) {
    r.lat = lat;
    r.lon = lon;
  }
  r.alt = num(d.alt) ?? r.alt;
  r.vel_h = num(d.vel_h) ?? r.vel_h;
  r.heading = num(d.heading) ?? r.heading;
  r.vel_v = num(d.vel_v) ?? r.vel_v;
  r.temp = num(d.temp) ?? r.temp;
  r.humidity = num(d.humidity) ?? r.humidity;
  r.pressure = num(d.pressure) ?? r.pressure;
  r.batt = num(d.batt) ?? r.batt;
  r.sats = num(d.sats) ?? r.sats;
  if (r.vel_v !== null && r.vel_v > 0) r.sawUp = true;
  const alt = num(d.alt);
  if (alt !== null && ++r.skip >= r.stride) {
    r.skip = 0;
    r.hist.push({ alt, lat: lat !== null && lon !== null ? lat : null, lon: lat !== null && lon !== null ? lon : null, temp: num(d.temp), hum: num(d.humidity) });
    if (r.hist.length > MAX_HIST) {
      r.hist = r.hist.filter((_, i) => i % 2 === 0);
      r.stride *= 2;
    }
  }
  if (model.m.size > MAX_SONDES) {
    let oldest: Rec | undefined;
    for (const x of model.m.values()) if (!oldest || x.ts < oldest.ts) oldest = x;
    if (oldest) model.m.delete(oldest.id);
  }
  return true;
}

export function useSondes(events: DecEvent[]): Sonde[] {
  return useIncremental<Model, Sonde[]>(events, "radiosonde", () => ({ m: new Map() }), apply, (m) =>
    [...m.m.values()].map(({ stride: _s, skip: _k, ...x }) => ({ ...x, hist: x.hist.slice() })).sort((a, b) => b.ts - a.ts),
  );
}
