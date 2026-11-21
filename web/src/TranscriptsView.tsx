import { useEffect, useMemo, useState } from "react";
import { getTranscripts, type Transcript } from "./api";
import { PlayBtn } from "./Clips";

const DEBOUNCE_MS = 300;
const CHIPS = 8;
const clock = (s: number) => new Date(s * 1000).toTimeString().slice(0, 8);
const mhz = (hz: number) => (hz / 1e6).toFixed(3);

function copy(text: string) {
  void navigator.clipboard?.writeText(text).catch(() => undefined);
}

/** Live transcript list with search (server side), channel chips and a silent-clip fold. */
export function TranscriptsView({ items }: { items: Transcript[] }) {
  const [q, setQ] = useState("");
  const [freq, setFreq] = useState<number | null>(null);
  const [silent, setSilent] = useState(false);
  const [found, setFound] = useState<Transcript[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const filtering = q.trim() !== "" || freq !== null;

  // Debounced refetch with the current filters.
  useEffect(() => {
    if (!filtering) {
      setFound(null);
      setErr(null);
      return;
    }
    const ac = new AbortController();
    const id = window.setTimeout(() => {
      getTranscripts({ q: q.trim() || undefined, freq_hz: freq ?? undefined, limit: 100 }, ac.signal)
        .then((l) => {
          setFound(l);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!ac.signal.aborted) setErr(e instanceof Error ? e.message : String(e));
        });
    }, DEBOUNCE_MS);
    return () => {
      window.clearTimeout(id);
      ac.abort();
    };
  }, [q, freq, filtering]);

  // Top channels by clip count, with the latest label.
  const chips = useMemo(() => {
    const m = new Map<number, { n: number; label: string }>();
    for (const t of items) {
      const c = m.get(t.freq_hz) ?? { n: 0, label: "" };
      c.n++;
      if (!c.label && t.label) c.label = t.label;
      m.set(t.freq_hz, c);
    }
    return [...m.entries()].sort((a, b) => b[1].n - a[1].n).slice(0, CHIPS);
  }, [items]);

  // While filtering: server results plus live clips that match and arrived after the fetch.
  const shown = useMemo(() => {
    if (!filtering) return items;
    const ql = q.trim().toLowerCase();
    const have = new Set((found ?? []).map((t) => t.id));
    const live = items.filter((t) => !have.has(t.id) && (freq === null || t.freq_hz === freq) && (ql === "" || t.text.toLowerCase().includes(ql)));
    return (found ?? []).concat(live).sort((a, b) => b.start - a.start);
  }, [filtering, found, items, q, freq]);

  const speech = shown.filter((t) => !t.empty);
  const quiet = shown.length - speech.length;
  const rows = silent ? shown : speech;

  return (
    <div className="trv">
      <div className="trv-bar">
        <input className="trv-q" value={q} placeholder="search text…" spellCheck={false} onChange={(e) => setQ(e.target.value)} />
        <span className="trv-chips">
          {chips.map(([hz, c]) => (
            <button key={hz} className={`mini ${freq === hz ? "on" : ""}`} title={`${c.n} clips`} onClick={() => setFreq(freq === hz ? null : hz)}>
              {mhz(hz)}{c.label ? ` ${c.label}` : ""}
            </button>
          ))}
          {freq !== null && !chips.some(([hz]) => hz === freq) && (
            <button className="mini on" onClick={() => setFreq(null)}>{mhz(freq)} ×</button>
          )}
        </span>
        <span className="grow" />
        <label className="chk dim">
          <input type="checkbox" checked={silent} onChange={(e) => setSilent(e.target.checked)} /> show silent clips ({quiet})
        </label>
      </div>
      {err && <div className="decerr">{err}</div>}
      <div className="trv-list">
        {rows.length === 0 && <div className="dim pad">{filtering ? "no matching transcripts" : "no transcripts yet: they appear when the scanner or a survey hears speech"}</div>}
        {rows.map((t) => (
          <div key={t.id} className={`trv-item ${t.empty ? "silent" : ""}`}>
            <div className="trv-meta">
              <span className="dim">{clock(t.start)}</span>
              <span className="mhz">{mhz(t.freq_hz)} MHz</span>
              {t.label && <span className="lbl">{t.label}</span>}
              <span className={`badge src-${t.source}`}>{t.source}</span>
              <span className="dim">{t.duration_s.toFixed(1)} s</span>
              <span className="grow" />
              <PlayBtn url={t.url} />
              {!t.empty && <button className="mini" title="Copy text" onClick={() => copy(t.text)}>copy</button>}
            </div>
            <div className="trv-text">{t.empty ? <span className="dim">(no speech)</span> : t.text}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
