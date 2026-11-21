import { useEffect, useMemo, useRef, useState } from "react";
import type { Vessel } from "./useAis";
import { esc, L, MapBox, SortTable, useFit, type Col, type RegisterCenter } from "./mapkit";

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });
const label = (v: Vessel) => v.name || String(v.mmsi);
const dir = (v: Vessel) => (v.heading !== null && v.heading >= 0 && v.heading < 360 ? v.heading : v.cog !== null && v.cog < 360 ? v.cog : 0);

function icon(v: Vessel, on: boolean): L.DivIcon {
  const s = on ? 22 : 16;
  return L.divIcon({
    className: "ac",
    iconSize: [s, s],
    iconAnchor: [s / 2, s / 2],
    html:
      `<svg width="${s}" height="${s}" viewBox="0 0 24 24" style="transform:rotate(${Math.round(dir(v))}deg)">` +
      `<path d="M12 2l7 19-7-4-7 4z" fill="${on ? "#4fd1c5" : "#63b3ed"}" stroke="#05080c" stroke-width="1.2"/></svg>` +
      `<span class="aclabel" style="left:${s + 1}px">${esc(label(v))}</span>`,
  });
}

export function AisView(props: { vessels: Vessel[]; registerCenter: RegisterCenter }) {
  const { vessels } = props;
  const [map, setMap] = useState<L.Map | null>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const store = useRef<Map<number, { m: L.Marker; key: string }> | null>(null);

  useEffect(() => {
    if (!map) {
      store.current = null;
      return;
    }
    store.current = new Map();
    return () => {
      store.current = null;
    };
  }, [map]);

  useEffect(() => {
    const s = store.current;
    if (!map || !s) return;
    const live = new Set<number>();
    for (const v of vessels) {
      if (v.lat === null || v.lon === null) continue;
      live.add(v.mmsi);
      const on = v.mmsi === selected;
      const key = `${Math.round(dir(v) / 3)}|${label(v)}|${on}`;
      let e = s.get(v.mmsi);
      if (!e) {
        const m = L.marker([v.lat, v.lon], { icon: icon(v, on) });
        m.on("click", () => setSelected(v.mmsi));
        m.addTo(map);
        e = { m, key };
        s.set(v.mmsi, e);
      } else {
        e.m.setLatLng([v.lat, v.lon]);
        if (e.key !== key) {
          e.m.setIcon(icon(v, on));
          e.key = key;
        }
      }
    }
    for (const [k, e] of s)
      if (!live.has(k)) {
        e.m.remove();
        s.delete(k);
      }
  }, [map, vessels, selected]);

  const pts = useMemo(
    () => vessels.filter((v) => v.lat !== null && v.lon !== null).map((v): [number, number] => [v.lat!, v.lon!]),
    [vessels],
  );
  const fit = useFit(map, pts);

  const cols: Col<Vessel>[] = useMemo(
    () => [
      { key: "mmsi", label: "mmsi", sort: (r) => r.mmsi, render: (r) => r.mmsi },
      { key: "name", label: "name", sort: (r) => r.name || null, render: (r) => r.name },
      { key: "call", label: "callsign", sort: (r) => r.callsign || null, render: (r) => r.callsign },
      { key: "type", label: "type", right: true, sort: (r) => r.shiptype, render: (r) => r.shiptype ?? "" },
      { key: "sog", label: "sog kt", right: true, firstDir: -1, sort: (r) => r.sog, render: (r) => (r.sog === null ? "" : r.sog.toFixed(1)) },
      { key: "cog", label: "cog °", right: true, sort: (r) => r.cog, render: (r) => (r.cog === null ? "" : Math.round(r.cog)) },
      { key: "pos", label: "position", sort: (r) => r.lat, render: (r) => (r.lat === null || r.lon === null ? "" : `${r.lat.toFixed(4)}, ${r.lon.toFixed(4)}`) },
      { key: "seen", label: "last seen", sort: (r) => r.ts, firstDir: -1, render: (r) => time(r.ts) },
    ],
    [],
  );

  return (
    <div className="split">
      <MapBox onMap={setMap} registerCenter={props.registerCenter}>
        <div className="maptools"><button className="mini" onClick={fit}>fit</button></div>
      </MapBox>
      <div className="side">
        <div className="dim pad">{vessels.length} vessels, {pts.length} with position</div>
        <SortTable
          rows={vessels}
          cols={cols}
          rowKey={(r) => String(r.mmsi)}
          defaultSort="seen"
          defaultDir={-1}
          empty="no AIS vessels yet"
          onClick={(v) => {
            setSelected(v.mmsi);
            if (map && v.lat !== null && v.lon !== null) map.panTo([v.lat, v.lon]);
          }}
          rowClass={(r) => (r.mmsi === selected ? "sel" : "")}
          selectedKey={selected === null ? null : String(selected)}
        />
      </div>
    </div>
  );
}
