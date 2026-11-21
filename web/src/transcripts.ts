import { useEffect, useRef, useState } from "react";
import { getTranscripts, toTranscript, type ScannerTx, type Transcript } from "./api";
import type { DecEvent } from "./useEvents";

export const MAX_TRANSCRIPTS = 500;
const INITIAL = 100;

function merge(prev: Transcript[], add: Transcript[]): Transcript[] {
  if (!add.length) return prev;
  const m = new Map<string, Transcript>();
  for (const t of prev) m.set(t.id, t);
  for (const t of add) m.set(t.id, t);
  const all = [...m.values()].sort((a, b) => b.start - a.start);
  return all.length > MAX_TRANSCRIPTS ? all.slice(0, MAX_TRANSCRIPTS) : all;
}

/** Newest-first transcripts: GET /api/transcripts once, then the "transcribe" events. Bounded to 500. */
export function useTranscripts(events: DecEvent[]): Transcript[] {
  const [items, setItems] = useState<Transcript[]>([]);
  const lastId = useRef(0);

  useEffect(() => {
    const ac = new AbortController();
    getTranscripts({ limit: INITIAL }, ac.signal)
      .then((list) => setItems((p) => merge(p, list)))
      .catch(() => {
        /* backend without transcription: events (if any) still work */
      });
    return () => ac.abort();
  }, []);

  useEffect(() => {
    const last = events[events.length - 1];
    if (!last) return;
    if (last.id < lastId.current) lastId.current = 0; // backend restart
    const add: Transcript[] = [];
    for (const e of events) {
      if (e.id <= lastId.current) continue;
      if (e.decoder !== "transcribe" || e.kind !== "message") continue;
      const t = toTranscript(e.data);
      if (t) add.push(t);
    }
    lastId.current = last.id;
    if (add.length) setItems((p) => merge(p, add));
  }, [events]);

  return items;
}

/** Which transcript belongs to a scanner transmission: by id, else same channel (1 kHz) and start within 1.5 s. */
export function matchTx(tx: ScannerTx, byId: Map<string, Transcript>, items: Transcript[]): Transcript | undefined {
  if (tx.id) {
    const t = byId.get(tx.id);
    if (t) return t;
  }
  let best: Transcript | undefined;
  let bd = 1.5;
  for (const t of items) {
    if (t.source !== "scanner" || Math.abs(t.freq_hz - tx.freq_hz) > 1000) continue;
    const d = Math.abs(t.start - tx.start);
    if (d <= bd) {
      bd = d;
      best = t;
    }
  }
  return best;
}

/** The transcript of a survey listen hold (same channel, made after the survey started). */
export function surveyTranscript(items: Transcript[], freq_hz: number, since: number): Transcript | undefined {
  return items.find((t) => t.source === "survey" && Math.abs(t.freq_hz - freq_hz) <= 1000 && t.start >= since - 5);
}
