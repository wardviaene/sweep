import { useCallback, useEffect, useState } from "react";
import { getTranscribe, putTranscribe, type TranscribeStatus } from "./api";

/** Settings section: GET/PUT /api/transcribe (polled every 3 s while the menu is open). */
export function TranscribeSettings({ open }: { open: boolean }) {
  const [st, setSt] = useState<TranscribeStatus | null | undefined>(undefined); // undefined: loading, null: no API
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback((signal?: AbortSignal) => {
    getTranscribe(signal)
      .then(setSt)
      .catch((e: unknown) => {
        if (!signal?.aborted) {
          setSt(null);
          setErr(e instanceof Error ? e.message : String(e));
        }
      });
  }, []);

  useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    load(ac.signal);
    const id = window.setInterval(() => load(ac.signal), 3000);
    return () => {
      ac.abort();
      window.clearInterval(id);
    };
  }, [open, load]);

  const toggle = async (on: boolean) => {
    setBusy(true);
    setErr(null);
    try {
      setSt(await putTranscribe(on));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const problem = !!st && (st.server === "error" || /model|not found|no such file/i.test(st.last_error));
  return (
    <>
      <div className="label">transcription</div>
      {st === null && <div className="dim pad0">transcription API not available on this backend</div>}
      {st && (
        <>
          <div className="row">
            <label className="chk">
              <input type="checkbox" checked={st.enabled} disabled={busy} onChange={(e) => void toggle(e.target.checked)} /> enabled
            </label>
          </div>
          <div className="pad0">
            <span className="dim">model</span> {st.model || "-"} · <span className="dim">server</span>{" "}
            <span className={st.server === "running" ? "conn-open" : st.server === "error" ? "conn-closed" : "dim"}>{st.server}</span> ·{" "}
            <span className="dim">queue</span> {st.queue}
          </div>
          {st.last_error && <div className="err pad0">{st.last_error}</div>}
          {problem && (
            <div className="dim pad0">
              Setup: <code>brew install whisper-cpp</code>, then <code>make whisper-model</code>.
            </div>
          )}
        </>
      )}
      {err && <div className="err pad0">{err}</div>}
      <div className="dim pad0">Runs locally (whisper.cpp); audio never leaves this computer.</div>
    </>
  );
}
