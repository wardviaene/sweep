import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface FisbReport {
  key: string;
  type: string;
  location: string;
  time: string;
  product: string;
  text: string;
  ts: number;
}

interface Model {
  m: Map<string, FisbReport>;
}
const MAX_REPORTS = 500;
const s = (v: unknown) => (typeof v === "string" ? v : "");

function apply(model: Model, e: DecEvent): boolean {
  if (e.kind !== "message") return false;
  const d = e.data;
  const text = s(d.text);
  if (!text) return false;
  const type = s(d.report_type).toUpperCase() || "OTHER";
  const location = s(d.location);
  const key = `${type}|${location || text.slice(0, 40)}`;
  model.m.delete(key); // re-insert so Map order stays oldest-first
  model.m.set(key, { key, type, location, time: s(d.time), product: s(d.product), text, ts: e.ts });
  if (model.m.size > MAX_REPORTS) {
    const oldest = model.m.keys().next().value;
    if (oldest !== undefined) model.m.delete(oldest);
  }
  return true;
}

/** Latest FIS-B text report per (report_type, location), newest first, max 500. */
export function useFisb(events: DecEvent[]): FisbReport[] {
  return useIncremental<Model, FisbReport[]>(events, "uat", () => ({ m: new Map() }), apply, (m) =>
    [...m.m.values()].reverse(),
  );
}
