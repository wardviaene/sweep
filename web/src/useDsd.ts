import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface DsdCall {
  id: number;
  key: string;
  mode: string;
  tgt: number | null;
  src: number | null;
  cc: number | null;
  encrypted: boolean;
  first: number;
  last: number;
}

interface Model {
  calls: DsdCall[]; // oldest first
  latest: Map<string, DsdCall>;
}
const MAX_CALLS = 300;
const GAP_S = 3;
const n = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : null);

function apply(m: Model, e: DecEvent): boolean {
  if (e.kind !== "message") return false;
  const d = e.data;
  const mode = typeof d.mode === "string" ? d.mode : "";
  const tgt = n(d.tgt);
  const src = n(d.src);
  const key = `${mode}|${tgt ?? ""}|${src ?? ""}`;
  const prev = m.latest.get(key);
  if (prev && e.ts - prev.last <= GAP_S) {
    prev.last = e.ts;
    if (d.encrypted === true) prev.encrypted = true;
    const cc = n(d.color_code);
    if (cc !== null) prev.cc = cc;
    return true;
  }
  const c: DsdCall = {
    id: e.id, key, mode, tgt, src, cc: n(d.color_code), encrypted: d.encrypted === true, first: e.ts, last: e.ts,
  };
  m.calls.push(c);
  m.latest.set(key, c);
  if (m.calls.length > MAX_CALLS) {
    const old = m.calls.shift()!;
    if (m.latest.get(old.key) === old) m.latest.delete(old.key);
  }
  return true;
}

/** Call log, newest first (max 300). */
export function useDsd(events: DecEvent[]): DsdCall[] {
  return useIncremental<Model, DsdCall[]>(events, "dsd", () => ({ calls: [], latest: new Map() }), apply, (m) =>
    m.calls.map((c) => ({ ...c })).reverse(),
  );
}
