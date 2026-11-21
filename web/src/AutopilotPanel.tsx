import { useState } from "react";
import { autopilotRun, autopilotStop, type ApHistory, type SatPass } from "./api";
import type { AutopilotApi } from "./useAutopilot";
import type { AudioApi } from "./useAudio";
import type { SstvImage } from "./useSstv";

const clock = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false, hour: "2-digit", minute: "2-digit" });
const day = (ts: number) => new Date(ts * 1000).toLocaleString([], { hour12: false, month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit" });
const mhz = (hz: number) => (hz / 1e6).toFixed(4);

function cd(s: number): string {
  const t = Math.max(0, Math.round(s));
  const h = Math.floor(t / 3600);
  const m = Math.floor((t % 3600) / 60);
  return h > 0 ? `${h}h ${String(m).padStart(2, "0")}m` : `${m}m ${String(t % 60).padStart(2, "0")}s`;
}

const dec = (d: string, baud?: number) => (d === "packet" && baud ? `packet ${baud}` : d);

interface Props {
  ap: AutopilotApi;
  now: number;
  passes: SatPass[];
  images: SstvImage[];
  audio: AudioApi;
  onOpenSettings: () => void;
}

export function AutopilotPanel({ ap, now, passes, images, audio, onOpenSettings }: Props) {
  const s = ap.state;
  const [custom, setCustom] = useState<boolean | null>(null); // null = follow the server
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const act = (key: string, f: () => Promise<void>) => {
    setBusy(key);
    setErr(null);
    f()
      .then(() => ap.refresh())
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(null));
  };

  if (!s) {
    return (
      <div className="sat-sec ap">
        <h4>Autopilot</h4>
        {ap.error ? <div className="err" style={{ margin: 0 }}>{ap.error}</div> : <div className="dim">loading...</div>}
      </div>
    );
  }

  const chosen = custom ?? s.satellites.length > 0;
  // choices: satellites seen in the pass list, plus any already selected (so they can be unticked)
  const names = new Map<number, string>();
  for (const p of passes) if (p.norad) names.set(p.norad, p.name);
  for (const n of s.next) if (n.norad) names.set(n.norad, n.name);
  for (const n of s.satellites) if (!names.has(n)) names.set(n, String(n));
  const choices = [...names.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  const toggleSat = (n: number) => ap.patch({ satellites: s.satellites.includes(n) ? s.satellites.filter((x) => x !== n) : [...s.satellites, n] });

  const cur = s.current;
  const pct = cur && cur.los > cur.aos ? Math.max(0, Math.min(100, (100 * (now - cur.aos)) / (cur.los - cur.aos))) : 0;
  const missing: string[] = [];
  if (!s.receiver_set) missing.push("receiver location (settings)");
  if (!s.datasets_ok) missing.push("satellite data (run make metadata)");

  return (
    <div className="sat-sec ap">
      <h4>Autopilot</h4>
      <div className="ap-ctl">
        <label className="chk"><input type="checkbox" checked={s.enabled} onChange={(e) => ap.patch({ enabled: e.target.checked })} /> <b style={{ color: s.enabled ? "var(--ok)" : "var(--text)" }}>enabled</b></label>
        <label className="dim">
          min elevation {s.min_elevation}°{" "}
          <input type="range" min={0} max={80} step={1} value={s.min_elevation} onChange={(e) => ap.patch({ min_elevation: Number(e.target.value) })} />
        </label>
        <label className="chk" title="Stop whatever is running (survey, scanner, decoder) when a pass starts, and restore it afterwards"><input type="checkbox" checked={s.preempt} onChange={(e) => ap.patch({ preempt: e.target.checked })} /> preempt other activity</label>
        <label className="chk" title="The VFO follows the downlink with Doppler correction; turn audio on to hear it"><input type="checkbox" checked={s.listen} onChange={(e) => ap.patch({ listen: e.target.checked })} /> play pass audio</label>
        <label className="chk" title="Records raw IQ of each pass"><input type="checkbox" checked={s.record_iq} onChange={(e) => ap.patch({ record_iq: e.target.checked })} /> record IQ</label>
        {s.record_iq && <span className="badge">IQ files are large (hundreds of MB per pass)</span>}
      </div>
      <div className="ap-ctl">
        <span className="dim">satellites</span>
        <span className="seg">
          <button className={!chosen ? "on" : ""} onClick={() => { setCustom(false); if (s.satellites.length) ap.patch({ satellites: [] }); }}>all decodable</button>
          <button className={chosen ? "on" : ""} onClick={() => setCustom(true)}>chosen</button>
        </span>
        {chosen && (
          <span className="ap-sats">
            {choices.length === 0 && <span className="dim">no satellites known yet</span>}
            {choices.map(([n, name]) => (
              <label key={n} className="chk"><input type="checkbox" checked={s.satellites.includes(n)} onChange={() => toggleSat(n)} /> {name}</label>
            ))}
            {choices.length > 0 && s.satellites.length === 0 && <span className="dim">(none ticked = all)</span>}
          </span>
        )}
      </div>
      {missing.length > 0 && (
        <div className="decerr">
          The autopilot needs: {missing.join(", ")}.{" "}
          {!s.receiver_set && <button className="linkbtn" onClick={onOpenSettings}>Open settings</button>}
        </div>
      )}
      {(ap.error || err) && <div className="err" style={{ margin: 0 }}>{err ?? ap.error}</div>}

      {cur && (
        <div className="ap-cur">
          <div className="ap-name">{cur.name} <span className="badge okb">pass in progress</span></div>
          <div className="ap-grid">
            <span>el <b>{cur.elevation_deg.toFixed(1)}°</b></span>
            <span>az <b>{cur.azimuth_deg.toFixed(0)}°</b></span>
            <span>doppler <b>{cur.doppler_hz > 0 ? "+" : ""}{(cur.doppler_hz / 1e3).toFixed(2)} kHz</b></span>
            <span>{mhz(cur.downlink_hz)} MHz · {dec(cur.decoder, cur.baud)}</span>
            <span>packets <b>{cur.packets}</b></span>
            <span>images <b>{cur.images}</b></span>
            <span className="dim">max el {cur.max_elevation_deg.toFixed(0)}°</span>
          </div>
          <div className="ap-prog">
            <span className="dim">{clock(cur.aos)}</span>
            <div className="apbar"><div style={{ width: `${pct}%` }} /></div>
            <span className="dim">{clock(cur.los)} (LOS in {cd(cur.los - now)})</span>
            <button disabled={busy === "stop"} onClick={() => act("stop", autopilotStop)}>Stop</button>
          </div>
          {s.listen && !audio.on && (
            <div className="scn-audio"><button className="primary" onClick={audio.toggle}>Turn audio on to hear the pass</button></div>
          )}
        </div>
      )}

      <div className="ap-cols">
        <div>
          <h4>Next passes (12 h)</h4>
          {s.next.length === 0 ? (
            <div className="dim">{s.enabled ? "no qualifying passes" : "autopilot is off"}</div>
          ) : (
            <table>
              <thead><tr><th>name</th><th>AOS in</th><th>max el</th><th>downlink</th><th>run</th><th></th></tr></thead>
              <tbody>
                {s.next.map((p) => {
                  const here = p.aos <= now && p.los > now;
                  return (
                    <tr key={`${p.norad}-${p.aos}`} className={here ? "now" : ""}>
                      <td>{p.name}</td>
                      <td>{here ? `LOS ${cd(p.los - now)}` : `${cd(p.aos - now)} (${clock(p.aos)})`}</td>
                      <td>{p.max_elevation_deg.toFixed(0)}°</td>
                      <td>{mhz(p.downlink_hz)} · {dec(p.decoder, p.baud)}</td>
                      <td>
                        {p.will_run ? <span className="badge okb">will run</span> : <span className="badge" title={p.reason}>skip</span>}
                        {!p.will_run && p.reason && <span className="dim"> {p.reason}</span>}
                      </td>
                      <td>{here && !cur && <button className="mini" disabled={busy === "run"} onClick={() => act("run", () => autopilotRun(p.norad))} title="Handle this pass now, whatever its elevation">Run now</button>}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
        <div>
          <h4>History</h4>
          {s.history.length === 0 ? (
            <div className="dim">no passes handled yet</div>
          ) : (
            <table>
              <thead><tr><th>name</th><th>time</th><th>max el</th><th>decoder</th><th>pkts</th><th>images</th><th>audio</th><th>state</th></tr></thead>
              <tbody>
                {s.history.map((h) => <HistRow key={`${h.dir}-${h.aos}`} h={h} images={images} />)}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}

function HistRow({ h, images }: { h: ApHistory; images: SstvImage[] }) {
  // the contract has no image file names in history: match the gallery's images received during the pass
  const thumbs = h.images > 0 ? images.filter((i) => i.ts >= h.aos - 30 && i.ts <= h.los + 60).slice(0, 4) : [];
  return (
    <tr>
      <td>{h.name}</td>
      <td>{day(h.aos)}</td>
      <td>{h.max_elevation_deg.toFixed(0)}°</td>
      <td>{h.decoder}</td>
      <td>{h.packets}</td>
      <td>
        {h.images}
        {thumbs.map((t) => <a key={t.id} href={t.url} target="_blank" rel="noreferrer"><img className="ap-thumb" src={t.url} loading="lazy" alt="" /></a>)}
      </td>
      <td>{h.audio && h.dir ? <a href={`/api/files/passes/${encodeURIComponent(h.dir)}/${encodeURIComponent(h.audio)}`} target="_blank" rel="noreferrer">audio</a> : ""}</td>
      <td>
        <span className={`badge ${h.state === "done" ? "okb" : ""}`}>{h.state}</span>
        {h.reason && <span className="dim"> {h.reason}</span>}
      </td>
    </tr>
  );
}
