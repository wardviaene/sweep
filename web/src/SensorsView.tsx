import { useEffect, useState } from "react";
import type { Sensor } from "./useSensors";

const SKIP = new Set([
  "time", "model", "id", "channel", "temperature_C", "temperature_F", "humidity", "battery_ok", "rssi", "snr", "noise",
  "pressure_hPa", "pressure_kPa", "pressure", "wind_avg_km_h", "wind_avg_m_s", "wind_max_km_h", "wind_max_m_s", "wind_dir_deg",
  "rain_mm", "rain_in", "mic",
]);

function ago(now: number, ts: number): string {
  const s = Math.max(0, Math.round(now - ts));
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  return `${Math.floor(s / 3600)} h ago`;
}

export function Spark({ v, color }: { v: number[]; color: string }) {
  if (v.length < 2) return <svg className="spark" width="100" height="22" />;
  const lo = Math.min(...v);
  const hi = Math.max(...v);
  const span = hi - lo || 1;
  const pts = v.map((y, i) => `${((i / (v.length - 1)) * 98 + 1).toFixed(1)},${(20 - ((y - lo) / span) * 18).toFixed(1)}`).join(" ");
  return (
    <svg className="spark" width="100" height="22" role="img" aria-label="history">
      <polyline points={pts} fill="none" stroke={color} strokeWidth="1.2" strokeLinejoin="round" />
    </svg>
  );
}

const nv = (d: Record<string, unknown>, k: string): number | null => (typeof d[k] === "number" ? (d[k] as number) : null);

function Card({ s, now }: { s: Sensor; now: number }) {
  const d = s.data;
  const tc = nv(d, "temperature_C");
  const tf = nv(d, "temperature_F") ?? (tc === null ? null : tc * 1.8 + 32);
  const hum = nv(d, "humidity");
  const pr = nv(d, "pressure_hPa") ?? nv(d, "pressure_kPa") ?? nv(d, "pressure");
  const prUnit = nv(d, "pressure_hPa") !== null ? "hPa" : nv(d, "pressure_kPa") !== null ? "kPa" : "";
  const wind = nv(d, "wind_avg_km_h") ?? nv(d, "wind_avg_m_s");
  const windUnit = nv(d, "wind_avg_km_h") !== null ? "km/h" : "m/s";
  const gust = nv(d, "wind_max_km_h") ?? nv(d, "wind_max_m_s");
  const dirDeg = nv(d, "wind_dir_deg");
  const rain = nv(d, "rain_mm");
  const batt = d.battery_ok;
  const rssi = nv(d, "rssi");
  const snr = nv(d, "snr");
  const rest = Object.entries(d).filter(([k]) => !SKIP.has(k));
  return (
    <div className="card">
      <div className="card-head">
        <b>{s.model}</b>
        <span className="dim">{[s.id && `id ${s.id}`, s.channel && `ch ${s.channel}`].filter(Boolean).join(" · ")}</span>
        {batt !== undefined && (
          <span className={`badge ${batt === 1 || batt === true ? "okb" : ""}`}>{batt === 1 || batt === true ? "batt ok" : "batt low"}</span>
        )}
      </div>
      <div className="bigrow">
        {tc !== null && (
          <div>
            <span className="big">{tc.toFixed(1)}</span>°C <span className="dim">{tf !== null && `${tf.toFixed(1)}°F`}</span>
            <Spark v={s.temps} color="#f6ad55" />
          </div>
        )}
        {hum !== null && (
          <div>
            <span className="big">{Math.round(hum)}</span>% <span className="dim">RH</span>
            <Spark v={s.hums} color="#63b3ed" />
          </div>
        )}
        {pr !== null && <div><span className="big">{pr.toFixed(1)}</span> {prUnit}</div>}
        {wind !== null && (
          <div>
            <span className="big">{wind.toFixed(1)}</span> {windUnit}
            <span className="dim">{gust !== null && ` gust ${gust.toFixed(1)}`}{dirDeg !== null && ` @${Math.round(dirDeg)}°`}</span>
          </div>
        )}
        {rain !== null && <div><span className="big">{rain.toFixed(1)}</span> mm rain</div>}
      </div>
      {rest.length > 0 && (
        <div className="kv small">
          {rest.map(([k, v]) => `${k}=${typeof v === "number" ? +v.toFixed(2) : typeof v === "object" ? JSON.stringify(v) : String(v)}`).join(" ")}
        </div>
      )}
      <div className="dim small">
        {rssi !== null && `rssi ${rssi.toFixed(1)} `}
        {snr !== null && `snr ${snr.toFixed(1)} `}· {s.count} msgs · {ago(now, s.ts)}
      </div>
    </div>
  );
}

export function SensorsView({ sensors }: { sensors: Sensor[] }) {
  // One interval for all cards ("ago" labels). Event timestamps are server time (seconds).
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now() / 1000), 1000);
    return () => window.clearInterval(id);
  }, []);
  if (!sensors.length) return <div className="dim pad">no rtl_433 sensors yet</div>;
  return (
    <div className="cards">
      {sensors.map((s) => (
        <Card key={s.key} s={s} now={now} />
      ))}
    </div>
  );
}
