import type { DecEvent } from "./useEvents";
import { useIncremental } from "./useIncremental";

export interface SstvImage {
  id: number;
  ts: number;
  url: string;
  mode: string;
  width: number;
  height: number;
  satellite: string;
}
export interface SstvProgress {
  ts: number;
  mode: string;
  lines: number;
  total: number;
  url: string;
}
export interface SstvView {
  images: SstvImage[]; // newest first
  progress: SstvProgress | null;
}

interface Model {
  imgs: SstvImage[]; // oldest first
  prog: SstvProgress | null;
}
const MAX_IMAGES = 100;
const s = (v: unknown) => (typeof v === "string" ? v : "");
const n = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);

export function satName(d: Record<string, unknown>): string {
  return d.satellite && typeof d.satellite === "object" ? s((d.satellite as Record<string, unknown>).name) : "";
}

function apply(m: Model, e: DecEvent): boolean {
  const d = e.data;
  if (e.kind === "progress") {
    m.prog = { ts: e.ts, mode: s(d.mode), lines: n(d.lines), total: n(d.total_lines), url: s(d.url) };
    return true;
  }
  if (e.kind === "image") {
    const url = s(d.url);
    if (!url) return false;
    m.imgs.push({ id: e.id, ts: e.ts, url, mode: s(d.mode), width: n(d.width), height: n(d.height), satellite: satName(d) });
    if (m.imgs.length > MAX_IMAGES) m.imgs.shift();
    m.prog = null;
    return true;
  }
  return false;
}

export function useSstv(events: DecEvent[]): SstvView {
  return useIncremental<Model, SstvView>(
    events,
    "sstv",
    () => ({ imgs: [], prog: null }),
    apply,
    (m) => ({ images: m.imgs.slice().reverse(), progress: m.prog }),
  );
}
