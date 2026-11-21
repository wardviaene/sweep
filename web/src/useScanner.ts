import { useCallback, useEffect, useRef, useState } from "react";
import {
  getScanner,
  scannerLockout,
  startScanner,
  stopScanner,
  type ScannerParams,
  type ScannerState,
} from "./api";

export interface ScannerApi {
  state: ScannerState | null;
  /** watching or locked */
  running: boolean;
  /** hz of channels the scanner prefers (this client's reconfigures) */
  priority: number[];
  busy: boolean;
  error: string | null;
  start: (p: ScannerParams) => Promise<boolean>;
  stop: () => Promise<boolean>;
  lockout: (hz: number, remove?: boolean) => void;
  prioritize: (hz: number) => void;
}

const FAST_MS = 500;

/** Polls GET /api/scanner: every 500 ms while running (or while the panel is open and idle, 1 s), 2 s otherwise. */
export function useScanner(open: boolean): ScannerApi {
  const [state, setState] = useState<ScannerState | null>(null);
  const [priority, setPriority] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const stateRef = useRef<ScannerState | null>(null);
  const openRef = useRef(open);
  openRef.current = open;
  const cfgRef = useRef<ScannerParams | null>(null);
  const prioRef = useRef<number[]>([]);
  const pollNow = useRef<() => void>(() => {});

  const apply = useCallback((s: ScannerState) => {
    stateRef.current = s;
    setState(s);
  }, []);

  useEffect(() => {
    let stopped = false;
    let timer: number | undefined;
    let ctl: AbortController | null = null;
    const loop = () => {
      window.clearTimeout(timer);
      ctl?.abort();
      ctl = new AbortController();
      getScanner(ctl.signal)
        .then((s) => {
          if (!stopped) apply(s);
        })
        .catch(() => {
          /* backend without scanner, or transient: keep the last state */
        })
        .finally(() => {
          if (stopped) return;
          const st = stateRef.current?.state;
          const running = st === "watching" || st === "locked";
          timer = window.setTimeout(loop, running ? FAST_MS : openRef.current ? 1000 : 2000);
        });
    };
    pollNow.current = loop;
    loop();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      ctl?.abort();
    };
  }, [apply]);

  const fail = (e: unknown) => setError(e instanceof Error ? e.message : String(e));

  const start = useCallback(
    async (p: ScannerParams) => {
      setBusy(true);
      setError(null);
      try {
        cfgRef.current = p;
        prioRef.current = p.priority_hz ?? [];
        setPriority(prioRef.current);
        apply(await startScanner(p));
        return true;
      } catch (e) {
        fail(e);
        return false;
      } finally {
        setBusy(false);
      }
    },
    [apply],
  );

  const stop = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      apply(await stopScanner());
      return true;
    } catch (e) {
      fail(e);
      return false;
    } finally {
      setBusy(false);
    }
  }, [apply]);

  const lockout = useCallback(
    (hz: number, remove?: boolean) => {
      setError(null);
      scannerLockout(hz, remove)
        .then(() => pollNow.current())
        .catch(fail);
    },
    [],
  );

  const prioritize = useCallback(
    (hz: number) => {
      const s = stateRef.current;
      if (!s) return;
      const base: ScannerParams = cfgRef.current ?? {
        ...(s.band ? { band: s.band } : {}),
        lo_hz: s.lo_hz,
        hi_hz: s.hi_hz,
        mode: s.mode,
      };
      if (prioRef.current.includes(hz)) return;
      void start({ ...base, lockout_hz: s.lockout_hz, priority_hz: [...prioRef.current, hz] });
    },
    [start],
  );

  const running = state?.state === "watching" || state?.state === "locked";
  return { state, running, priority, busy, error, start, stop, lockout, prioritize };
}
