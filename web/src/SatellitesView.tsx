import { useEffect, useState } from "react";
import type { Receiver, SatDownlink } from "./api";
import { useOverhead, usePasses } from "./useSatellites";
import { DataSources } from "./meta";
import type { SatOverhead } from "./api";
import { useAutopilot } from "./useAutopilot";
import { AutopilotPanel } from "./AutopilotPanel";
import type { AudioApi } from "./useAudio";
import type { SstvImage } from "./useSstv";

const mhz = (hz: number) => (hz / 1e6).toFixed(4);

function dlText(d: SatDownlink): string {
  return [`${mhz(d.freq_hz)} MHz`, d.mode, d.baud ? `${d.baud} baud` : ""].filter(Boolean).join(" ");
}

function countdown(s: number): string {
  const t = Math.max(0, Math.round(s));
  const h = Math.floor(t / 3600);
  const m = Math.floor((t % 3600) / 60);
  const sec = t % 60;
  return h > 0 ? `${h}h ${String(m).padStart(2, "0")}m` : `${m}m ${String(sec).padStart(2, "0")}s`;
}

const clock = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false, hour: "2-digit", minute: "2-digit" });

export function SatellitesView(props: {
  /** true while the tab is visible: polling only happens then */
  active: boolean;
  receiver: Receiver | null;
  onOpenSettings: () => void;
  onListen: (hz: number) => void;
  onDecode: (hz: number, baud: number) => void;
  audio: AudioApi;
  sstvImages: SstvImage[];
}) {
  const { active, receiver } = props;
  const over = useOverhead(active);
  const ap = useAutopilot(active);
  const passes = usePasses(active, receiver ? `${receiver.lat},${receiver.lon}` : "");
  // 1 s tick for the countdowns only; this component is small and only mounted while the tab is open.
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    if (!active) return;
    const id = window.setInterval(() => setNow(Date.now() / 1000), 1000);
    setNow(Date.now() / 1000);
    return () => window.clearInterval(id);
  }, [active]);

  const actions = (d: SatDownlink) => (
    <span className="dlrow">
      <span>{dlText(d)}</span>
      <button className="mini" onClick={() => props.onListen(d.freq_hz)} title="Tune here and demodulate NFM (nominal frequency, no Doppler correction)">Listen</button>
      {d.decoder === "packet" && (
        <button className="mini" onClick={() => props.onDecode(d.freq_hz, d.baud === 9600 ? 9600 : 1200)} title="Start the AX.25 packet decoder here">Decode</button>
      )}
    </span>
  );

  const noRx = passes.noReceiver;
  const apNext = ap.state?.enabled ? ap.state.next.filter((n) => n.will_run) : [];
  const upcoming = passes.passes.filter((p) => p.los > now).sort((a, b) => a.aos - b.aos);

  return (
    <div className="sat">
      <AutopilotPanel ap={ap} now={now} passes={passes.passes} images={props.sstvImages} audio={props.audio} onOpenSettings={props.onOpenSettings} />
      <div className="sat-top">
        <div className="sat-over">
          <div className="sat-sec">
            <h4>Overhead now ({over.sats.length})</h4>
            {over.error && <div className="err" style={{ margin: 0 }}>{over.error}</div>}
            {!over.error && over.sats.length === 0 && (
              <div className="dim">
                {noRx ? "needs the receiver location" : "no satellites with a known downlink above the horizon (data comes from make metadata)"}
              </div>
            )}
            {over.sats.length > 0 && (
              <table>
                <thead>
                  <tr><th>name</th><th>el °</th><th>az °</th><th>range km</th><th>doppler</th><th>downlinks</th></tr>
                </thead>
                <tbody>
                  {[...over.sats].sort((a, b) => b.elevation_deg - a.elevation_deg).map((s) => (
                    <tr key={s.norad || s.name}>
                      <td>{s.name}</td>
                      <td>{s.elevation_deg.toFixed(0)}</td>
                      <td>{s.azimuth_deg.toFixed(0)}</td>
                      <td>{s.range_km === undefined ? "" : Math.round(s.range_km)}</td>
                      <td>{s.doppler_hz === undefined ? "" : `${s.doppler_hz > 0 ? "+" : ""}${(s.doppler_hz / 1e3).toFixed(1)} kHz`}</td>
                      <td>{s.downlinks.map((d, i) => <div key={i}>{actions(d)}</div>)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
        <SkyPlot sats={over.sats} />
      </div>
      <div className="sat-sec">
        <h4>Upcoming passes (24 h, min elevation 10°)</h4>
        {noRx && (
          <div>
            Passes are computed locally and need your receiver location.{" "}
            <button className="linkbtn" onClick={props.onOpenSettings}>Set the receiver location in settings</button>
          </div>
        )}
        {!noRx && passes.error && <div className="err" style={{ margin: 0 }}>{passes.error}</div>}
        {!noRx && !passes.error && passes.loaded && upcoming.length === 0 && (
          <div className="dim">no passes found (satellite data missing? run make metadata)</div>
        )}
        {upcoming.length > 0 && (
          <table>
            <thead>
              <tr><th>name</th><th>AOS in</th><th>AOS</th><th>max el °</th><th>duration</th><th>downlinks</th></tr>
            </thead>
            <tbody>
              {upcoming.map((p) => {
                const here = p.aos <= now && p.los > now;
                return (
                  <tr key={`${p.norad}-${p.aos}`} className={here ? "now" : ""}>
                    <td>{p.name}{here && <> <span className="badge okb">now</span></>}
                      {apNext.some((n) => n.norad === p.norad && Math.abs(n.aos - p.aos) <= 60) && <> <span className="badge okb" title="The autopilot will handle this pass">auto</span></>}</td>
                    <td>{here ? `LOS ${countdown(p.los - now)}` : countdown(p.aos - now)}</td>
                    <td>{clock(p.aos)}</td>
                    <td>{p.max_elevation_deg.toFixed(0)}</td>
                    <td>{countdown(p.los - p.aos)}</td>
                    <td>{p.downlinks.map((d, i) => <div key={i}>{actions(d)}</div>)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
      <DataSources kind="satellites" />
    </div>
  );
}

/** Polar plot: azimuth around (north up, clockwise), elevation radial (zenith in the center). */
function SkyPlot({ sats }: { sats: SatOverhead[] }) {
  const S = 220;
  const c = S / 2;
  const R = 92;
  const pos = (az: number, el: number): [number, number] => {
    const r = (R * (90 - Math.max(0, Math.min(90, el)))) / 90;
    const a = (az * Math.PI) / 180;
    return [c + r * Math.sin(a), c - r * Math.cos(a)];
  };
  return (
    <svg className="skyplot" width={S} height={S} viewBox={`0 0 ${S} ${S}`} role="img" aria-label="Sky plot of satellites above the horizon">
      {[0, 30, 60].map((el) => (
        <circle key={el} cx={c} cy={c} r={(R * (90 - el)) / 90} fill="none" stroke="#1f2a38" />
      ))}
      <line x1={c - R} y1={c} x2={c + R} y2={c} stroke="#1f2a38" />
      <line x1={c} y1={c - R} x2={c} y2={c + R} stroke="#1f2a38" />
      {(
        [["N", c, c - R - 3], ["E", c + R + 8, c + 3], ["S", c, c + R + 11], ["W", c - R - 8, c + 3]] as [string, number, number][]
      ).map(([t, x, y]) => (
        <text key={t} x={x} y={y} textAnchor="middle" fontSize="9" fill="#6f8399">{t}</text>
      ))}
      <text x={c + 3} y={c - (R * 2) / 3 + 9} fontSize="8" fill="#6f8399">30°</text>
      <text x={c + 3} y={c - R / 3 + 9} fontSize="8" fill="#6f8399">60°</text>
      {sats.map((s) => {
        const [x, y] = pos(s.azimuth_deg, s.elevation_deg);
        return (
          <g key={s.norad || s.name}>
            <circle cx={x} cy={y} r={3.5} fill="#4fd1c5" stroke="#05080c" />
            <text x={x + 6} y={y + 3} fontSize="9" fill="#d5dfeb" style={{ paintOrder: "stroke" }} stroke="#05080c" strokeWidth="2">{s.name}</text>
          </g>
        );
      })}
    </svg>
  );
}
