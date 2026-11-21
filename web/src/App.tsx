import { SpectrumView } from "./SpectrumView";
import { useSpectrum } from "./useSpectrum";
import { useStatus } from "./useStatus";
import { fmtMHz } from "./draw";

export function App() {
  const { stream, conn, meta, fps } = useSpectrum();
  const { status, error } = useStatus(2000);

  const center = meta?.center_hz ?? status?.center_hz;
  const rate = meta?.sample_rate ?? status?.sample_rate;
  const gain = status ? (status.gain_db === null ? "auto" : `${status.gain_db.toFixed(1)} dB`) : "-";

  return (
    <div className="app">
      <header className="bar">
        <span className="brand">sweep</span>
        <Item label="link" value={conn} cls={`conn-${conn}`} />
        <Item label="center" value={center === undefined ? "-" : `${fmtMHz(center, 4)} MHz`} />
        <Item label="rate" value={rate === undefined ? "-" : `${(rate / 1e6).toFixed(3)} MS/s`} />
        <Item label="gain" value={gain} />
        <Item label="fps" value={conn === "open" ? fps.toFixed(1) : "-"} />
        <Item
          label="device"
          value={error ? "status unavailable" : (status?.device ?? "-")}
          cls={error ? "conn-closed" : undefined}
        />
      </header>
      <SpectrumView stream={stream} meta={meta} />
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
