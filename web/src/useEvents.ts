import { useEffect, useState } from "react";
import { connectWs } from "./wsutil";

export interface DecEvent {
  id: number;
  ts: number;
  decoder: string;
  kind: string;
  data: Record<string, unknown>;
}

const MAX_EVENTS = 500;
const FLUSH_MS = 250;

/** /ws/events, batched into React state every 250 ms; keeps the newest 500. */
export function useEvents(): DecEvent[] {
  const [events, setEvents] = useState<DecEvent[]>([]);

  useEffect(() => {
    let buf: DecEvent[] = [];
    let reset = false;
    const stop = connectWs("/ws/events", {
      // The server replays the last 100 events on (re)connect, so start clean.
      onOpen: () => {
        buf = [];
        reset = true;
      },
      onText: (t) => {
        try {
          const e = JSON.parse(t) as DecEvent;
          if (typeof e.id === "number" && e.data && typeof e.data === "object") buf.push(e);
        } catch {
          /* ignore malformed */
        }
      },
    });
    const timer = window.setInterval(() => {
      if (!buf.length && !reset) return;
      const batch = buf;
      buf = [];
      const clear = reset;
      reset = false;
      setEvents((prev) => {
        const all = clear ? batch : prev.concat(batch);
        return all.length > MAX_EVENTS ? all.slice(all.length - MAX_EVENTS) : all;
      });
    }, FLUSH_MS);
    return () => {
      stop();
      window.clearInterval(timer);
    };
  }, []);

  return events;
}
