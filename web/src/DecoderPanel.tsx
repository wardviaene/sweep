import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { DecoderName, Status } from "./api";
import type { DecEvent } from "./useEvents";

type Pick = DecoderName | "none";
const PICKS: Pick[] = ["none", "rtl_433", "adsb", "aprs", "ais"];
const MAX_ROWS = 200;

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });
const num = (v: unknown, d = 0): string => (typeof v === "number" && Number.isFinite(v) ? v.toFixed(d) : "");
const str = (v: unknown): string => (v === undefined || v === null ? "" : String(v));

interface Props {
  status: Status | null;
  events: DecEvent[];
  onSet: (d: Pick) => void;
  error: string | null;
}

export function DecoderPanel({ status, events, onSet, error }: Props) {
  const [open, setOpen] = useState(true);
  const [pick, setPick] = useState<Pick>("none");
  const dec = status?.decoder;
  const running = !!dec?.running && dec.name !== "none";

  // Follow the backend: when a decoder is running (started from here or from a band), select it.
  useEffect(() => {
    if (dec && dec.name !== "none") setPick(dec.name);
  }, [dec?.name]); // eslint-disable-line react-hooks/exhaustive-deps

  const mine = useMemo(() => (pick === "none" ? events : events.filter((e) => e.decoder === pick)), [events, pick]);
  const logs = useMemo(() => mine.filter((e) => e.kind === "log").slice(-3), [mine]);

  return (
    <section className="dec">
      <div className="bar dec-head">
        <button onClick={() => setOpen(!open)} title={open ? "Collapse" : "Expand"}>{open ? "▾" : "▸"}</button>
        <span className="label">decoder</span>
        <select value={pick} onChange={(e) => setPick(e.target.value as Pick)}>
          {PICKS.map((d) => (
            <option key={d} value={d}>{d === "none" ? "none (all events)" : d}</option>
          ))}
        </select>
        <button disabled={pick === "none" || (running && dec?.name === pick)} onClick={() => pick !== "none" && onSet(pick)}>
          Start
        </button>
        <button disabled={!running} onClick={() => onSet("none")}>Stop</button>
        <span className="item">
          {dec ? (
            <>
              <span className={running ? "conn-open" : "dim"}>{running ? `${dec.name} running` : "idle"}</span>
              {running && <span className="dim"> {(dec.freq_hz / 1e6).toFixed(4)} MHz · {dec.events} events</span>}
              {dec.error && <span className="err">{dec.error}</span>}
            </>
          ) : (
            <span className="dim">no decoder status</span>
          )}
        </span>
        {error && <span className="err">{error}</span>}
      </div>
      {open && (
        <div className="dec-body">
          <div className="loglines">
            {logs.map((e) => (
              <div key={e.id} className="dim">{time(e.ts)} [{e.decoder}] {str(e.data.line)}</div>
            ))}
          </div>
          <div className="tablewrap">
            {pick === "rtl_433" && <Rtl433 events={mine} />}
            {pick === "adsb" && <Adsb events={mine} />}
            {pick === "aprs" && <Aprs events={mine} />}
            {pick === "ais" && <Ais events={mine} />}
            {pick === "none" && <All events={mine} />}
          </div>
        </div>
      )}
    </section>
  );
}

function Table({ head, rows, empty }: { head: string[]; rows: ReactNode[][]; empty: string }) {
  if (!rows.length) return <div className="dim pad">{empty}</div>;
  return (
    <table>
      <thead>
        <tr>{head.map((h) => <th key={h}>{h}</th>)}</tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>{r.map((c, j) => <td key={j}>{c}</td>)}</tr>
        ))}
      </tbody>
    </table>
  );
}

const newest = (events: DecEvent[], kind: string) => events.filter((e) => e.kind === kind).slice(-MAX_ROWS).reverse();

const RTL_SKIP = new Set(["time", "model", "id", "channel"]);
function Rtl433({ events }: { events: DecEvent[] }) {
  const rows = newest(events, "message").map((e) => {
    const rest = Object.entries(e.data)
      .filter(([k]) => !RTL_SKIP.has(k))
      .map(([k, v]) => `${k}=${typeof v === "number" ? +v.toFixed(2) : str(v)}`)
      .join(" ");
    return [time(e.ts), str(e.data.model), str(e.data.id), str(e.data.channel), <span className="kv">{rest}</span>];
  });
  return <Table head={["time", "model", "id", "ch", "fields"]} rows={rows} empty="no rtl_433 messages yet" />;
}

interface Aircraft {
  hex?: string;
  flight?: string;
  alt_baro?: number | string;
  gs?: number;
  track?: number;
  lat?: number;
  lon?: number;
  seen?: number;
  rssi?: number;
  messages?: number;
}
function Adsb({ events }: { events: DecEvent[] }) {
  let snap: DecEvent | undefined;
  for (let i = events.length - 1; i >= 0; i--) {
    if (events[i]!.kind === "snapshot") {
      snap = events[i];
      break;
    }
  }
  const ac = ((snap?.data.aircraft as Aircraft[] | undefined) ?? []).slice().sort((a, b) => (a.seen ?? 1e9) - (b.seen ?? 1e9));
  const rows = ac.map((a) => [
    str(a.hex),
    str(a.flight).trim(),
    str(a.alt_baro),
    num(a.gs),
    num(a.track),
    num(a.lat, 4),
    num(a.lon, 4),
    num(a.seen, 1),
    num(a.rssi, 1),
    str(a.messages),
  ]);
  return (
    <>
      {snap && <div className="dim pad">snapshot {time(snap.ts)} · {ac.length} aircraft</div>}
      <Table head={["hex", "flight", "alt", "gs", "trk", "lat", "lon", "seen", "rssi", "msgs"]} rows={rows} empty="no aircraft snapshot yet" />
    </>
  );
}

function Aprs({ events }: { events: DecEvent[] }) {
  const rows = newest(events, "message").map((e) => {
    const d = e.data;
    const path = Array.isArray(d.path) ? (d.path as unknown[]).join(",") : "";
    return [
      time(e.ts),
      str(d.from),
      `${str(d.to)}${path ? " via " + path : ""}`,
      typeof d.lat === "number" && typeof d.lon === "number" ? `${num(d.lat, 4)}, ${num(d.lon, 4)}` : "",
      str(d.comment ?? d.info),
    ];
  });
  return <Table head={["time", "from", "to / path", "lat, lon", "comment / info"]} rows={rows} empty="no APRS packets yet" />;
}

interface Vessel {
  mmsi: number;
  name?: string;
  callsign?: string;
  shiptype?: number;
  lat?: number;
  lon?: number;
  sog?: number;
  cog?: number;
  ts: number;
}
function Ais({ events }: { events: DecEvent[] }) {
  const vessels = useMemo(() => {
    const m = new Map<number, Vessel>();
    for (const e of events) {
      if (e.kind !== "message") continue;
      const d = e.data;
      if (typeof d.mmsi !== "number") continue;
      const v = m.get(d.mmsi) ?? { mmsi: d.mmsi, ts: e.ts };
      v.ts = e.ts;
      if (typeof d.name === "string" && d.name) v.name = d.name;
      if (typeof d.callsign === "string" && d.callsign) v.callsign = d.callsign;
      if (typeof d.shiptype === "number") v.shiptype = d.shiptype;
      if (typeof d.lat === "number" && typeof d.lon === "number") {
        v.lat = d.lat;
        v.lon = d.lon;
      }
      if (typeof d.sog === "number") v.sog = d.sog;
      if (typeof d.cog === "number") v.cog = d.cog;
      m.set(d.mmsi, v);
    }
    return [...m.values()].sort((a, b) => b.ts - a.ts);
  }, [events]);
  const rows = vessels.slice(0, MAX_ROWS).map((v) => [
    String(v.mmsi),
    `${v.name ?? ""}${v.callsign ? ` (${v.callsign})` : ""}`,
    v.shiptype === undefined ? "" : String(v.shiptype),
    num(v.lat, 4),
    num(v.lon, 4),
    num(v.sog, 1),
    num(v.cog, 0),
    time(v.ts),
  ]);
  return <Table head={["mmsi", "name", "type", "lat", "lon", "sog", "cog", "last seen"]} rows={rows} empty="no AIS vessels yet" />;
}

function All({ events }: { events: DecEvent[] }) {
  const rows = events
    .slice(-MAX_ROWS)
    .reverse()
    .map((e) => {
      const s = JSON.stringify(e.data);
      return [time(e.ts), e.decoder, e.kind, <span className="kv">{s.length > 160 ? s.slice(0, 160) + "…" : s}</span>];
    });
  return <Table head={["time", "decoder", "kind", "data"]} rows={rows} empty="no events yet" />;
}
