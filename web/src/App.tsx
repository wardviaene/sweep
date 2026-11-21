import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SpectrumView, type Vfo } from "./SpectrumView";
import { useSpectrum } from "./useSpectrum";
import { useStatus } from "./useStatus";
import { useTuner } from "./useTuner";
import { Controls } from "./Controls";
import { DemodControls } from "./DemodControls";
import { BandPanel } from "./BandPanel";
import { DecoderPanel } from "./DecoderPanel";
import { useAudio } from "./useAudio";
import { useDemod } from "./useDemod";
import { useBands } from "./useBands";
import { useEvents } from "./useEvents";
import { Toasts } from "./Toasts";
import { useConfig } from "./useConfig";
import { ToolsMenu } from "./ToolsMenu";
import { SettingsMenu } from "./SettingsMenu";
import { SurveyPanel } from "./SurveyPanel";
import { ScannerPanel } from "./ScannerPanel";
import { useScanner } from "./useScanner";
import { useSurvey } from "./useSurvey";
import { useAutopilot } from "./useAutopilot";
import { ExploreMenu, RadioPill, type PanelKind } from "./RadioStatus";
import { useTranscripts } from "./transcripts";
import type { LatLon } from "./geo";
import { fmtMHz } from "./draw";
import { autopilotStop, setDecoder, type DecoderName, type DecoderOpts, type SurveyStep } from "./api";
import { bandRange, bandViews, loadRegion, saveRegion, type Band, type BandMode, type Channel } from "./bands";


export function App() {
  const { stream, conn, meta, fps } = useSpectrum();
  const { status, error, applyStatus } = useStatus(2000);
  const tuner = useTuner(status, applyStatus);
  const demod = useDemod(applyStatus);
  const audio = useAudio();
  const { catalog, error: bandsError } = useBands();
  const events = useEvents();
  const transcripts = useTranscripts(events);
  const config = useConfig();
  const [toolsOpen, setToolsOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [mapCenter, setMapCenter] = useState<(() => LatLon) | null>(null);
  const registerCenter = useCallback((g: (() => LatLon) | null) => setMapCenter(() => g), []);
  const [step, setStep] = useState(100e3);
  const [regionPref, setRegionPref] = useState<string | null>(loadRegion);
  const [surveyOpen, setSurveyOpen] = useState(false);
  const [scanOpen, setScanOpen] = useState(false);
  const scanner = useScanner(scanOpen);
  const scanning = scanner.running;
  const sv = useSurvey(surveyOpen);
  const ap = useAutopilot(true, 2000);
  const [satSeq, setSatSeq] = useState(0);
  const surveyRunning = sv.survey?.state === "running";
  const pass = ap.state?.current ?? null;
  const busyNote = pass
    ? "Satellite pass in progress"
    : surveyRunning
      ? "Discover is using the radio"
      : scanning
        ? "Listen live controls the radio"
        : null;
  const locked = busyNote !== null;
  const SCAN_BUSY = `${busyNote ?? "Radio busy"}: stop it to tune manually`;
  const [surveyLine, setSurveyLine] = useState<string | null>(null);
  const [decFocus, setDecFocus] = useState<{ decoder: DecoderName; seq: number } | null>(null);
  const [decErr, setDecErr] = useState<string | null>(null);
  const [actErr, setActErr] = useState<string | null>(null);
  const errTimer = useRef<number | undefined>(undefined);

  const center = meta?.center_hz ?? status?.center_hz;
  const rate = meta?.sample_rate ?? status?.sample_rate;

  const region = regionPref && catalog?.regions[regionPref] ? regionPref : (catalog?.default_region ?? "us");
  const setRegion = (r: string) => {
    setRegionPref(r);
    saveRegion(r);
  };
  const views = useMemo(() => bandViews(catalog, region), [catalog, region]);

  const sw = scanner.state?.window ?? null;
  const scanAct = scanner.state?.state === "locked" ? (scanner.state.active?.freq_hz ?? null) : null;
  const scanOverlay = useMemo(
    () => (scanning && sw ? { lo_hz: sw.lo_hz, hi_hz: sw.hi_hz, active_hz: scanAct } : null),
    [scanning, sw?.lo_hz, sw?.hi_hz, scanAct], // eslint-disable-line react-hooks/exhaustive-deps
  );

  const d = status?.demod;
  const vfo: Vfo | null = useMemo(
    () => (d && d.mode !== "off" ? { mode: d.mode, freq_hz: d.freq_hz, bandwidth_hz: d.bandwidth_hz } : null),
    [d?.mode, d?.freq_hz, d?.bandwidth_hz], // eslint-disable-line react-hooks/exhaustive-deps
  );

  const fail = useCallback((e: unknown) => {
    setActErr(e instanceof Error ? e.message : String(e));
    window.clearTimeout(errTimer.current);
    errTimer.current = window.setTimeout(() => setActErr(null), 4000);
  }, []);
  useEffect(() => () => window.clearTimeout(errTimer.current), []);

  const startDecoder = useCallback(
    (name: DecoderName | "none", opts?: DecoderOpts) => {
      setDecErr(null);
      setDecoder(name, region, opts)
        .then(applyStatus)
        .catch((e: unknown) => {
          setDecErr(e instanceof Error ? e.message : String(e));
          fail(e);
        });
    },
    [region, applyStatus, fail],
  );

  // Jump: with a demodulator, park the center a quarter span below the target so the VFO
  // sits off the DC spike (same offset sdrd uses), then set the VFO. Otherwise just tune.
  const jump = (b: Band) => {
    if (locked) return fail(new Error(SCAN_BUSY));
    const hz = Math.round(b.tune_mhz * 1e6);
    if (b.mode === "none") return tuner.setFreq(hz);
    const r = status?.sample_rate ?? 2.048e6;
    void tuner.tuneNow(hz - r / 4).then((ok) => {
      if (ok) demod.send({ mode: b.mode as "nfm", freq_hz: hz }, true);
    });
  };
  const openSurveyDecoder = (s: SurveyStep) => {
    if (!s.decoder) return;
    const opts: DecoderOpts | undefined =
      s.freq_hz !== undefined && (s.decoder === "dsd" || s.decoder === "packet" || s.decoder === "radiosonde")
        ? { freq_hz: s.freq_hz }
        : undefined;
    startDecoder(s.decoder, opts);
    setDecFocus((f) => ({ decoder: s.decoder!, seq: (f?.seq ?? 0) + 1 }));
  };
  const surveyListen = (hz: number, mode: BandMode) => {
    if (mode === "none") return;
    const r = status?.sample_rate ?? 2.048e6;
    void tuner.tuneNow(hz - r / 4).then((ok) => {
      if (ok) demod.send({ mode, freq_hz: hz }, true);
    });
  };
  const scanBand = (b: Band) => {
    const [lo, hi] = bandRange(b, region);
    setScanOpen(true);
    void scanner.start({ band: b.id, lo_hz: Math.round(lo * 1e6), hi_hz: Math.round(hi * 1e6), mode: b.mode });
  };
  const scanListen = (hz: number, mode: string) => {
    void scanner.stop().then((ok) => {
      if (ok) surveyListen(hz, mode as BandMode);
    });
  };
  const antennas = catalog?.antennas ?? [];
  const antenna = antennas.find((a) => a.id === config.antenna) ?? null;
  const pickChannel = (_b: Band, c: Channel) => {
    if (locked) return fail(new Error(SCAN_BUSY));
    const hz = Math.round(c.mhz * 1e6);
    if (c.mode !== "none") demod.send({ mode: c.mode, freq_hz: hz }, true);
    else tuner.setFreq(hz);
  };

  // Who controls the radio (priority: pass, Discover, Listen live, decoder, VFO). Strings only, so the memoized pill rarely re-renders.
  const dec = status?.decoder;
  const decRunning = !!dec?.running && dec.name !== "none";
  const lockedHz = scanner.state?.state === "locked" ? (scanner.state.active?.freq_hz ?? null) : null;
  const sBand = scanner.state?.band ? (catalog?.bands.find((b) => b.id === scanner.state?.band)?.name ?? scanner.state.band) : "";
  const elapsed = Math.round(sv.survey?.elapsed_s ?? 0);
  const owner = ((): { kind: PanelKind | "idle"; text: string } => {
    if (pass) return { kind: "satellites", text: `Satellite pass — ${pass.name} (el ${Math.round(pass.elevation_deg)}°)` };
    if (surveyRunning) return { kind: "discover", text: `Discover running · ${Math.floor(elapsed / 60)}m ${String(elapsed % 60).padStart(2, "0")}s` };
    if (scanning) {
      const where = sBand || (sw ? `${fmtMHz(sw.lo_hz, 1)}–${fmtMHz(sw.hi_hz, 1)} MHz` : "");
      return { kind: "listen", text: `Listen live${where ? ` — ${where}` : ""}${lockedHz !== null ? ` (▶ ${fmtMHz(lockedHz, 3)})` : ""}` };
    }
    if (decRunning && dec) return { kind: "decoder", text: `Decoder ${dec.name}` };
    if (d && d.mode !== "off") return { kind: "demod", text: `listening ${fmtMHz(d.freq_hz, 3)} ${d.mode.toUpperCase()}` };
    return { kind: "idle", text: "idle" };
  })();

  // Stable callbacks (read the latest closures through a ref) so memoized children do not re-render.
  const act = useRef({ openPanel: (_k: PanelKind) => {}, stopKind: (_k: PanelKind) => {} });
  act.current = {
    openPanel: (k) => {
      if (k === "discover") setSurveyOpen(true);
      else if (k === "listen") setScanOpen(true);
      else if (k === "satellites") setSatSeq((n) => n + 1);
      else if (k === "decoder") setDecFocus((f) => (dec && dec.name !== "none" ? { decoder: dec.name, seq: (f?.seq ?? 0) + 1 } : f));
    },
    stopKind: (k) => {
      if (k === "discover") sv.stop();
      else if (k === "listen") void scanner.stop();
      else if (k === "satellites") void autopilotStop().then(ap.refresh).catch(fail);
      else if (k === "decoder") startDecoder("none");
      else demod.send({ mode: "off" }, true);
    },
  };
  const onExplore = useCallback((k: "discover" | "listen" | "satellites") => act.current.openPanel(k), []);
  const onPillOpen = useCallback((k: PanelKind) => act.current.openPanel(k), []);
  const onPillStop = useCallback((k: PanelKind) => act.current.stopKind(k), []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      e.preventDefault();
      if (locked) return;
      tuner.shiftFreq((e.key === "ArrowRight" ? 1 : -1) * step * (e.shiftKey ? 10 : 1));
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [tuner, step, locked]);

  return (
    <div className="app">
      <header className="bar">
        <span className="brand">sweep</span>
        <Item label="link" value={conn} cls={`conn-${conn}`} />
        <Item label="center" value={center === undefined ? "-" : `${fmtMHz(center, 4)} MHz`} />
        <Item label="rate" value={rate === undefined ? "-" : `${(rate / 1e6).toFixed(3)} MS/s`} />
        <Item label="fps" value={conn === "open" ? fps.toFixed(1) : "-"} />
        <Item
          label="demod"
          value={d && d.mode !== "off" ? `${d.mode.toUpperCase()} ${fmtMHz(d.freq_hz, 4)} MHz` : "off"}
        />
        <span className="item">
          <span className="label">ant</span>{" "}
          <button className="linkbtn" onClick={() => setSettingsOpen(true)} title="Antenna (click to change in settings)">
            {config.antenna ? (antenna ? antenna.name.replace(/\s*\(.*$/, "").replace(/,.*$/, "") : config.antenna) : "not set"}
          </button>
        </span>
        <Item
          label="device"
          value={error ? "status unavailable" : (status?.device ?? "-")}
          cls={error ? "conn-closed" : undefined}
        />
      </header>
      <Controls status={status} centerHz={status?.center_hz ?? center} tuner={tuner} step={step} setStep={setStep} locked={locked} lockNote={busyNote ?? undefined}>
        <ExploreMenu onPick={onExplore} active={surveyOpen || scanOpen || scanning || surveyRunning} />
        <RadioPill kind={owner.kind} text={owner.text} onOpen={onPillOpen} onStop={onPillStop} />
        <DemodControls status={status} demod={demod} audio={audio} locked={locked} transcripts={transcripts} />
        <ToolsMenu status={status} bandViews={views} tuner={tuner} demod={demod} open={toolsOpen} setOpen={setToolsOpen} />
        <SettingsMenu config={config} open={settingsOpen} setOpen={setSettingsOpen} mapCenter={mapCenter} antennas={antennas} />
      </Controls>
      <div className="main">
        <BandPanel
          catalog={catalog}
          loadError={bandsError}
          region={region}
          setRegion={setRegion}
          centerHz={status?.center_hz ?? center}
          onJump={jump}
          onChannel={pickChannel}
          onDecoder={startDecoder}
          error={actErr}
          antenna={antenna}
          onOpenSettings={() => setSettingsOpen(true)}
          onScan={scanBand}
        />
        <div className="center">
          <SpectrumView
            stream={stream}
            meta={meta}
            bandViews={views}
            vfo={vfo}
            scan={scanOverlay}
            onSetVfo={(hz) => (locked ? fail(new Error(SCAN_BUSY)) : demod.send({ freq_hz: hz }))}
            onTuneHz={(hz) => (locked ? fail(new Error(SCAN_BUSY)) : tuner.setFreq(hz))}
            onWheel={(dir) => (locked ? undefined : tuner.shiftFreq(dir * step))}
          />
          {scanOpen && (
            <ScannerPanel
              scanner={scanner}
              audio={audio}
              catalog={catalog}
              region={region}
              events={events}
              transcripts={transcripts}
              onListen={scanListen}
              onClose={() => setScanOpen(false)}
            />
          )}
          <DecoderPanel
            status={status}
            events={events}
            onSet={startDecoder}
            error={null}
            decError={decErr}
            vfoHz={vfo ? vfo.freq_hz : null}
            meterIds={config.meterIds}
            receiver={config.receiver}
            registerCenter={registerCenter}
            onOpenSettings={() => setSettingsOpen(true)}
            surveyLine={surveyLine}
            onOpenSurvey={() => setSurveyOpen(true)}
            focus={decFocus}
            satSeq={satSeq}
            onListen={(hz) => surveyListen(hz, "nfm")}
            audio={audio}
            transcripts={transcripts}
          />
        </div>
      </div>
      <Toasts events={events} />
      <SurveyPanel
        open={surveyOpen}
        sv={sv}
        onClose={() => setSurveyOpen(false)}
        catalog={catalog}
        antennaId={config.antenna}
        region={region}
        onSummary={setSurveyLine}
        onOpenDecoder={openSurveyDecoder}
        onListen={surveyListen}
        audioOn={audio.on}
        onAudioToggle={audio.toggle}
        onTune={tuner.setFreq}
        transcripts={transcripts}
      />
    </div>
  );
}

function Item({ label, value, cls }: { label: string; value: string; cls?: string }) {
  return (
    <span className="item">
      <span className="label">{label}</span> <span className={`value ${cls ?? ""}`}>{value}</span>
    </span>
  );
}
