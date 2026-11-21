import { useEffect, useState } from "react";
import { getOverhead, getPasses, type SatOverhead, type SatPass } from "./api";

export interface OverheadState {
  sats: SatOverhead[];
  error: string | null;
}

/** Polls /api/satellites/overhead every 5 s, only while `active`. */
export function useOverhead(active: boolean): OverheadState {
  const [st, setSt] = useState<OverheadState>({ sats: [], error: null });
  useEffect(() => {
    if (!active) return;
    const ac = new AbortController();
    let timer: number | undefined;
    const poll = async () => {
      try {
        const sats = await getOverhead(ac.signal);
        setSt({ sats, error: null });
      } catch (e) {
        if (ac.signal.aborted) return;
        setSt((p) => ({ sats: p.sats, error: e instanceof Error ? e.message : String(e) }));
      }
      timer = window.setTimeout(poll, 5000);
    };
    void poll();
    return () => {
      ac.abort();
      window.clearTimeout(timer);
    };
  }, [active]);
  return st;
}

export interface PassState {
  passes: SatPass[];
  /** 400 from the backend: receiver location not set */
  noReceiver: boolean;
  error: string | null;
  loaded: boolean;
}

/** Fetches the next 24 h of passes now, then every 5 min while `active`; refetches when the receiver changes. */
export function usePasses(active: boolean, receiverKey: string): PassState {
  const [st, setSt] = useState<PassState>({ passes: [], noReceiver: false, error: null, loaded: false });
  useEffect(() => {
    if (!active) return;
    const ac = new AbortController();
    let timer: number | undefined;
    const load = async () => {
      try {
        const r = await getPasses(24, 10, ac.signal);
        setSt({ passes: r.passes.slice(0, 300), noReceiver: r.noReceiver, error: null, loaded: true });
      } catch (e) {
        if (ac.signal.aborted) return;
        setSt((p) => ({ ...p, error: e instanceof Error ? e.message : String(e), loaded: true }));
      }
      timer = window.setTimeout(load, 5 * 60_000);
    };
    void load();
    return () => {
      ac.abort();
      window.clearTimeout(timer);
    };
  }, [active, receiverKey]);
  return st;
}
