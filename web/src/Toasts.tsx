import { useEffect, useRef, useState } from "react";
import type { DecEvent } from "./useEvents";

interface Toast {
  id: number;
  text: string;
}
const TTL_MS = 6000;
const MAX_TOASTS = 4;
const FRESH_S = 15; // the server replays recent events on connect; only toast live ones

/** Non-blocking top-right toasts for autopilot AOS/LOS log lines. */
export function Toasts({ events }: { events: DecEvent[] }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const lastId = useRef(0);
  const timers = useRef<number[]>([]);

  useEffect(() => {
    const last = events[events.length - 1];
    if (!last) return;
    if (last.id < lastId.current) lastId.current = 0; // backend restart
    const fresh: Toast[] = [];
    const nowS = Date.now() / 1000;
    for (const e of events) {
      if (e.id <= lastId.current) continue;
      if (e.decoder !== "autopilot" || e.kind !== "log") continue;
      const line = typeof e.data.line === "string" ? e.data.line : "";
      if (/^(AOS|LOS)\b/.test(line) && nowS - e.ts < FRESH_S) fresh.push({ id: e.id, text: line });
    }
    lastId.current = last.id;
    if (!fresh.length) return;
    setToasts((t) => [...t, ...fresh].slice(-MAX_TOASTS));
    for (const f of fresh) {
      timers.current.push(window.setTimeout(() => setToasts((t) => t.filter((x) => x.id !== f.id)), TTL_MS));
    }
  }, [events]);

  useEffect(() => () => timers.current.forEach((t) => window.clearTimeout(t)), []);

  if (!toasts.length) return null;
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className="toast">{t.text}</div>
      ))}
    </div>
  );
}
