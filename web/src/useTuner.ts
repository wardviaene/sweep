import { useCallback, useEffect, useRef, useState } from "react";
import { tune, type Status, type TuneParams } from "./api";

export const MIN_HZ = 24e6;
export const MAX_HZ = 1766e6;
const MIN_INTERVAL_MS = 100; // max ~10 freq tunes/s
const ERROR_MS = 4000;

export const clampHz = (hz: number) => Math.min(MAX_HZ, Math.max(MIN_HZ, hz));

export interface Tuner {
  /** Tune to an absolute frequency (coalesced/rate limited). */
  setFreq: (hz: number) => void;
  /** Shift the (pending) frequency by deltaHz (coalesced/rate limited). */
  shiftFreq: (deltaHz: number) => void;
  /** Immediate tune of other parameters. */
  setParams: (p: TuneParams) => void;
  error: string | null;
}

export function useTuner(status: Status | null, applyStatus: (s: Status) => void): Tuner {
  const [error, setError] = useState<string | null>(null);
  const centerRef = useRef<number | null>(null);
  const pending = useRef<number | null>(null);
  const lastSend = useRef(0);
  const timer = useRef<number | undefined>(undefined);
  const errTimer = useRef<number | undefined>(undefined);
  const seq = useRef(0);
  const applied = useRef(0);
  const applyRef = useRef(applyStatus);
  applyRef.current = applyStatus;
  centerRef.current = status?.center_hz ?? null;

  const send = useCallback((p: TuneParams) => {
    const id = ++seq.current;
    tune(p)
      .then((s) => {
        if (id < applied.current) return; // stale response
        applied.current = id;
        applyRef.current(s);
      })
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : String(e));
        window.clearTimeout(errTimer.current);
        errTimer.current = window.setTimeout(() => setError(null), ERROR_MS);
      });
  }, []);

  const flush = useCallback(() => {
    timer.current = undefined;
    const hz = pending.current;
    pending.current = null;
    if (hz === null) return;
    lastSend.current = performance.now();
    send({ freq_hz: hz });
  }, [send]);

  const schedule = useCallback(() => {
    if (timer.current !== undefined) return;
    const wait = Math.max(0, MIN_INTERVAL_MS - (performance.now() - lastSend.current));
    timer.current = window.setTimeout(flush, wait);
  }, [flush]);

  const setFreq = useCallback(
    (hz: number) => {
      pending.current = clampHz(Math.round(hz));
      schedule();
    },
    [schedule],
  );

  const shiftFreq = useCallback(
    (d: number) => {
      const base = pending.current ?? centerRef.current;
      if (base === null) return;
      setFreq(base + d);
    },
    [setFreq],
  );

  useEffect(
    () => () => {
      window.clearTimeout(timer.current);
      window.clearTimeout(errTimer.current);
    },
    [],
  );

  return { setFreq, shiftFreq, setParams: send, error };
}
