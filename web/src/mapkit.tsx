import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import L from "leaflet";
import "leaflet/dist/leaflet.css";
import type { LatLon } from "./geo";

export { L };
export type RegisterCenter = (get: (() => LatLon) | null) => void;

export const esc = (s: string) =>
  s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c] ?? c);

/** Plain Leaflet map in a div; calls onMap when ready (null on teardown) and invalidates on resize. */
export function MapBox(props: { onMap: (m: L.Map | null) => void; registerCenter?: RegisterCenter; children?: ReactNode }) {
  const div = useRef<HTMLDivElement>(null);
  const cbs = useRef(props);
  cbs.current = props;

  useEffect(() => {
    const el = div.current;
    if (!el) return;
    const map = L.map(el, { center: [30, 0], zoom: 2, worldCopyJump: true, attributionControl: true });
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      attribution: "© OpenStreetMap contributors",
    }).addTo(map);
    const ro = new ResizeObserver(() => map.invalidateSize());
    ro.observe(el);
    cbs.current.onMap(map);
    cbs.current.registerCenter?.(() => {
      const c = map.getCenter();
      return { lat: c.lat, lon: ((c.lng + 540) % 360) - 180 };
    });
    return () => {
      ro.disconnect();
      cbs.current.registerCenter?.(null);
      cbs.current.onMap(null);
      map.remove();
    };
  }, []);

  return (
    <div className="mapwrap">
      <div ref={div} className="map" />
      {props.children}
    </div>
  );
}

export interface Col<T> {
  key: string;
  label: string;
  /** value used for sorting; null sorts last */
  sort: (r: T) => number | string | null;
  render: (r: T) => ReactNode;
  right?: boolean;
  /** first-click direction */
  firstDir?: 1 | -1;
}

/** Sortable compact table; rows keyed by `rowKey`. */
export function SortTable<T>(props: {
  rows: T[];
  cols: Col<T>[];
  rowKey: (r: T) => string;
  defaultSort: string;
  defaultDir?: 1 | -1;
  empty: string;
  onClick?: (r: T) => void;
  onHover?: (r: T | null) => void;
  rowClass?: (r: T) => string;
  limit?: number;
  /** scrolled into view when it changes (row must carry class "sel") */
  selectedKey?: string | null;
}) {
  const { rows, cols } = props;
  const [sort, setSort] = useState({ key: props.defaultSort, dir: props.defaultDir ?? (1 as 1 | -1) });
  const wrap = useRef<HTMLDivElement>(null);
  const sorted = useMemo(() => {
    const c = cols.find((x) => x.key === sort.key) ?? cols[0];
    if (!c) return rows;
    const out = rows.slice();
    out.sort((a, b) => {
      const x = c.sort(a);
      const y = c.sort(b);
      if (x === null && y === null) return 0;
      if (x === null) return 1;
      if (y === null) return -1;
      return (typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y))) * sort.dir;
    });
    return out.slice(0, props.limit ?? 500);
  }, [rows, cols, sort, props.limit]);

  // Keep a highlighted row visible.
  const selKey = props.selectedKey;
  useEffect(() => {
    if (selKey) wrap.current?.querySelector("tr.sel")?.scrollIntoView({ block: "nearest" });
  }, [selKey]);

  if (!rows.length) return <div className="dim pad">{props.empty}</div>;
  return (
    <div className="tablewrap" ref={wrap}>
      <table>
        <thead>
          <tr>
            {cols.map((c) => (
              <th
                key={c.key}
                className="sortable"
                style={c.right ? { textAlign: "right" } : undefined}
                onClick={() => setSort((s) => (s.key === c.key ? { key: c.key, dir: (-s.dir) as 1 | -1 } : { key: c.key, dir: c.firstDir ?? 1 }))}
              >
                {c.label}
                {sort.key === c.key ? (sort.dir === 1 ? " ▲" : " ▼") : ""}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {sorted.map((r) => (
            <tr
              key={props.rowKey(r)}
              className={`${props.onClick ? "click" : ""} ${props.rowClass?.(r) ?? ""}`}
              onClick={() => props.onClick?.(r)}
              onMouseEnter={() => props.onHover?.(r)}
              onMouseLeave={() => props.onHover?.(null)}
            >
              {cols.map((c) => (
                <td key={c.key} style={c.right ? { textAlign: "right" } : undefined}>{c.render(r)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Fit once, the first time `points` is non-empty; `fit()` forces it. */
export function useFit(map: L.Map | null, points: [number, number][]) {
  const done = useRef(false);
  useEffect(() => {
    done.current = false;
  }, [map]);
  const fit = () => {
    if (!map || !points.length) return;
    if (points.length === 1) map.setView(points[0]!, Math.max(map.getZoom(), 9));
    else map.fitBounds(L.latLngBounds(points), { padding: [30, 30], maxZoom: 12 });
  };
  useEffect(() => {
    if (!map || done.current || !points.length) return;
    done.current = true;
    fit();
  }); // eslint-disable-line react-hooks/exhaustive-deps
  return fit;
}
