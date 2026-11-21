import { useEffect, useMemo, useRef, useState } from "react";
import type { Survey, SurveyStep, Transcript } from "./api";
import { TranscriptLine } from "./Clips";
import { surveyTranscript } from "./transcripts";
import { antennaFit, planSurvey, type Antenna, type Band, type BandMode, type Catalog } from "./bands";
import { useSurvey } from "./useSurvey";

const DEFAULT_SECONDS = 20;
const MAX_SAMPLES = 3;
const MAX_PEAKS = 5;

type Rec = Record<string, unknown>;
const str = (v: unknown): string => (typeof v === "string" ? v.trim() : typeof v === "number" && Number.isFinite(v) ? String(v) : "");
const numv = (v: unknown): number | undefined => (typeof v === "number" && Number.isFinite(v) ? v : undefined);
const clip = (s: string, n = 60) => (s.length > n ? s.slice(0, n) + "…" : s);

function fmtDur(s: number): string {
  const t = Math.max(0, Math.round(s));
  return t >= 60 ? `${Math.floor(t / 60)}m ${String(t % 60).padStart(2, "0")}s` : `${t}s`;
}

const SENSOR_KEYS: [string, string, string][] = [
  ["temperature_C", "°C", ""],
  ["temperature_F", "°F", ""],
  ["humidity", "%", " RH"],
  ["pressure_hPa", " hPa", ""],
  ["rain_mm", " mm", " rain"],
  ["wind_avg_km_h", " km/h", " wind"],
  ["power_W", " W", ""],
  ["consumption", "", ""],
];
const SENSOR_SKIP = new Set(["id", "channel", "rssi", "snr", "noise", "freq", "freq1", "freq2", "mic", "time", "battery_ok", "subtype"]);

/** One compact line for a sample event `data` object, per decoder type. */
export function sampleText(decoder: string | undefined, d: Rec): string {
  switch (decoder) {
    case "adsb":
    case "uat": {
      if (d.kind === "fisb" || d.report_type !== undefined) return clip([str(d.report_type), str(d.location)].filter(Boolean).join(" ") || "FIS-B report");
      const alt = d.alt_baro;
      const altS = typeof alt === "number" ? `${Math.round(alt)} ft` : str(alt);
      const al = d.airline && typeof d.airline === "object" ? str((d.airline as Rec).name) : "";
      return [str(d.flight) || str(d.registration) || str(d.hex), al, str(d.type), altS].filter(Boolean).join("  ");
    }
    case "rtl_433": {
      for (const [k, unit, suffix] of SENSOR_KEYS) {
        const v = numv(d[k]);
        if (v !== undefined) return `${str(d.model)} ${+v.toFixed(1)}${unit}${suffix}`.trim();
      }
      for (const [k, v] of Object.entries(d)) {
        const n = numv(v);
        if (n !== undefined && !SENSOR_SKIP.has(k)) return `${str(d.model)} ${k} ${+n.toFixed(2)}`.trim();
      }
      return str(d.model) || "sensor";
    }
    case "aprs":
    case "packet":
      return clip([str(d.from), d.satellite && typeof d.satellite === "object" ? `via ${str((d.satellite as Rec).name)}` : "", str(d.comment) || str(d.info)].filter(Boolean).join("  "));
    case "ais":
      return str(d.name) || (d.mmsi !== undefined ? `MMSI ${str(d.mmsi)}` : "vessel");
    case "radiosonde": {
      const alt = numv(d.alt);
      return [str(d.type), str(d.id), alt !== undefined ? `${Math.round(alt)} m` : ""].filter(Boolean).join(" ");
    }
    case "dsd":
      return [str(d.mode), d.tgt !== undefined ? `TG ${str(d.tgt)}` : "", d.src !== undefined ? `SRC ${str(d.src)}` : ""].filter(Boolean).join(" ");
    case "meters": {
      const c = d.consumption_data !== undefined ? str(d.consumption_data) : str(d.consumption);
      return [str(d.model), d.id !== undefined ? `#${str(d.id)}` : "", c].filter(Boolean).join(" ") || clip(JSON.stringify(d));
    }
    default:
      return clip(JSON.stringify(d));
  }
}

interface Props {
  open: boolean;
  onClose: () => void;
  catalog: Catalog | null;
  /** configured antenna id */
  antennaId: string | null;
  region: string;
  /** reports the one-line summary for the decoder header (null = nothing to say) */
  onSummary: (line: string | null) => void;
  onOpenDecoder: (step: SurveyStep) => void;
  onListen: (hz: number, mode: BandMode) => void;
  onTune: (hz: number) => void;
  /** audio output state and toggle (the control bar's) */
  audioOn?: boolean;
  onAudioToggle?: () => void;
  transcripts: Transcript[];
}

const DEFAULT_LISTEN_S = 8;
const mhzStr = (hz: number) => (hz / 1e6).toFixed(3);

export function SurveyPanel(p: Props) {
  const { open, catalog } = p;
  const sv = useSurvey(open);
  const { survey } = sv;
  const antennas = catalog?.antennas ?? [];

  const [antId, setAntId] = useState("");
  const [region, setRegion] = useState(p.region);
  const [seconds, setSeconds] = useState(DEFAULT_SECONDS);
  const [fair, setFair] = useState(false);
  const [listen, setListen] = useState(true);
  const [listenS, setListenS] = useState(DEFAULT_LISTEN_S);
  const [off, setOff] = useState<Set<string>>(() => new Set());
  const [setupMode, setSetupMode] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const lastSeconds = useRef(DEFAULT_SECONDS);

  // Prefill from config / band panel each time the panel opens.
  useEffect(() => {
    if (!open) return;
    setAntId(p.antennaId ?? "");
    setRegion(p.region);
    setNote(null);
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") p.onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, p.onClose]); // eslint-disable-line react-hooks/exhaustive-deps

  // Summary line for the decoder panel header.
  const summary = useMemo(() => {
    if (!survey || survey.state === "idle" || survey.steps.length === 0) return null;
    if (survey.state === "running") return "Survey running…";
    const counted = survey.steps.filter((s) => s.state !== "skipped");
    const found = counted.filter((s) => (s.found ?? 0) > 0).length;
    return `Last survey: ${found} of ${counted.length} found data`;
  }, [survey]);
  const onSummary = p.onSummary;
  useEffect(() => onSummary(summary), [summary, onSummary]);

  const ant: Antenna | undefined = antennas.find((a) => a.id === antId);
  const plan = useMemo(
    () => (catalog && ant ? planSurvey(catalog, region, ant, fair, seconds) : []),
    [catalog, ant, region, fair, seconds],
  );
  const planKey = `${antId}|${region}|${fair}`;
  useEffect(() => setOff(new Set()), [planKey]);
  const runnable = plan.filter((s) => !s.skipped);
  const chosen = runnable.filter((s) => !off.has(s.id));
  const estimate = chosen.reduce((t, s) => t + s.seconds, 0) + (listen ? listenS * chosen.filter((s) => s.band.mode !== "none" && s.id.startsWith("scan:")).length : 0);

  const running = survey?.state === "running";
  const hasResult = !!survey && survey.state !== "idle" && survey.steps.length > 0;
  const showSetup = !running && (!hasResult || setupMode);

  const start = async () => {
    if (!ant) return;
    lastSeconds.current = seconds;
    const ok = await sv.start({
      antenna: ant.id,
      region,
      seconds,
      include_fair: fair,
      listen,
      ...(listen ? { listen_seconds: listenS } : {}),
      ...(chosen.length < runnable.length ? { steps: chosen.map((s) => s.id) } : {}),
    });
    if (ok) setSetupMode(false);
  };

  const tryLonger = async (st: SurveyStep) => {
    if (!survey) return;
    const secs = Math.min(120, Math.max(5, Math.round((lastSeconds.current || DEFAULT_SECONDS) * 3)));
    const ids = [st.id];
    // dsd takes its frequency from the scan of its band, so repeat that scan first
    if (st.decoder === "dsd") {
      const scan = survey.steps.find((x) => x.kind === "scan" && x.band === st.band);
      if (scan) ids.unshift(scan.id);
    }
    lastSeconds.current = secs;
    await sv.start({ antenna: survey.antenna, region: survey.region, seconds: secs, steps: ids });
  };

  const bandName = (id: string | undefined): string => {
    if (!id) return "Other";
    return catalog?.bands.find((b) => b.id === id)?.name ?? id;
  };
  const bandOf = (id: string | undefined): Band | undefined => (id ? catalog?.bands.find((b) => b.id === id) : undefined);

  if (!open) return null;

  return (
    <aside className="sv" role="dialog" aria-label="What's on the air?">
      <div className="bar sv-head">
        <strong>◎ What's on the air?</strong>
        {survey && survey.state !== "idle" && <span className={`dim`}>{survey.state}</span>}
        <span style={{ flex: 1 }} />
        <button onClick={p.onClose} title="Close (the survey keeps running)">✕</button>
      </div>
      <div className="sv-body">
        {sv.error && <div className="decerr">{sv.error}</div>}
        {survey?.state === "running" && survey.listening && (
          <div className="sv-now" role="status">
            ▶ now playing {mhzStr(survey.listening.freq_hz)} MHz
            <span className="dim"> {survey.listening.mode.toUpperCase()}{survey.listening.label ? ` · ${survey.listening.label}` : ""}</span>
            {!p.audioOn && p.onAudioToggle && <span className="dim"> (audio is off)</span>}
          </div>
        )}
        {showSetup ? (
          <Setup
            catalog={catalog}
            antennas={antennas}
            antId={antId}
            setAntId={setAntId}
            region={region}
            setRegion={setRegion}
            seconds={seconds}
            setSeconds={setSeconds}
            fair={fair}
            setFair={setFair}
            listen={listen}
            setListen={setListen}
            listenS={listenS}
            setListenS={setListenS}
            audioOn={!!p.audioOn}
            onAudioToggle={p.onAudioToggle}
            plan={plan}
            off={off}
            setOff={setOff}
            estimate={estimate}
            count={chosen.length}
            busy={sv.busy}
            onStart={() => void start()}
            canCancel={hasResult}
            onCancel={() => setSetupMode(false)}
            hasAnt={!!ant}
          />
        ) : (
          survey && (
            <Progress
              survey={survey}
              bandName={bandName}
              bandOf={bandOf}
              note={note}
              transcripts={p.transcripts}
              onStop={sv.stop}
              onNew={() => setSetupMode(true)}
              busy={sv.busy}
              onOpenDecoder={(s) => {
                p.onOpenDecoder(s);
                p.onClose();
              }}
              onListen={(hz, mode) => {
                p.onListen(hz, mode);
                setNote(`listening at ${(hz / 1e6).toFixed(4)} MHz`);
              }}
              onTune={(hz) => {
                p.onTune(hz);
                setNote(`tuned to ${(hz / 1e6).toFixed(4)} MHz`);
              }}
              onTryLonger={(s) => void tryLonger(s)}
            />
          )
        )}
      </div>
    </aside>
  );
}

function Setup(p: {
  catalog: Catalog | null;
  antennas: Antenna[];
  antId: string;
  setAntId: (s: string) => void;
  region: string;
  setRegion: (s: string) => void;
  seconds: number;
  setSeconds: (n: number) => void;
  fair: boolean;
  setFair: (b: boolean) => void;
  listen: boolean;
  setListen: (b: boolean) => void;
  listenS: number;
  setListenS: (n: number) => void;
  audioOn: boolean;
  onAudioToggle?: () => void;
  plan: ReturnType<typeof planSurvey>;
  off: Set<string>;
  setOff: (s: Set<string>) => void;
  estimate: number;
  count: number;
  busy: boolean;
  onStart: () => void;
  canCancel: boolean;
  onCancel: () => void;
  hasAnt: boolean;
}) {
  const toggle = (id: string) => {
    const n = new Set(p.off);
    if (n.has(id)) n.delete(id);
    else n.add(id);
    p.setOff(n);
  };
  const regions = p.catalog ? Object.entries(p.catalog.regions) : [];
  const ant = p.antennas.find((a) => a.id === p.antId);
  return (
    <>
      <p className="dim pad0">
        Runs a short band scan and each decoder in turn with your antenna and reports what was heard. The survey takes over the radio
        and restores your tuning afterwards.
      </p>
      <div className="sv-form">
        <label>
          <span className="label">antenna (required)</span>
          <select value={p.antId} onChange={(e) => p.setAntId(e.target.value)}>
            <option value="">(choose)</option>
            {p.antennas.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </label>
        <label>
          <span className="label">region</span>
          <select value={p.region} onChange={(e) => p.setRegion(e.target.value)}>
            {regions.map(([id, name]) => (
              <option key={id} value={id}>{id.toUpperCase()} - {name}</option>
            ))}
          </select>
        </label>
        <label>
          <span className="label">seconds per decoder: {p.seconds}</span>
          <input type="range" min={10} max={60} step={5} value={p.seconds} onChange={(e) => p.setSeconds(Number(e.target.value))} style={{ width: "100%" }} />
        </label>
        <label className="chk">
          <input type="checkbox" checked={p.fair} onChange={(e) => p.setFair(e.target.checked)} /> include off-tune bands
        </label>
        <div className="sv-listen">
          <label className="chk">
            <input type="checkbox" checked={p.listen} onChange={(e) => p.setListen(e.target.checked)} /> Play what the survey hears
            <input
              type="number"
              min={2}
              max={30}
              value={p.listenS}
              disabled={!p.listen}
              aria-label="seconds per voice band"
              onChange={(e) => p.setListenS(Math.min(30, Math.max(2, Math.round(Number(e.target.value)) || DEFAULT_LISTEN_S)))}
              style={{ width: "4.5em" }}
            />
            <span className="dim">s</span>
          </label>
          <span className="dim">turn audio on to hear each voice band's strongest carrier for {p.listenS} s</span>
          {p.listen && !p.audioOn && p.onAudioToggle && (
            <span className="sv-nudge">
              Audio is off, so you will not hear it. <button type="button" className="mini" onClick={p.onAudioToggle}>Turn audio on</button>
            </span>
          )}
        </div>
      </div>
      {!p.hasAnt && <div className="dim pad0">Choose your antenna to see the plan.</div>}
      {p.hasAnt && p.plan.length === 0 && (
        <div className="dim pad0">No band fits this antenna in this region. Try "include off-tune bands".</div>
      )}
      {p.plan.length > 0 && (
        <>
          <div className="label">
            plan{ant ? ` (${p.count} steps, about ${fmtDur(p.estimate)})` : ""}
            <span className="sv-plan-all">
              <button type="button" className="link" onClick={() => p.setOff(new Set())}>select all</button>
              {" · "}
              <button
                type="button"
                className="link"
                onClick={() => p.setOff(new Set(p.plan.filter((s) => !s.skipped).map((s) => s.id)))}
              >
                deselect all
              </button>
            </span>
          </div>
          <div className="sv-plan">
            {p.plan.map((s) => (
              <label key={s.id} className={`sv-plan-row ${s.skipped ? "dim" : ""}`} title={s.id}>
                <input type="checkbox" disabled={!!s.skipped} checked={!s.skipped && !p.off.has(s.id)} onChange={() => toggle(s.id)} />
                <span className="sv-plan-name">{s.label}</span>
                <span className="dim">
                  {s.skipped
                    ? `skipped: ${s.skipped}`
                    : `~${fmtDur(s.seconds)}${ant && antennaFit(ant, s.band.tune_mhz) === "fair" ? " · off-tune" : ""}`}
                </span>
              </label>
            ))}
          </div>
        </>
      )}
      <div className="row sv-actions">
        <button className="primary" disabled={!p.hasAnt || p.count === 0 || p.busy} onClick={p.onStart}>
          {p.busy ? "Starting…" : "Start survey"}
        </button>
        {p.canCancel && <button onClick={p.onCancel}>Back to results</button>}
      </div>
    </>
  );
}

function rank(s: SurveyStep): number {
  if ((s.found ?? 0) > 0) return 0;
  if (s.state === "done") return 1;
  if (s.state === "error") return 2;
  if (s.state === "running" || s.state === "pending") return 1;
  return 3;
}

function Progress(p: {
  survey: Survey;
  bandName: (id: string | undefined) => string;
  bandOf: (id: string | undefined) => Band | undefined;
  note: string | null;
  transcripts: Transcript[];
  busy: boolean;
  onStop: () => void;
  onNew: () => void;
  onOpenDecoder: (s: SurveyStep) => void;
  onListen: (hz: number, mode: BandMode) => void;
  onTune: (hz: number) => void;
  onTryLonger: (s: SurveyStep) => void;
}) {
  const { survey } = p;
  const running = survey.state === "running";
  const est = survey.estimated_s > 0 ? survey.estimated_s : Math.max(1, survey.elapsed_s);
  const pct = running ? Math.min(99, (survey.elapsed_s / est) * 100) : survey.state === "done" ? 100 : Math.min(100, (survey.elapsed_s / est) * 100);

  const groups = useMemo(() => {
    const m = new Map<string, SurveyStep[]>();
    for (const s of survey.steps) {
      const k = s.band ?? "";
      const a = m.get(k);
      if (a) a.push(s);
      else m.set(k, [s]);
    }
    return [...m.entries()];
  }, [survey.steps]);
  const sorted = useMemo(
    () => survey.steps.map((s, i) => ({ s, i })).sort((a, b) => rank(a.s) - rank(b.s) || a.i - b.i).map((x) => x.s),
    [survey.steps],
  );

  const card = (s: SurveyStep) => (
    <StepCard
      key={s.id}
      step={s}
      band={p.bandOf(s.band)}
      finished={!running}
      transcript={s.listened ? surveyTranscript(p.transcripts, s.listened.freq_hz, survey.started ?? 0) : undefined}
      {...p}
    />
  );

  return (
    <>
      <div className="sv-prog">
        <div className="sv-bar"><div style={{ width: `${pct}%` }} /></div>
        <div className="row">
          <span>
            {running ? "running" : survey.state} · {fmtDur(survey.elapsed_s)}
            {survey.estimated_s > 0 ? ` of about ${fmtDur(survey.estimated_s)}` : ""}
          </span>
          <span className="dim">{[survey.antenna, survey.region?.toUpperCase()].filter(Boolean).join(" · ")}</span>
          <span style={{ flex: 1 }} />
          {running ? <button onClick={p.onStop}>Stop</button> : <button className="primary" disabled={p.busy} onClick={p.onNew}>New survey</button>}
        </div>
        {p.note && <div className="conn-open">{p.note}</div>}
      </div>
      {running
        ? groups.map(([band, steps]) => (
            <div key={band} className="sv-group">
              <div className="label">{p.bandName(band || undefined)}</div>
              <div className="sv-cards">{steps.map(card)}</div>
            </div>
          ))
        : <div className="sv-cards">{sorted.map(card)}</div>}
    </>
  );
}

function StepIcon({ state }: { state: SurveyStep["state"] }) {
  switch (state) {
    case "running":
      return <span className="spin" title="running" />;
    case "done":
      return <span className="conn-open" title="done">✓</span>;
    case "error":
      return <span className="err" style={{ margin: 0 }} title="error">!</span>;
    case "skipped":
      return <span className="dim" title="skipped">–</span>;
    default:
      return <span className="dim" title="pending">○</span>;
  }
}

function StepCard(p: {
  step: SurveyStep;
  transcript?: Transcript;
  band: Band | undefined;
  finished: boolean;
  onOpenDecoder: (s: SurveyStep) => void;
  onListen: (hz: number, mode: BandMode) => void;
  onTune: (hz: number) => void;
  onTryLonger: (s: SurveyStep) => void;
}) {
  const s = p.step;
  const found = s.found ?? 0;
  const secs = s.seconds !== undefined ? `${s.seconds.toFixed(s.seconds < 10 ? 1 : 0)} s` : "";
  const mode = p.band?.mode ?? "none";
  const peaks = [...(s.peaks ?? [])].sort((a, b) => b.snr_db - a.snr_db).slice(0, MAX_PEAKS);
  const samples = (s.samples ?? []).slice(0, MAX_SAMPLES);
  const nothing = s.state === "done" && found === 0;
  return (
    <div className={`sv-card ${found > 0 ? "found" : ""} ${s.state}`}>
      <div className="sv-card-head">
        <StepIcon state={s.state} />
        <span className="sv-card-title">{s.label ?? s.id}</span>
        {found > 0 && <span className="badge okb">{found} found</span>}
        <span className="dim">{secs}</span>
      </div>
      {s.state === "running" && <div className="dim">{found > 0 ? `${found} so far${s.summary ? ` · ${s.summary}` : ""}` : "listening…"}</div>}
      {s.state === "done" && s.summary && <div>{s.summary}</div>}
      {s.listened && (
        <div className="sv-listened">
          ▶ listened {Math.round(s.listened.seconds)} s to {mhzStr(s.listened.freq_hz)} MHz {s.listened.mode.toUpperCase()} ({s.listened.level_db.toFixed(0).replace("-", "−")} dB)
          {(() => {
            const l = s.listened;
            const lbl = l.label || s.peaks?.find((x) => Math.abs(x.freq_hz - l.freq_hz) < 5000 && x.label)?.label;
            return lbl ? <span className="lbl"> {lbl}</span> : null;
          })()}
        </div>
      )}
      {s.listened && p.transcript && <div className="sv-tr"><TranscriptLine t={p.transcript} /></div>}
      {nothing && <div className="dim">{s.kind === "scan" ? "no carriers found" : `nothing heard in ${Math.round(s.seconds ?? 0)} s`}</div>}
      {s.state === "skipped" && <div className="dim">skipped{s.reason ? `: ${s.reason}` : ""}</div>}
      {s.state === "error" && <div className="err" style={{ margin: 0, whiteSpace: "pre-wrap" }}>{s.error ?? "error"}</div>}
      {s.kind === "decoder" && samples.length > 0 && (
        <ul className="sv-samples">
          {samples.map((d, i) => (
            <li key={i}>{sampleText(s.decoder, d)}</li>
          ))}
        </ul>
      )}
      {s.kind === "scan" && peaks.length > 0 && (
        <div className="sv-peaks">
          {peaks.map((pk) => (
            <div key={pk.freq_hz} className="sv-peak">
              <span className="mhz">{(pk.freq_hz / 1e6).toFixed(3)} MHz</span>
              {pk.label && <span className="lbl" title="From the band catalog or a visible satellite downlink">{pk.label}</span>}
              <span className="dim">{Math.round(pk.snr_db)} dB SNR</span>
              <span style={{ flex: 1 }} />
              {mode !== "none" && (
                <button className="mini" onClick={() => p.onListen(pk.freq_hz, mode)} title={`Listen (${mode})`}>Listen</button>
              )}
              <button className="mini" onClick={() => p.onTune(pk.freq_hz)} title="Tune the center here">Tune</button>
            </div>
          ))}
        </div>
      )}
      {p.finished && (
        <div className="row">
          {s.kind === "decoder" && s.decoder && found > 0 && <button className="mini" onClick={() => p.onOpenDecoder(s)}>Open</button>}
          {s.kind === "decoder" && (nothing || (s.state === "done" && found > 0)) && (
            <button className="mini" onClick={() => p.onTryLonger(s)} title="Run just this step again with 3x the seconds">Try longer</button>
          )}
        </div>
      )}
    </div>
  );
}
