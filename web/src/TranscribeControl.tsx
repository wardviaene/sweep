import { useEffect, useRef, useState } from "react";
import { postTranscribe, type Transcript } from "./api";
import { TranscriptLine } from "./Clips";

const SECONDS = [5, 10, 20, 30];
const GIVE_UP_S = 60; // after the countdown, wait this long for the result

interface Run {
  startedAt: number;
  seconds: number;
}

/** "Transcribe" button: records the next N seconds of the VFO audio and shows the text inline. */
export function TranscribeControl({ enabled, transcripts }: { enabled: boolean; transcripts: Transcript[] }) {
  const [secs, setSecs] = useState(10);
  const [run, setRun] = useState<Run | null>(null);
  const [now, setNow] = useState(0);
  const [result, setResult] = useState<Transcript | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const runRef = useRef<Run | null>(null);
  const clearTimer = useRef<number | undefined>(undefined);

  const finish = (t: Transcript | null, e: string | null) => {
    runRef.current = null;
    setRun(null);
    setResult(t);
    setErr(e);
    window.clearTimeout(clearTimer.current);
    clearTimer.current = window.setTimeout(() => {
      setResult(null);
      setErr(null);
    }, 20000);
  };

  const go = () => {
    const r = { startedAt: Date.now() / 1000, seconds: secs };
    runRef.current = r;
    setRun(r);
    setNow(r.startedAt);
    setResult(null);
    setErr(null);
    postTranscribe(r.seconds)
      .then((t) => {
        if (runRef.current === r && t) finish(t, null); // else the transcribe event finishes it
      })
      .catch((e: unknown) => {
        if (runRef.current === r) finish(null, e instanceof Error ? e.message : String(e));
      });
  };

  // Countdown / timeout.
  useEffect(() => {
    if (!run) return;
    const id = window.setInterval(() => {
      const t = Date.now() / 1000;
      setNow(t);
      if (runRef.current === run && t > run.startedAt + run.seconds + GIVE_UP_S) finish(null, "no transcript arrived");
    }, 250);
    return () => window.clearInterval(id);
  }, [run]);

  // The manual transcript arriving as an event.
  useEffect(() => {
    if (!run) return;
    const t = transcripts.find((x) => x.source === "manual" && x.start >= run.startedAt - 2);
    if (t) finish(t, null);
  }, [transcripts, run]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => () => window.clearTimeout(clearTimer.current), []);

  const left = run ? Math.max(0, Math.ceil(run.startedAt + run.seconds - now)) : 0;
  return (
    <span className="item tr-ctl">
      <button disabled={!enabled || !!run} onClick={go} title={enabled ? "Transcribe the next N seconds of the VFO audio (local, whisper.cpp)" : "Turn the demodulator on first"}>
        Transcribe
      </button>{" "}
      <select value={secs} disabled={!!run} onChange={(e) => setSecs(Number(e.target.value))} title="Seconds to record">
        {SECONDS.map((n) => (
          <option key={n} value={n}>{n} s</option>
        ))}
      </select>
      {run && <span className="conn-connecting"> {left > 0 ? `recording ${left} s…` : "transcribing…"}</span>}
      {err && <span className="err">{err}</span>}
      {result && (
        <span className="tr-inline">
          {" "}<TranscriptLine t={result} />
          {!result.empty && (
            <button className="mini" title="Copy text" onClick={() => void navigator.clipboard?.writeText(result.text).catch(() => undefined)}>copy</button>
          )}
          <button className="mini" title="Dismiss" onClick={() => setResult(null)}>×</button>
        </span>
      )}
    </span>
  );
}
