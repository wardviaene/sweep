import { useEffect, useState } from "react";
import type { DsdCall } from "./useDsd";

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });
const dur = (c: DsdCall) => {
  const s = c.last - c.first;
  return s < 1 ? "<1 s" : `~${Math.round(s)} s`;
};
const v = (x: number | null) => (x === null ? "" : String(x));

export function DsdView({ calls }: { calls: DsdCall[] }) {
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now() / 1000), 500);
    return () => window.clearInterval(id);
  }, []);
  const cur = calls[0];
  const active = cur && now - cur.last <= 2 ? cur : null;
  return (
    <div className="dsd">
      <div className="dsdbar">
        {active ? (
          <span className="conn-open">
            now active: {active.mode} tg {v(active.tgt) || "-"} src {v(active.src) || "-"}
            {active.encrypted && <span className="badge"> encrypted</span>}
          </span>
        ) : (
          <span className="dim">idle</span>
        )}
        <span className="dim">voice plays through the audio button (8 kHz). Encrypted traffic is never decrypted.</span>
      </div>
      {!calls.length ? (
        <div className="dim pad">no calls heard yet</div>
      ) : (
        <div className="tablewrap">
          <table>
            <thead>
              <tr>
                <th>time</th><th>mode</th><th>talkgroup</th><th>source ID</th><th>CC</th><th></th><th style={{ textAlign: "right" }}>duration</th>
              </tr>
            </thead>
            <tbody>
              {calls.map((c) => (
                <tr key={c.id}>
                  <td>{time(c.first)}</td>
                  <td>{c.mode}</td>
                  <td>{v(c.tgt)}</td>
                  <td>{v(c.src)}</td>
                  <td>{v(c.cc)}</td>
                  <td>{c.encrypted && <span className="badge">encrypted</span>}</td>
                  <td style={{ textAlign: "right" }}>{dur(c)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
