import { useEffect, useState } from "react";
import { SpectrumView } from "./SpectrumView";
import { useSpectrum } from "./useSpectrum";
import { useStatus } from "./useStatus";
import { useTuner } from "./useTuner";
import { Controls } from "./Controls";
import { fmtMHz } from "./draw";

export function App() {
  const { stream, conn, meta, fps } = useSpectrum();
  const { status, error, applyStatus } = useStatus(2000);
  const tuner = useTuner(status, applyStatus);
  const [step, setStep] = useState(100e3);

  const center = meta?.center_hz ?? status?.center_hz;
  const rate = meta?.sample_rate ?? status?.sample_rate;

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
          label="device"
          value={error ? "status unavailable" : (status?.device ?? "-")}
          cls={error ? "conn-closed" : undefined}
        />
      </header>
      <Controls status={status} centerHz={status?.center_hz ?? center} tuner={tuner} step={step} setStep={setStep} />
      <SpectrumView
        stream={stream}
        meta={meta}
        onTuneHz={tuner.setFreq}
        onWheel={(dir) => tuner.shiftFreq(dir * step)}
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
