import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { DecoderName, Receiver, Status } from "./api";
import type { DecEvent } from "./useEvents";
import type { RegisterCenter } from "./mapkit";
import { useAircraft } from "./useAircraft";
import { useAprs } from "./useAprs";
import { useAis } from "./useAis";
import { useSensors } from "./useSensors";
import { AircraftView } from "./AircraftView";
import { AprsView } from "./AprsView";
import { AisView } from "./AisView";
import { SensorsView } from "./SensorsView";

type Pick = DecoderName | "none";
const PICKS: Pick[] = ["none", "rtl_433", "adsb", "aprs", "ais"];
const MAX_ROWS = 200;
const SIZE_KEY = "sweep.decExpanded";

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });
const str = (v: unknown): string => (v === undefined || v === null ? "" : String(v));

function loadExpanded(): boolean {
  try {
    return localStorage.getItem(SIZE_KEY) === "1";
  } catch {
    return false;
  }
}

interface Props {
  status: Status | null;
  events: DecEvent[];
  onSet: (d: Pick) => void;
  error: string | null;
  receiver: Receiver | null;
  registerCenter: RegisterCenter;
  onOpenSettings: () => void;
}

export function DecoderPanel({ status, events, onSet, error, receiver, registerCenter, onOpenSettings }: Props) {
  const [open, setOpen] = useState(true);
  const [expanded, setExpanded] = useState(loadExpanded);
  const [tab, setTab] = useState<"view" | "raw">("view");
  const [pick, setPick] = useState<Pick>("none");
  const dec = status?.decoder;
  const running = !!dec?.running && dec.name !== "none";

  const aircraft = useAircraft(events);
  const aprs = useAprs(events);
  const vessels = useAis(events);
  const sensors = useSensors(events);

  const toggleSize = () => {
    const v = !expanded;
    setExpanded(v);
    try {
      localStorage.setItem(SIZE_KEY, v ? "1" : "0");
    } catch {
      /* storage unavailable */
    }
  };

  // Follow the backend: when a decoder is running (started from here or from a band), select it.
  useEffect(() => {
    if (dec && dec.name !== "none") setPick(dec.name);
  }, [dec?.name]); // eslint-disable-line react-hooks/exhaustive-deps

  const mine = useMemo(() => (pick === "none" ? events : events.filter((e) => e.decoder === pick)), [events, pick]);
  const logs = useMemo(() => mine.filter((e) => e.kind === "log").slice(-3), [mine]);
  const rich = pick !== "none";
  const showView = rich && tab === "view";

  return (
    <section className="dec" style={open ? { flex: `0 0 ${expanded ? 75 : 30}%` } : undefined}>
      <div className="bar dec-head">
        <button onClick={() => setOpen(!open)} title={open ? "Collapse" : "Expand"}>{open ? "▾" : "▸"}</button>
        <button className={expanded ? "on" : ""} disabled={!open} onClick={toggleSize} title={expanded ? "Smaller decoder area" : "Larger decoder area"}>
          {expanded ? "⤡" : "⤢"}
        </button>
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
        {rich && (
          <span className="seg">
            <button className={tab === "view" ? "on" : ""} onClick={() => setTab("view")}>view</button>
            <button className={tab === "raw" ? "on" : ""} onClick={() => setTab("raw")}>raw</button>
          </span>
        )}
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
          {showView && (
            <div className="dec-view">
              {pick === "adsb" && <AircraftView view={aircraft} receiver={receiver} registerCenter={registerCenter} onOpenSettings={onOpenSettings} />}
              {pick === "aprs" && <AprsView view={aprs} registerCenter={registerCenter} />}
              {pick === "ais" && <AisView vessels={vessels} registerCenter={registerCenter} />}
              {pick === "rtl_433" && <SensorsView sensors={sensors} />}
            </div>
          )}
          {!showView && (
            <div className="tablewrap">
              <All events={mine} />
            </div>
          )}
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
