import { useCallback, useEffect, useRef, useState } from "react";
import { setDemod, type DemodParams, type Status } from "./api";

const MIN_INTERVAL_MS = 100; // ~10 req/s
const ERROR_MS = 4000;

export interface DemodApi {
  /** Merge params into the pending request; sent at most ~10/s unless `immediate`. */
  send: (p: DemodParams, immediate?: boolean) => void;
  error: string | null;
}

export function useDemod(applyStatus: (s: Status) => void): DemodApi {
  const [error, setError] = useState<string | null>(null);
  const pending = useRef<DemodParams | null>(null);
  const lastSend = useRef(0);
  const timer = useRef<number | undefined>(undefined);
  const errTimer = useRef<number | undefined>(undefined);
  const seq = useRef(0);
  const applied = useRef(0);
  const applyRef = useRef(applyStatus);
  applyRef.current = applyStatus;

  const flush = useCallback(() => {
    window.clearTimeout(timer.current);
    timer.current = undefined;
    const p = pending.current;
    pending.current = null;
    if (!p) return;
    lastSend.current = performance.now();
    const id = ++seq.current;
    setDemod(p)
      .then((s) => {
        if (id < applied.current) return;
        applied.current = id;
        applyRef.current(s);
      })
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : String(e));
        window.clearTimeout(errTimer.current);
        errTimer.current = window.setTimeout(() => setError(null), ERROR_MS);
      });
  }, []);

  const send = useCallback(
    (p: DemodParams, immediate?: boolean) => {
      pending.current = { ...pending.current, ...p };
      if (immediate) return flush();
      if (timer.current !== undefined) return;
      const wait = Math.max(0, MIN_INTERVAL_MS - (performance.now() - lastSend.current));
      timer.current = window.setTimeout(flush, wait);
    },
    [flush],
  );

  useEffect(
    () => () => {
      window.clearTimeout(timer.current);
      window.clearTimeout(errTimer.current);
    },
    [],
  );

  return { send, error };
}
