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
import { useConfig } from "./useConfig";
import { ToolsMenu } from "./ToolsMenu";
import { SettingsMenu } from "./SettingsMenu";
import type { LatLon } from "./geo";
import { fmtMHz } from "./draw";
import { setDecoder, type DecoderName, type DecoderOpts } from "./api";
import { bandViews, loadRegion, saveRegion, type Band, type Channel } from "./bands";

export function App() {
  const { stream, conn, meta, fps } = useSpectrum();
  const { status, error, applyStatus } = useStatus(2000);
  const tuner = useTuner(status, applyStatus);
  const demod = useDemod(applyStatus);
  const audio = useAudio();
  const { catalog, error: bandsError } = useBands();
  const events = useEvents();
  const config = useConfig();
  const [toolsOpen, setToolsOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [mapCenter, setMapCenter] = useState<(() => LatLon) | null>(null);
  const registerCenter = useCallback((g: (() => LatLon) | null) => setMapCenter(() => g), []);
  const [step, setStep] = useState(100e3);
  const [regionPref, setRegionPref] = useState<string | null>(loadRegion);
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
    const hz = Math.round(b.tune_mhz * 1e6);
    if (b.mode === "none") return tuner.setFreq(hz);
    const r = status?.sample_rate ?? 2.048e6;
    void tuner.tuneNow(hz - r / 4).then((ok) => {
      if (ok) demod.send({ mode: b.mode as "nfm", freq_hz: hz }, true);
    });
  };
  const pickChannel = (_b: Band, c: Channel) => {
    const hz = Math.round(c.mhz * 1e6);
    if (c.mode !== "none") demod.send({ mode: c.mode, freq_hz: hz }, true);
    else tuner.setFreq(hz);
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      e.preventDefault();
      tuner.shiftFreq((e.key === "ArrowRight" ? 1 : -1) * step * (e.shiftKey ? 10 : 1));
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [tuner, step]);

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
        <Item
          label="device"
          value={error ? "status unavailable" : (status?.device ?? "-")}
          cls={error ? "conn-closed" : undefined}
        />
      </header>
      <Controls status={status} centerHz={status?.center_hz ?? center} tuner={tuner} step={step} setStep={setStep}>
        <DemodControls status={status} demod={demod} audio={audio} />
        <ToolsMenu status={status} bandViews={views} tuner={tuner} demod={demod} open={toolsOpen} setOpen={setToolsOpen} />
        <SettingsMenu config={config} open={settingsOpen} setOpen={setSettingsOpen} mapCenter={mapCenter} />
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
        />
        <div className="center">
          <SpectrumView
            stream={stream}
            meta={meta}
            bandViews={views}
            vfo={vfo}
            onSetVfo={(hz) => demod.send({ freq_hz: hz })}
            onTuneHz={tuner.setFreq}
            onWheel={(dir) => tuner.shiftFreq(dir * step)}
          />
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
          />
        </div>
      </div>
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
