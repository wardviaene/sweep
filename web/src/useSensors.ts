import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface Sensor {
  key: string;
  model: string;
  id: string;
  channel: string;
  data: Record<string, unknown>; // latest message
  ts: number;
  count: number;
  temps: number[];
  hums: number[];
}

interface Model {
  m: Map<string, Sensor>;
}
const MAX_SENSORS = 100;
const HIST = 100;

function apply(model: Model, e: DecEvent): boolean {
  if (e.kind !== "message") return false;
  const d = e.data;
  const model_ = d.model === undefined ? "" : String(d.model);
  const id = d.id === undefined ? "" : String(d.id);
  const channel = d.channel === undefined ? "" : String(d.channel);
  const key = [model_, id, channel].filter((x) => x !== "").join("/") || "unknown";
  let s = model.m.get(key);
  if (!s) {
    s = { key, model: model_ || "unknown", id, channel, data: d, ts: e.ts, count: 0, temps: [], hums: [] };
    model.m.set(key, s);
  }
  s.data = { ...s.data, ...d };
  s.ts = e.ts;
  s.count++;
  const t = typeof d.temperature_C === "number" ? d.temperature_C : null;
  const h = typeof d.humidity === "number" ? d.humidity : null;
  if (t !== null) {
    s.temps.push(t);
    if (s.temps.length > HIST) s.temps.shift();
  }
  if (h !== null) {
    s.hums.push(h);
    if (s.hums.length > HIST) s.hums.shift();
  }
  if (model.m.size > MAX_SENSORS) {
    let oldest: Sensor | undefined;
    for (const x of model.m.values()) if (!oldest || x.ts < oldest.ts) oldest = x;
    if (oldest) model.m.delete(oldest.key);
  }
  return true;
}

export function useSensors(events: DecEvent[]): Sensor[] {
  return useIncremental<Model, Sensor[]>(events, "rtl_433", () => ({ m: new Map() }), apply, (m) =>
    [...m.m.values()].map((s) => ({ ...s, temps: s.temps.slice(), hums: s.hums.slice() })).sort((a, b) => b.ts - a.ts),
  );
}
