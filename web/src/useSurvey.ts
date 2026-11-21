import { useCallback, useEffect, useRef, useState } from "react";
import { getSurvey, startSurvey, stopSurvey, type Survey, type SurveyParams } from "./api";

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export interface SurveyApi {
  survey: Survey | null;
  error: string | null;
  busy: boolean;
  start: (p: SurveyParams) => Promise<boolean>;
  stop: () => void;
}

/** Fetches the survey state when `active` (panel open), then polls every 1 s while it is running and every 2 s otherwise (cheap; lets the radio pill notice a survey). */
export function useSurvey(active: boolean): SurveyApi {
  const [survey, setSurvey] = useState<Survey | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const running = survey?.state === "running";

  // Initial fetch each time the panel opens (also once on mount, for the summary line).
  useEffect(() => {
    const ac = new AbortController();
    getSurvey(ac.signal)
      .then((s) => {
        setSurvey(s);
        setError(null);
      })
      .catch((e: unknown) => {
        if (!ac.signal.aborted) setError(msg(e));
      });
    return () => ac.abort();
  }, [active]);

  const inflight = useRef(false);
  useEffect(() => {
    const ac = new AbortController();
    const id = window.setInterval(() => {
      if (inflight.current) return;
      inflight.current = true;
      getSurvey(ac.signal)
        .then((s) => {
          // idle and unchanged: keep the same object so subscribers do not re-render
          setSurvey((prev) => (prev && s.state !== "running" && prev.state === s.state && prev.id === s.id ? prev : s));
          setError(null);
        })
        .catch((e: unknown) => {
          if (!ac.signal.aborted) setError(msg(e));
        })
        .finally(() => {
          inflight.current = false;
        });
    }, running ? 1000 : 2000);
    return () => {
      ac.abort();
      window.clearInterval(id);
      inflight.current = false;
    };
  }, [running]);

  const start = useCallback(async (p: SurveyParams) => {
    setBusy(true);
    setError(null);
    try {
      setSurvey(await startSurvey(p));
      return true;
    } catch (e) {
      setError(msg(e));
      return false;
    } finally {
      setBusy(false);
    }
  }, []);

  const stop = useCallback(() => {
    stopSurvey()
      .then(setSurvey)
      .catch((e: unknown) => setError(msg(e)));
  }, []);

  return { survey, error, busy, start, stop };
}
