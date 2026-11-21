import { useEffect, useMemo, useRef, useState } from "react";
import type { Receiver } from "./api";
import { bearingDeg, rangeNmi, NMI_M } from "./geo";
import type { Aircraft, AircraftView as View } from "./useAircraft";
import { esc, L, MapBox, SortTable, useFit, type Col, type RegisterCenter } from "./mapkit";

const RINGS = [25, 50, 100, 200];
const LEGEND: { label: string; ft: number | "ground" }[] = [
  { label: "gnd", ft: "ground" },
  { label: "0", ft: 0 },
  { label: "10k", ft: 10000 },
  { label: "20k", ft: 20000 },
  { label: "30k", ft: 30000 },
  { label: "40k+", ft: 40000 },
];

/** ground gray, low = warm (orange), high = cool (blue). */
export function altColor(alt: number | "ground" | null): string {
  if (alt === "ground") return "#8a97a6";
  if (alt === null) return "#b0b8c2";
  const t = Math.min(1, Math.max(0, alt / 40000));
  return `hsl(${Math.round(25 + t * 195)}, 90%, 60%)`;
}
const fl = (alt: number | "ground" | null) =>
  alt === "ground" ? "GND" : alt === null ? "" : `FL${String(Math.round(alt / 100)).padStart(3, "0")}`;
const link = (hex: string) => `https://globe.adsbexchange.com/?icao=${encodeURIComponent(hex)}`;
const name = (a: Aircraft) => a.flight || a.hex.toUpperCase();

const PLANE = "M12 1.5l1.9 7.2 8.1 5v2.3l-8.1-2.4-.9 5 2.8 2v1.4L12 21.5l-3.8 1.5v-1.4l2.8-2-.9-5-8.1 2.4v-2.3l8.1-5z";
function icon(a: Aircraft, on: boolean): L.DivIcon {
  const size = on ? 30 : 22;
  const col = altColor(a.alt);
  const label = `${esc(name(a))} ${fl(a.alt)}`.trim();
  return L.divIcon({
    className: `ac${on ? " on" : ""}`,
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
    html:
      `<svg width="${size}" height="${size}" viewBox="0 0 24 24" style="transform:rotate(${Math.round(a.track ?? 0)}deg)">` +
      `<path d="${PLANE}" fill="${col}" stroke="#05080c" stroke-width="1"/></svg>` +
      `<span class="aclabel" style="left:${size + 1}px">${label}</span>`,
  });
}

function popup(a: Aircraft): string {
  return (
    `<b>${esc(name(a))}</b> ${esc(a.hex)}<br>` +
    `${fl(a.alt)} ${a.gs !== null ? Math.round(a.gs) + " kt" : ""}<br>` +
    `<a href="${link(a.hex)}" target="_blank" rel="noopener noreferrer">ADS-B Exchange ↗</a>`
  );
}

interface Row extends Aircraft {
  range: number | null;
  bearing: number | null;
}

interface Layers {
  markers: Map<string, { m: L.Marker; key: string }>;
  trails: Map<string, L.Polyline>;
  rx: L.LayerGroup;
}

export function AircraftView(props: {
  view: View;
  receiver: Receiver | null;
  registerCenter: RegisterCenter;
  onOpenSettings: () => void;
  /** label of the data source, e.g. "ADS-B" or "UAT" */
  source?: string;
}) {
  const { view, receiver } = props;
  const [map, setMap] = useState<L.Map | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [hover, setHover] = useState<string | null>(null);
  const layers = useRef<Layers | null>(null);
  const byHex = useRef(new Map<string, Aircraft>());
  const hl = hover ?? selected;

  const rows: Row[] = useMemo(
    () =>
      view.aircraft.map((a) => {
        const has = receiver && a.lat !== null && a.lon !== null;
        const p = has ? { lat: a.lat!, lon: a.lon! } : null;
        return {
          ...a,
          range: receiver && p ? rangeNmi(receiver, p) : null,
          bearing: receiver && p ? bearingDeg(receiver, p) : null,
        };
      }),
    [view.aircraft, receiver],
  );

  // map setup / teardown of our layers
  useEffect(() => {
    if (!map) {
      layers.current = null;
      return;
    }
    layers.current = { markers: new Map(), trails: new Map(), rx: L.layerGroup().addTo(map) };
    return () => {
      layers.current = null;
    };
  }, [map]);

  // receiver marker + range rings
  useEffect(() => {
    const l = layers.current;
    if (!map || !l) return;
    l.rx.clearLayers();
    if (!receiver) return;
    const c: L.LatLngTuple = [receiver.lat, receiver.lon];
    L.circleMarker(c, { radius: 5, color: "#4fd1c5", weight: 2, fillOpacity: 0.6 }).bindTooltip("receiver").addTo(l.rx);
    for (const nm of RINGS) {
      L.circle(c, { radius: nm * NMI_M, color: "#4fd1c5", weight: 1, opacity: 0.55, fill: false, dashArray: "4 4", interactive: false }).addTo(l.rx);
      L.marker([receiver.lat + nm / 60, receiver.lon], {
        interactive: false,
        icon: L.divIcon({ className: "ring-label", html: `${nm} nmi`, iconSize: [50, 12], iconAnchor: [25, 6] }),
      }).addTo(l.rx);
    }
  }, [map, receiver]);

  // aircraft markers + trails
  useEffect(() => {
    const l = layers.current;
    if (!map || !l) return;
    byHex.current = new Map(view.aircraft.map((a) => [a.hex, a]));
    const live = new Set<string>();
    for (const a of view.aircraft) {
      if (a.lat === null || a.lon === null) continue;
      live.add(a.hex);
      const on = a.hex === hl;
      const key = `${Math.round((a.track ?? 0) / 3)}|${altColor(a.alt)}|${on}|${name(a)}|${fl(a.alt)}`;
      let e = l.markers.get(a.hex);
      if (!e) {
        const m = L.marker([a.lat, a.lon], { icon: icon(a, on), riseOnHover: true });
        m.bindPopup(() => {
          const cur = byHex.current.get(a.hex);
          return cur ? popup(cur) : "";
        });
        m.on("click", () => setSelected(a.hex));
        m.addTo(map);
        e = { m, key };
        l.markers.set(a.hex, e);
      } else {
        e.m.setLatLng([a.lat, a.lon]);
        if (e.key !== key) {
          e.m.setIcon(icon(a, on));
          e.key = key;
        }
      }
      e.m.setZIndexOffset(on ? 1000 : 0);
      let t = l.trails.get(a.hex);
      if (!t) {
        t = L.polyline(a.trail, { weight: 1.5, opacity: 0.6, color: altColor(a.alt), interactive: false }).addTo(map);
        l.trails.set(a.hex, t);
      } else {
        t.setLatLngs(a.trail);
        t.setStyle({ color: altColor(a.alt), weight: on ? 3 : 1.5 });
      }
    }
    for (const [hex, e] of l.markers)
      if (!live.has(hex)) {
        e.m.remove();
        l.markers.delete(hex);
        l.trails.get(hex)?.remove();
        l.trails.delete(hex);
      }
  }, [map, view.aircraft, hl]);

  const pts: [number, number][] = useMemo(() => {
    const p = view.aircraft.filter((a) => a.lat !== null && a.lon !== null).map((a): [number, number] => [a.lat!, a.lon!]);
    if (p.length && receiver) p.push([receiver.lat, receiver.lon]);
    return p;
  }, [view.aircraft, receiver]);
  const fit = useFit(map, pts);

  // no aircraft yet but a receiver: show it once
  const centered = useRef(false);
  useEffect(() => {
    if (map && receiver && !pts.length && !centered.current) {
      centered.current = true;
      map.setView([receiver.lat, receiver.lon], 8);
    }
  }, [map, receiver, pts.length]);

  const pick = (a: Aircraft) => {
    setSelected(a.hex);
    if (map && a.lat !== null && a.lon !== null) map.panTo([a.lat, a.lon]);
  };

  const cols: Col<Row>[] = useMemo(
    () => [
      { key: "call", label: "callsign", sort: (r) => r.flight || null, render: (r) => r.flight || <span className="dim">-</span> },
      { key: "hex", label: "hex", sort: (r) => r.hex, render: (r) => r.hex },
      { key: "alt", label: "alt ft", right: true, firstDir: -1, sort: (r) => (r.alt === "ground" ? 0 : r.alt), render: (r) => (r.alt === "ground" ? "gnd" : r.alt ?? "") },
      { key: "trend", label: "", sort: (r) => r.trend, render: (r) => (r.trend > 0 ? <span className="up">↑</span> : r.trend < 0 ? <span className="down">↓</span> : "") },
      { key: "gs", label: "gs kt", right: true, firstDir: -1, sort: (r) => r.gs, render: (r) => (r.gs === null ? "" : Math.round(r.gs)) },
      { key: "trk", label: "trk °", right: true, sort: (r) => r.track, render: (r) => (r.track === null ? "" : Math.round(r.track)) },
      { key: "range", label: "range nmi", right: true, sort: (r) => r.range, render: (r) => (r.range === null ? "" : r.range.toFixed(1)) },
      { key: "brg", label: "brg °", right: true, sort: (r) => r.bearing, render: (r) => (r.bearing === null ? "" : Math.round(r.bearing)) },
      { key: "msgs", label: "msgs", right: true, firstDir: -1, sort: (r) => r.messages, render: (r) => r.messages ?? "" },
      { key: "seen", label: "seen s", right: true, sort: (r) => r.seen, render: (r) => (r.seen === null ? "" : r.seen.toFixed(0)) },
      {
        key: "link", label: "", sort: () => null,
        render: (r) => (
          <a href={link(r.hex)} target="_blank" rel="noopener noreferrer" title="ADS-B Exchange" onClick={(e) => e.stopPropagation()}>↗</a>
        ),
      },
    ],
    [],
  );

  return (
    <div className="split">
      <MapBox onMap={setMap} registerCenter={props.registerCenter}>
        <div className="maptools">
          <button className="mini" onClick={fit}>fit</button>
        </div>
        <div className="legend">
          {LEGEND.map((l) => (
            <span key={l.label}><i style={{ background: altColor(l.ft) }} />{l.label}</span>
          ))}
          <span className="dim">ft</span>
        </div>
        {!receiver && (
          <div className="mapnote">
            <button className="linkbtn" onClick={props.onOpenSettings}>set receiver location</button> for range/bearing
          </div>
        )}
      </MapBox>
      <div className="side">
        <div className="dim pad">{props.source ?? "ADS-B"}: {view.aircraft.length} aircraft, {view.withPos} with position</div>
        <SortTable
          key={receiver ? "rx" : "norx"}
          rows={rows}
          cols={cols}
          rowKey={(r) => r.hex}
          defaultSort={receiver ? "range" : "seen"}
          empty={`no ${props.source ?? "ADS-B"} aircraft snapshot yet`}
          onClick={pick}
          onHover={(r) => setHover(r ? r.hex : null)}
          rowClass={(r) => (r.hex === selected ? "sel" : r.hex === hover ? "hov" : "")}
          selectedKey={selected}
        />
      </div>
    </div>
  );
}
