import { useEffect, useMemo, useRef, useState } from "react";
import type { AprsPacket, AprsView as View } from "./useAprs";
import { esc, L, MapBox, SortTable, useFit, type Col, type RegisterCenter } from "./mapkit";

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });

export function AprsView(props: { view: View; registerCenter: RegisterCenter; title?: string; info?: string }) {
  const { view } = props;
  const [map, setMap] = useState<L.Map | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const store = useRef<{ g: L.LayerGroup; m: Map<string, { c: L.CircleMarker; t: L.Polyline }> } | null>(null);

  useEffect(() => {
    if (!map) {
      store.current = null;
      return;
    }
    store.current = { g: L.layerGroup().addTo(map), m: new Map() };
    return () => {
      store.current = null;
    };
  }, [map]);

  useEffect(() => {
    const s = store.current;
    if (!s) return;
    const live = new Set<string>();
    for (const st of view.stations) {
      if (st.lat === null || st.lon === null) continue;
      live.add(st.call);
      const on = st.call === selected;
      let e = s.m.get(st.call);
      if (!e) {
        const c = L.circleMarker([st.lat, st.lon], { radius: 5, color: "#05080c", weight: 1, fillColor: "#f6ad55", fillOpacity: 0.9 });
        c.bindTooltip(esc(st.call), { permanent: true, direction: "right", offset: [6, 0], className: "stlabel" });
        c.on("click", () => setSelected(st.call));
        const t = L.polyline([], { weight: 1.5, opacity: 0.6, color: "#f6ad55", interactive: false });
        s.g.addLayer(t).addLayer(c);
        e = { c, t };
        s.m.set(st.call, e);
      }
      e.c.setLatLng([st.lat, st.lon]);
      e.c.setStyle({ radius: on ? 8 : 5, fillColor: on ? "#4fd1c5" : "#f6ad55" });
      e.t.setLatLngs(st.track.length > 1 ? st.track : []);
    }
    for (const [k, e] of s.m)
      if (!live.has(k)) {
        s.g.removeLayer(e.c).removeLayer(e.t);
        s.m.delete(k);
      }
  }, [map, view.stations, selected]);

  const pts = useMemo(
    () => view.stations.filter((s) => s.lat !== null && s.lon !== null).map((s): [number, number] => [s.lat!, s.lon!]),
    [view.stations],
  );
  const fit = useFit(map, pts);

  const pan = (call: string) => {
    setSelected(call);
    const st = view.stations.find((x) => x.call === call);
    if (map && st && st.lat !== null && st.lon !== null) map.panTo([st.lat, st.lon]);
  };

  const hasSat = useMemo(() => view.packets.some((p) => p.satellite !== ""), [view.packets]);
  const cols: Col<AprsPacket>[] = useMemo(
    () => [
      { key: "time", label: "time", sort: (r) => r.id, firstDir: -1, render: (r) => time(r.ts) },
      { key: "from", label: "from", sort: (r) => r.from, render: (r) => r.from },
      { key: "path", label: "to / path", sort: (r) => r.to, render: (r) => `${r.to}${r.path ? " via " + r.path : ""}` },
      ...(hasSat
        ? ([{ key: "sat", label: "satellite", sort: (r) => r.satellite || null, render: (r) => (r.satellite ? <span className="lbl">{r.satellite}</span> : "") }] as Col<AprsPacket>[])
        : []),
      { key: "pos", label: "pos", sort: (r) => (r.lat === null ? null : r.lat), render: (r) => (r.lat === null || r.lon === null ? "" : "●") },
      { key: "info", label: "comment / info", sort: (r) => r.info, render: (r) => <span className="kv">{r.info}</span> },
    ],
    [hasSat],
  );

  const withPos = pts.length;
  return (
    <div className="split">
      <MapBox onMap={setMap} registerCenter={props.registerCenter}>
        <div className="maptools"><button className="mini" onClick={fit}>fit</button></div>
      </MapBox>
      <div className="side">
        <div className="dim pad">
          {props.title && <b style={{ color: "var(--text)" }}>{props.title}</b>}
          {props.info && <> · {props.info}</>}
          {props.title && " · "}{view.stations.length} stations, {withPos} with position
        </div>
        <SortTable
          rows={view.packets}
          cols={cols}
          rowKey={(r) => String(r.id)}
          defaultSort="time"
          defaultDir={-1}
          empty={`no ${props.title ? "packets" : "APRS packets"} yet`}
          onClick={(r) => pan(r.from)}
          rowClass={(r) => (r.from === selected ? "sel" : "")}
          limit={300}
        />
      </div>
    </div>
  );
}
