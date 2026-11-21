import { useEffect, useMemo, useRef, useState } from "react";
import type { Receiver } from "./api";
import { bearingDeg, rangeNmi } from "./geo";
import type { Sonde, SondePt } from "./useSondes";
import { esc, L, MapBox, useFit, type RegisterCenter } from "./mapkit";

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });
const FT = 3.28084;
const BUCKET_M = 2500;

/** low = warm, high = cool (0..35 km). */
export function sondeColor(altM: number): string {
  const t = Math.min(1, Math.max(0, altM / 35000));
  return `hsl(${Math.round(25 + t * 195)}, 90%, 60%)`;
}

function phase(s: Sonde): string {
  const v = s.vel_v;
  if (v === null) return "";
  if (v < -2) return s.sawUp ? "descending (burst)" : "descending";
  if (v > 0.5) return "ascending";
  return "level";
}

interface Entry {
  dot: L.CircleMarker;
  lines: L.LayerGroup;
  n: number;
  sel: boolean;
}

export function SondeView(props: { sondes: Sonde[]; receiver: Receiver | null; registerCenter: RegisterCenter }) {
  const { sondes, receiver } = props;
  const [map, setMap] = useState<L.Map | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const store = useRef<{ g: L.LayerGroup; rx: L.LayerGroup; m: Map<string, Entry> } | null>(null);
  const sel = sondes.find((s) => s.id === selected) ?? sondes[0] ?? null;
  const selId = sel?.id ?? null;

  useEffect(() => {
    if (!map) {
      store.current = null;
      return;
    }
    store.current = { g: L.layerGroup().addTo(map), rx: L.layerGroup().addTo(map), m: new Map() };
    return () => {
      store.current = null;
    };
  }, [map]);

  useEffect(() => {
    const s = store.current;
    if (!map || !s) return;
    s.rx.clearLayers();
    if (receiver) L.circleMarker([receiver.lat, receiver.lon], { radius: 5, color: "#4fd1c5", weight: 2, fillOpacity: 0.6 }).bindTooltip("receiver").addTo(s.rx);
  }, [map, receiver]);

  useEffect(() => {
    const s = store.current;
    if (!s) return;
    const live = new Set<string>();
    for (const x of sondes) {
      const pos = x.lat !== null && x.lon !== null ? ([x.lat, x.lon] as L.LatLngTuple) : null;
      if (!pos) continue;
      live.add(x.id);
      const on = x.id === selId;
      let e = s.m.get(x.id);
      if (!e) {
        const dot = L.circleMarker(pos, { radius: 6, color: "#05080c", weight: 1, fillOpacity: 0.95 });
        dot.bindTooltip(esc(x.id), { permanent: true, direction: "right", offset: [7, 0], className: "stlabel" });
        const id = x.id;
        dot.on("click", () => setSelected(id));
        const lines = L.layerGroup();
        s.g.addLayer(lines).addLayer(dot);
        e = { dot, lines, n: -1, sel: false };
        s.m.set(x.id, e);
      }
      e.dot.setLatLng(pos);
      e.dot.setStyle({ fillColor: sondeColor(x.alt ?? 0), radius: on ? 8 : 6, color: on ? "#fff" : "#05080c" });
      if (e.n !== x.hist.length || e.sel !== on) {
        e.n = x.hist.length;
        e.sel = on;
        e.lines.clearLayers();
        // one polyline per run of equal altitude bucket
        let run: L.LatLngTuple[] = [];
        let b = -1;
        const flush = () => {
          if (run.length > 1) L.polyline(run, { weight: on ? 3 : 1.8, opacity: 0.85, color: sondeColor(b * BUCKET_M + BUCKET_M / 2), interactive: false }).addTo(e!.lines);
        };
        for (const p of x.hist) {
          if (p.lat === null || p.lon === null) continue;
          const nb = Math.floor(p.alt / BUCKET_M);
          const pt: L.LatLngTuple = [p.lat, p.lon];
          if (nb !== b) {
            const last = run[run.length - 1];
            flush();
            run = last ? [last] : [];
            b = nb;
          }
          run.push(pt);
        }
        flush();
      }
    }
    for (const [k, e] of s.m)
      if (!live.has(k)) {
        s.g.removeLayer(e.lines).removeLayer(e.dot);
        s.m.delete(k);
      }
  }, [map, sondes, selId]);

  const pts = useMemo(() => {
    const p = sondes.filter((x) => x.lat !== null && x.lon !== null).map((x): [number, number] => [x.lat!, x.lon!]);
    if (p.length && receiver) p.push([receiver.lat, receiver.lon]);
    return p;
  }, [sondes, receiver]);
  const fit = useFit(map, pts);

  return (
    <div className="split">
      <MapBox onMap={setMap} registerCenter={props.registerCenter}>
        <div className="maptools"><button className="mini" onClick={fit}>fit</button></div>
      </MapBox>
      <div className="side">
        <div className="dim pad">{sondes.length} radiosonde{sondes.length === 1 ? "" : "s"} heard</div>
        {sondes.length === 0 ? (
          <div className="dim pad">no radiosonde frames yet. Sondes transmit for ~2 h after launch (about 00/12 UTC); the carrier must be found first.</div>
        ) : (
          <div className="sondebody">
            <div className="sondelist">
              {sondes.map((x) => (
                <button key={x.id} className={x.id === selId ? "on" : ""} onClick={() => {
                  setSelected(x.id);
                  if (map && x.lat !== null && x.lon !== null) map.panTo([x.lat, x.lon]);
                }}>
                  {x.id} <span className="dim">{x.type}</span>
                </button>
              ))}
            </div>
            {sel && <Detail s={sel} receiver={receiver} />}
          </div>
        )}
      </div>
    </div>
  );
}

function Detail({ s, receiver }: { s: Sonde; receiver: Receiver | null }) {
  const p = s.lat !== null && s.lon !== null ? { lat: s.lat, lon: s.lon } : null;
  const nmi = receiver && p ? rangeNmi(receiver, p) : null;
  const brg = receiver && p ? bearingDeg(receiver, p) : null;
  const n = (x: number | null, d: number, u = "") => (x === null ? "-" : `${x.toFixed(d)}${u}`);
  const rows: [string, string][] = [
    ["type", s.type || "-"],
    ["id", s.id],
    ["freq", s.freq_hz === null ? "-" : `${(s.freq_hz / 1e6).toFixed(3)} MHz`],
    ["last frame", `${time(s.ts)}${s.frame !== null ? ` (#${s.frame})` : ""}`],
    ["altitude", s.alt === null ? "-" : `${Math.round(s.alt)} m / ${Math.round(s.alt * FT)} ft`],
    ["climb", s.vel_v === null ? "-" : `${s.vel_v.toFixed(1)} m/s ${phase(s)}`],
    ["speed", s.vel_h === null ? "-" : `${s.vel_h.toFixed(1)} m/s (${Math.round(s.vel_h * 3.6)} km/h)${s.heading !== null ? ` @ ${Math.round(s.heading)}°` : ""}`],
    ["temp", n(s.temp, 1, " °C")],
    ["humidity", n(s.humidity, 0, " %")],
    ["pressure", n(s.pressure, 1, " hPa")],
    ["battery", n(s.batt, 2, " V")],
    ["sats", n(s.sats, 0)],
  ];
  if (nmi !== null && brg !== null) rows.push(["from receiver", `${(nmi * 1.852).toFixed(1)} km (${nmi.toFixed(1)} nmi) @ ${Math.round(brg)}°`]);
  else rows.push(["from receiver", receiver ? "no position yet" : "set receiver location in settings"]);
  return (
    <div className="sonde">
      <div className="kvgrid">
        {rows.map(([k, v]) => (
          <div key={k} style={{ display: "contents" }}>
            <span className="label">{k}</span>
            <span>{v}</span>
          </div>
        ))}
      </div>
      <a href={`https://sondehub.org/${encodeURIComponent(s.id)}`} target="_blank" rel="noopener noreferrer" className="linkbtn">track on SondeHub ↗</a>
      <div className="charts">
        <Profile title="temperature vs altitude" unit="°C" pts={s.hist} pick={(p) => p.temp} color="#f6ad55" />
        <Profile title="humidity vs altitude" unit="%" pts={s.hist} pick={(p) => p.hum} color="#63b3ed" fixed={[0, 100]} />
      </div>
    </div>
  );
}

const W = 200;
const H = 150;
const PAD = { l: 34, r: 6, t: 6, b: 16 };

/** Sounding profile: value on x, altitude (km) on y. */
function Profile(props: { title: string; unit: string; pts: SondePt[]; pick: (p: SondePt) => number | null; color: string; fixed?: [number, number] }) {
  const d = useMemo(() => {
    const out: [number, number][] = [];
    for (const p of props.pts) {
      const v = props.pick(p);
      if (v !== null) out.push([v, p.alt]);
    }
    return out;
  }, [props.pts]); // eslint-disable-line react-hooks/exhaustive-deps
  const title = <div className="label">{props.title}</div>;
  if (d.length < 2) return <div>{title}<div className="dim">no data</div></div>;
  let x0 = props.fixed ? props.fixed[0] : Math.min(...d.map((q) => q[0]));
  let x1 = props.fixed ? props.fixed[1] : Math.max(...d.map((q) => q[0]));
  if (x1 - x0 < 1) {
    x0 -= 0.5;
    x1 += 0.5;
  }
  const y1 = Math.max(1000, Math.max(...d.map((q) => q[1])));
  const px = (v: number) => PAD.l + ((v - x0) / (x1 - x0)) * (W - PAD.l - PAD.r);
  const py = (a: number) => H - PAD.b - (a / y1) * (H - PAD.t - PAD.b);
  const line = d.map(([v, a]) => `${px(v).toFixed(1)},${py(a).toFixed(1)}`).join(" ");
  return (
    <div>
      {title}
      <svg width={W} height={H} role="img" aria-label={props.title} className="profile">
        <rect x={PAD.l} y={PAD.t} width={W - PAD.l - PAD.r} height={H - PAD.t - PAD.b} fill="none" stroke="#1f2a38" />
        <polyline points={line} fill="none" stroke={props.color} strokeWidth="1.3" strokeLinejoin="round" />
        <text x={PAD.l - 3} y={PAD.t + 8} textAnchor="end" fill="#6f8399" fontSize="9">{(y1 / 1000).toFixed(0)}k</text>
        <text x={PAD.l - 3} y={H - PAD.b} textAnchor="end" fill="#6f8399" fontSize="9">0</text>
        <text x={PAD.l} y={H - 4} textAnchor="start" fill="#6f8399" fontSize="9">{x0.toFixed(0)}</text>
        <text x={W - PAD.r} y={H - 4} textAnchor="end" fill="#6f8399" fontSize="9">{x1.toFixed(0)} {props.unit}</text>
        <text x={4} y={H / 2} fill="#6f8399" fontSize="9" transform={`rotate(-90 8 ${H / 2})`} textAnchor="middle">m</text>
      </svg>
    </div>
  );
}
