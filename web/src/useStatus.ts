import { useEffect, useState } from "react";
import { getStatus, type Status } from "./api";

/** Polls GET /api/status every `intervalMs`. */
export function useStatus(intervalMs = 2000): { status: Status | null; error: boolean; applyStatus: (s: Status) => void } {
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    let timer: number | undefined;
    const poll = async () => {
      try {
        setStatus(await getStatus(ac.signal));
        setError(false);
      } catch {
        if (ac.signal.aborted) return;
        setError(true);
      }
      timer = window.setTimeout(poll, intervalMs);
    };
    void poll();
    return () => {
      ac.abort();
      window.clearTimeout(timer);
    };
  }, [intervalMs]);

  return { status, error, applyStatus: setStatus };
}
