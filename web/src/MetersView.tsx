import { useEffect, useState } from "react";
import type { Meter, MetersView as View } from "./useMeters";
import { Spark } from "./SensorsView";

const SKIP = new Set(["time", "model", "id", "consumption_data", "consumption", "mic", "rssi", "snr", "noise"]);
const fmt = (x: number) => (Number.isInteger(x) ? String(x) : String(+x.toFixed(3)));

function ago(now: number, ts: number): string {
  const s = Math.max(0, Math.round(now - ts));
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  return `${Math.floor(s / 3600)} h ago`;
}

function Card({ m, now }: { m: Meter; now: number }) {
  const delta = m.first !== null && m.last !== null ? m.last - m.first : null;
  const rest = Object.entries(m.data).filter(([k]) => !SKIP.has(k));
  return (
    <div className="card" style={{ width: 260 }}>
      <div className="card-head">
        <b>{m.model}</b>
        <span className="dim">id {m.id}</span>
      </div>
      <div className="bigrow">
        <div>
          {m.last !== null ? <span className="big">{fmt(m.last)}</span> : <span className="dim">no reading</span>}
          <span className="dim"> raw units</span>
          <Spark v={m.hist} color="#4fd1c5" />
        </div>
        {delta !== null && (
          <div>
            <span className="big">{delta >= 0 ? "+" : ""}{fmt(delta)}</span>
            <span className="dim"> since first reading ({fmt(m.first!)})</span>
          </div>
        )}
      </div>
      {rest.length > 0 && (
        <div className="kv small">
          {rest.map(([k, x]) => `${k}=${typeof x === "number" ? +x.toFixed(2) : typeof x === "object" ? JSON.stringify(x) : String(x)}`).join(" ")}
        </div>
      )}
      <div className="dim small">{m.count} readings · {ago(now, m.ts)}</div>
    </div>
  );
}

export function MetersView(props: { view: View; meterIds: string[]; onOpenSettings: () => void }) {
  const { view, meterIds } = props;
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now() / 1000), 1000);
    return () => window.clearInterval(id);
  }, []);
  return (
    <div className="cards" style={{ flexDirection: "column", flexWrap: "nowrap" }}>
      {meterIds.length === 0 && (
        <div className="pad">
          No meter ID configured, so no meter is shown. <button className="linkbtn" onClick={props.onOpenSettings}>add your meter ID in settings</button>{" "}
          (the ID is printed on your meter).
        </div>
      )}
      {meterIds.length > 0 && view.meters.length === 0 && (
        <div className="dim pad">waiting for your meter ({meterIds.length} ID{meterIds.length > 1 ? "s" : ""} configured); meters usually report every 30 s to a few minutes</div>
      )}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
        {view.meters.map((m) => <Card key={m.key} m={m} now={now} />)}
      </div>
      <div className="dim pad">
        {view.otherHeard === null ? "other meters: waiting for the first count" : `${view.otherHeard} other meters heard (IDs hidden for privacy)`}
      </div>
    </div>
  );
}
