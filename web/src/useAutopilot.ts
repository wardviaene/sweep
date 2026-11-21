import { useCallback, useEffect, useRef, useState } from "react";
import { getAutopilot, putAutopilot, type Autopilot, type AutopilotPatch } from "./api";

export interface AutopilotApi {
  state: Autopilot | null;
  error: string | null;
  /** merge one or more fields (only the changed ones are sent) */
  patch: (p: AutopilotPatch) => void;
  refresh: () => void;
}

/** GET /api/autopilot every 1 s while a pass is current, else every 10 s; only while `active`. */
export function useAutopilot(active: boolean): AutopilotApi {
  const [state, setState] = useState<Autopilot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const kick = useRef<(() => void) | null>(null);

  useEffect(() => {
    if (!active) return;
    const ac = new AbortController();
    let timer: number | undefined;
    const poll = async () => {
      window.clearTimeout(timer);
      let ms = 10_000;
      try {
        const s = await getAutopilot(ac.signal);
        setState(s);
        setError(null);
        if (s.current) ms = 1000;
      } catch (e) {
        if (ac.signal.aborted) return;
        setError(e instanceof Error ? e.message : String(e));
      }
      timer = window.setTimeout(poll, ms);
    };
    kick.current = () => void poll();
    void poll();
    return () => {
      ac.abort();
      window.clearTimeout(timer);
      kick.current = null;
    };
  }, [active]);

  const patch = useCallback((p: AutopilotPatch) => {
    // optimistic, then the server's merged answer
    setState((s) => (s ? { ...s, ...p } : s));
    putAutopilot(p)
      .then((s) => {
        setState(s);
        setError(null);
      })
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : String(e));
        kick.current?.();
      });
  }, []);

  const refresh = useCallback(() => kick.current?.(), []);
  return { state, error, patch, refresh };
}
