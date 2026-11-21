import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface Meter {
  key: string;
  model: string;
  id: string;
  first: number | null; // first consumption reading
  last: number | null;
  ts: number;
  count: number;
  hist: number[]; // consumption over time (bounded)
  data: Record<string, unknown>;
}

export interface MetersView {
  meters: Meter[];
  otherHeard: number | null;
  ownIds: number | null;
}

interface Model {
  m: Map<string, Meter>;
  other: number | null;
  own: number | null;
}
const MAX_METERS = 50;
const HIST = 200;
const num = (v: unknown): number | null => {
  if (typeof v === "number" && Number.isFinite(v)) return v;
  if (typeof v === "string" && v.trim() !== "" && Number.isFinite(Number(v))) return Number(v);
  return null;
};

function apply(model: Model, e: DecEvent): boolean {
  const d = e.data;
  if (e.kind === "snapshot") {
    model.other = num(d.other_meters_heard);
    model.own = num(d.own_ids);
    return true;
  }
  if (e.kind !== "message") return false;
  const id = d.id === undefined ? "" : String(d.id);
  const mdl = typeof d.model === "string" ? d.model : "meter";
  const key = `${mdl}/${id}`;
  let r = model.m.get(key);
  if (!r) {
    r = { key, model: mdl, id, first: null, last: null, ts: e.ts, count: 0, hist: [], data: d };
    model.m.set(key, r);
  }
  r.data = d;
  r.ts = e.ts;
  r.count++;
  const c = num(d.consumption_data) ?? num(d.consumption);
  if (c !== null) {
    if (r.first === null) r.first = c;
    r.last = c;
    r.hist.push(c);
    if (r.hist.length > HIST) r.hist.shift();
  }
  if (model.m.size > MAX_METERS) {
    let oldest: Meter | undefined;
    for (const x of model.m.values()) if (!oldest || x.ts < oldest.ts) oldest = x;
    if (oldest) model.m.delete(oldest.key);
  }
  return true;
}

export function useMeters(events: DecEvent[]): MetersView {
  return useIncremental<Model, MetersView>(events, "meters", () => ({ m: new Map(), other: null, own: null }), apply, (m) => ({
    meters: [...m.m.values()].map((x) => ({ ...x, hist: x.hist.slice() })).sort((a, b) => b.ts - a.ts),
    otherHeard: m.other,
    ownIds: m.own,
  }));
}
