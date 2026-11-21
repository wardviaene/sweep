import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface AprsStation {
  call: string;
  lat: number | null;
  lon: number | null;
  track: [number, number][];
  ts: number;
  packets: number;
  comment: string;
}
export interface AprsPacket {
  id: number;
  ts: number;
  from: string;
  to: string;
  path: string;
  info: string;
  lat: number | null;
  lon: number | null;
}
export interface AprsView {
  stations: AprsStation[];
  packets: AprsPacket[]; // newest first
}

interface Model {
  st: Map<string, AprsStation>;
  pk: AprsPacket[]; // oldest first
}
const MAX_STATIONS = 300;
const MAX_PACKETS = 300;
const MAX_TRACK = 100;
const s = (v: unknown) => (typeof v === "string" ? v : "");

function apply(m: Model, e: DecEvent): boolean {
  if (e.kind !== "message") return false;
  const d = e.data;
  const from = s(d.from);
  if (!from) return false;
  const lat = typeof d.lat === "number" ? d.lat : null;
  const lon = typeof d.lon === "number" ? d.lon : null;
  m.pk.push({
    id: e.id, ts: e.ts, from, to: s(d.to),
    path: Array.isArray(d.path) ? (d.path as unknown[]).join(",") : "",
    info: s(d.comment) || s(d.info), lat, lon,
  });
  if (m.pk.length > MAX_PACKETS) m.pk.shift();
  let st = m.st.get(from);
  if (!st) {
    st = { call: from, lat: null, lon: null, track: [], ts: e.ts, packets: 0, comment: "" };
    m.st.set(from, st);
  }
  st.ts = e.ts;
  st.packets++;
  if (s(d.comment)) st.comment = s(d.comment);
  if (lat !== null && lon !== null) {
    st.lat = lat;
    st.lon = lon;
    const t = st.track[st.track.length - 1];
    if (!t || t[0] !== lat || t[1] !== lon) {
      st.track.push([lat, lon]);
      if (st.track.length > MAX_TRACK) st.track.shift();
    }
  }
  if (m.st.size > MAX_STATIONS) {
    let oldest: AprsStation | undefined;
    for (const x of m.st.values()) if (!oldest || x.ts < oldest.ts) oldest = x;
    if (oldest) m.st.delete(oldest.call);
  }
  return true;
}

export function useAprs(events: DecEvent[]): AprsView {
  return useIncremental<Model, AprsView>(
    events,
    "aprs",
    () => ({ st: new Map(), pk: [] }),
    apply,
    (m) => ({
      stations: [...m.st.values()].map((x) => ({ ...x, track: x.track.slice() })),
      packets: m.pk.slice().reverse(),
    }),
  );
}
