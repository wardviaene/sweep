import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import type { ScannerParams, ScannerTx, Transcript } from "./api";
import { TranscriptLine } from "./Clips";
import { matchTx } from "./transcripts";
import { bandRange, type Band, type Catalog } from "./bands";
import type { AudioApi } from "./useAudio";
import type { DecEvent } from "./useEvents";
import type { ScannerApi } from "./useScanner";
import { parseFreq } from "./Controls";

const MODES = ["am", "nfm", "usb", "lsb"] as const;
const MAX_LOG = 100;
const VOICE = new Set(["nfm", "am", "usb", "lsb"]);

/** Voice bands the scanner can cover (24-1766 MHz, not HF direct sampling). */
export function isVoiceBand(b: Band): boolean {
  return VOICE.has(b.mode) && !b.hf && b.lo_mhz >= 24;
}

const mhz = (hz: number) => (hz / 1e6).toFixed(3);
const mhzR = (hz: number) => +(hz / 1e6).toFixed(4);
const clock = (s: number) => new Date(s * 1000).toTimeString().slice(0, 8);
function fmtDur(s: number): string {
  if (s < 60) return `${s.toFixed(1)} s`;
  const t = Math.round(s);
  return `${Math.floor(t / 60)}m ${String(t % 60).padStart(2, "0")}s`;
}

interface Props {
  scanner: ScannerApi;
  audio: AudioApi;
  catalog: Catalog | null;
  region: string;
  events: DecEvent[];
  transcripts: Transcript[];
  onListen: (hz: number, mode: string) => void;
  onClose: () => void;
}

export function ScannerPanel({ scanner, audio, catalog, region, events, transcripts, onListen, onClose }: Props) {
  const { state, running } = scanner;
  const [expanded, setExpanded] = useState(true);
  const [sel, setSel] = useState("custom");
  const [mode, setMode] = useState<string>("am");
  const [lo, setLo] = useState("118");
  const [hi, setHi] = useState("137");
  const [adv, setAdv] = useState({ margin: "", hang: "", dwell: "", step: "" });
  const [localErr, setLocalErr] = useState<string | null>(null);

  const bands = useMemo(() => (catalog ? catalog.bands.filter((b) => b.regions.includes(region) && isVoiceBand(b)) : []), [catalog, region]);

  // Pre-select airband once the catalog is there.
  const preset = useRef(false);
  useEffect(() => {
    if (preset.current || bands.length === 0) return;
    preset.current = true;
    pick((bands.find((b) => b.id === "air") ?? bands[0]!).id);
  }, [bands]); // eslint-disable-line react-hooks/exhaustive-deps

  function pick(id: string) {
    setSel(id);
    const b = bands.find((x) => x.id === id);
    if (!b) return;
    const [l, h] = bandRange(b, region);
    setLo(String(l));
    setHi(String(h));
    if ((MODES as readonly string[]).includes(b.mode)) setMode(b.mode);
  }

  const num = (t: string, scale = 1): number | undefined => {
    const v = parseFloat(t);
    return Number.isFinite(v) && v > 0 ? v * scale : undefined;
  };

  const doStart = () => {
    const l = parseFreq(lo);
    const h = parseFreq(hi);
    if (l === null || h === null || h <= l) return setLocalErr("invalid range (MHz, low < high)");
    setLocalErr(null);
    const p: ScannerParams = { lo_hz: l, hi_hz: h, mode };
    if (sel !== "custom") p.band = sel;
    const m = num(adv.margin);
    if (m !== undefined) p.margin_db = m;
    const hg = num(adv.hang);
    if (hg !== undefined) p.hang_s = hg;
    const dw = num(adv.dwell);
    if (dw !== undefined) p.dwell_ms = Math.round(dw);
    const st = num(adv.step, 1000);
    if (st !== undefined) p.step_hz = Math.round(st);
    if (state && state.lockout_hz.length) p.lockout_hz = state.lockout_hz;
    void scanner.start(p);
  };

  // Log: polled list plus events that arrived since (not yet in the poll).
  const log = useMemo(() => {
    const base = state?.recent ?? [];
    const since = state?.started ?? 0;
    const extra: ScannerTx[] = [];
    for (let i = events.length - 1; i >= 0 && extra.length < MAX_LOG; i--) {
      const e = events[i]!;
      if (e.decoder !== "scanner" || e.kind !== "message") continue;
      const d = e.data;
      const f = d.freq_hz;
      const s = d.start;
      if (typeof f !== "number" || typeof s !== "number" || s < since) continue;
      if (base.some((r) => r.freq_hz === f && Math.abs(r.start - s) < 0.5)) continue;
      extra.push({
        ...(typeof d.id === "string" ? { id: d.id } : {}),
        freq_hz: f,
        label: typeof d.label === "string" ? d.label : "",
        start: s,
        duration_s: typeof d.duration_s === "number" ? d.duration_s : 0,
        peak_db: typeof d.peak_db === "number" ? d.peak_db : 0,
        mode: typeof d.mode === "string" ? d.mode : undefined,
      });
    }
    return extra.concat(base).sort((a, b) => b.start - a.start).slice(0, MAX_LOG);
  }, [state?.recent, state?.started, events]);

  // Transcript per row; "transcribing…" for 30 s after a transmission ended.
  const byId = useMemo(() => new Map(transcripts.map((t) => [t.id, t])), [transcripts]);
  const rows = useMemo(() => log.map((r) => ({ r, t: matchTx(r, byId, transcripts) })), [log, byId, transcripts]);
  const nowS = Date.now() / 1000;

  // Locked duration: server timestamp when the clocks agree, else first-seen on this client.
  const firstSeen = useRef<{ key: string; t: number }>({ key: "", t: 0 });
  const a = state?.active ?? null;
  let lockedFor = 0;
  if (state?.state === "locked" && a) {
    const key = `${a.freq_hz}:${a.since}`;
    if (firstSeen.current.key !== key) firstSeen.current = { key, t: Date.now() / 1000 };
    const d = Date.now() / 1000 - a.since;
    lockedFor = d >= 0 && d < 3600 ? d : Date.now() / 1000 - firstSeen.current.t;
  }

  const w = state?.window ?? null;
  const prio = new Set(scanner.priority);
  const err = scanner.error ?? localErr;
  const mode_ = state?.mode ?? mode;

  const actions = (hz: number, label?: string) => (
    <span className="scn-act">
      <button className="mini" title="Stop Listen live and tune here" onClick={() => onListen(hz, mode_)}>Listen</button>
      <button className="mini" title={`Never stop on ${mhz(hz)} MHz${label ? ` (${label})` : ""}`} onClick={() => scanner.lockout(hz)}>Lockout</button>
      <button className={`mini ${prio.has(hz) ? "on" : ""}`} disabled={!running || prio.has(hz) || scanner.busy} title="Let this channel interrupt a running transmission" onClick={() => scanner.prioritize(hz)}>
        {prio.has(hz) ? "Priority" : "Prioritize"}
      </button>
    </span>
  );

  return (
    <div className="scn">
      <div className="bar scn-head">
        <strong title="Plays each transmission as it happens; runs until you stop it">🎧 Listen live — scanner</strong>
        <span className={`scn-status ${state?.state ?? "idle"}`}>
          {state?.state === "locked" && a ? (
            <>
              <i className="scn-pulse" />▶ {mhz(a.freq_hz)} MHz · {lockedFor.toFixed(1)} s · {a.level_db.toFixed(0)} dB
              {a.label ? <span className="dim"> {a.label}</span> : null}
            </>
          ) : state?.state === "watching" ? (
            <>
              <i className="scn-dot" />
              watching {mhz(w?.lo_hz ?? state.lo_hz)}–{mhz(w?.hi_hz ?? state.hi_hz)} MHz
              {w ? ` (window ${w.index + 1}/${w.count})` : ""}
            </>
          ) : (
            <span className="dim">{state?.state === "stopped" ? "stopped" : "idle"}</span>
          )}
        </span>
        {w && w.count > 1 && running && (
          <span className="scn-dots" title={`window ${w.index + 1} of ${w.count}`}>
            {Array.from({ length: Math.min(w.count, 40) }, (_, i) => (
              <i key={i} className={i === w.index ? "on" : ""} />
            ))}
          </span>
        )}
        <span className="grow" />
        {running ? (
          <button className="danger" disabled={scanner.busy} onClick={() => void scanner.stop()}>Stop</button>
        ) : (
          <button className="primary" disabled={scanner.busy} onClick={doStart}>Start</button>
        )}
        <button onClick={() => setExpanded(!expanded)} title={expanded ? "Collapse" : "Expand"}>{expanded ? "▾" : "▴"}</button>
        <button onClick={onClose} title="Hide this strip (Listen live keeps running)">×</button>
      </div>
      {err && <div className="decerr">{err}</div>}
      {!audio.on && (
        <div className="scn-audio">
          <button className="primary" onClick={audio.toggle}>Turn audio on to hear transmissions</button>
        </div>
      )}
      {expanded && (
        <div className="scn-body">
          <p className="dim scn-intro">
            Watches a band and plays each transmission as it happens. Runs until you stop it. For a one-off report of what your antenna can
            receive, use Explore → Discover.
          </p>
          <div className="scn-form">
            <label>
              <span className="label">band</span>
              <select value={sel} disabled={running} onChange={(e) => pick(e.target.value)}>
                <option value="custom">custom range</option>
                {bands.map((b) => (
                  <option key={b.id} value={b.id}>{b.name}</option>
                ))}
              </select>
            </label>
            <label>
              <span className="label">from MHz</span>
              <input className="freq narrow" value={lo} disabled={running || sel !== "custom"} spellCheck={false} onChange={(e) => setLo(e.target.value)} />
            </label>
            <label>
              <span className="label">to MHz</span>
              <input className="freq narrow" value={hi} disabled={running || sel !== "custom"} spellCheck={false} onChange={(e) => setHi(e.target.value)} />
            </label>
            <label>
              <span className="label">mode</span>
              <select value={mode} disabled={running} onChange={(e) => setMode(e.target.value)}>
                {MODES.map((m) => (
                  <option key={m} value={m}>{m.toUpperCase()}</option>
                ))}
              </select>
            </label>
            <details className="scn-adv">
              <summary className="label">advanced</summary>
              <div className="scn-advrow">
                {(
                  [
                    ["margin", "margin dB", "10"],
                    ["hang", "hang s", "2"],
                    ["dwell", "dwell ms", "1500"],
                    ["step", "step kHz", "auto"],
                  ] as const
                ).map(([k, name, ph]) => (
                  <label key={k}>
                    <span className="label">{name}</span>
                    <input className="freq narrow" value={adv[k]} placeholder={ph} disabled={running} spellCheck={false} onChange={(e) => setAdv({ ...adv, [k]: e.target.value })} />
                  </label>
                ))}
              </div>
            </details>
            <span className="dim">{running ? "Listen live controls the radio" : "Starting stops a running decoder."}</span>
          </div>
          <div className="scn-cols">
            <div className="scn-col">
              <h4>activity ({log.length})</h4>
              <div className="scn-scroll">
                {log.length === 0 && <div className="dim">{running ? "no transmissions yet" : "nothing yet"}</div>}
                <table>
                  <tbody>
                    {rows.map(({ r, t }) => {
                      const pending = !t && nowS < r.start + r.duration_s + 30;
                      return (
                        <Fragment key={`${r.freq_hz}:${r.start}`}>
                          <tr>
                            <td className="dim">{clock(r.start)}</td>
                            <td className="mhz">{mhz(r.freq_hz)}</td>
                            <td className="lbl">{r.label ?? ""}</td>
                            <td>{fmtDur(r.duration_s)}</td>
                            <td>{r.peak_db.toFixed(0)} dB</td>
                            <td>{actions(r.freq_hz, r.label)}</td>
                          </tr>
                          {(t || pending) && (
                            <tr className="tr-row">
                              <td colSpan={6}><TranscriptLine t={t} pending={pending} /></td>
                            </tr>
                          )}
                        </Fragment>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>
            <div className="scn-col">
              <h4>most active channels</h4>
              <div className="scn-scroll">
                {(state?.channels.length ?? 0) === 0 && <div className="dim">none yet</div>}
                <table>
                  <tbody>
                    {state?.channels.map((c) => (
                      <tr key={c.freq_hz}>
                        <td className="mhz">{mhz(c.freq_hz)}</td>
                        <td className="lbl">{c.label ?? ""}</td>
                        <td>{c.count}×</td>
                        <td>{fmtDur(c.total_s)}</td>
                        <td>{actions(c.freq_hz, c.label)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {(state?.lockout_hz.length ?? 0) > 0 && (
                <div className="scn-lock">
                  <span className="label">lockout</span>
                  {state!.lockout_hz.map((hz) => (
                    <span key={hz} className="chip">
                      {mhzR(hz)}
                      <button className="mini" title="Remove lockout" onClick={() => scanner.lockout(hz, true)}>×</button>
                    </span>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
