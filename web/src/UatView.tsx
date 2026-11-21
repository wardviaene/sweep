import { useMemo, useState } from "react";
import type { Receiver } from "./api";
import type { RegisterCenter } from "./mapkit";
import type { AircraftView as AcView } from "./useAircraft";
import type { FisbReport } from "./useFisb";
import { AircraftView } from "./AircraftView";

const GROUPS = ["METAR", "TAF", "WINDS", "NOTAM", "OTHER"] as const;
type Group = (typeof GROUPS)[number];

const groupOf = (t: string): Group => {
  const u = t.toUpperCase();
  if (u.startsWith("METAR") || u === "SPECI") return "METAR";
  if (u.startsWith("TAF")) return "TAF";
  if (u.startsWith("WINDS")) return "WINDS";
  if (u.includes("NOTAM")) return "NOTAM";
  return "OTHER";
};

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });

export function UatView(props: {
  aircraft: AcView;
  reports: FisbReport[];
  receiver: Receiver | null;
  registerCenter: RegisterCenter;
  onOpenSettings: () => void;
}) {
  const [tab, setTab] = useState<"air" | "wx">("air");
  return (
    <div style={{ flex: 1, minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column" }}>
      <div className="subtabs">
        <span className="seg">
          <button className={tab === "air" ? "on" : ""} onClick={() => setTab("air")}>Aircraft (UAT)</button>
          <button className={tab === "wx" ? "on" : ""} onClick={() => setTab("wx")}>Weather (FIS-B) {props.reports.length}</button>
        </span>
      </div>
      <div className="subbody">
        {/* keep the map mounted while on the weather tab so it keeps its view */}
        <div className="subpane" style={{ display: tab === "air" ? "flex" : "none" }}>
          <AircraftView
            view={props.aircraft}
            receiver={props.receiver}
            registerCenter={props.registerCenter}
            onOpenSettings={props.onOpenSettings}
            source="UAT"
          />
        </div>
        {tab === "wx" && <Weather reports={props.reports} />}
      </div>
    </div>
  );
}

function Weather({ reports }: { reports: FisbReport[] }) {
  const [q, setQ] = useState("");
  const filtered = useMemo(() => {
    const f = q.trim().toLowerCase();
    return f ? reports.filter((r) => r.location.toLowerCase().includes(f) || r.text.toLowerCase().includes(f)) : reports;
  }, [reports, q]);
  const groups = useMemo(() => {
    const g = new Map<Group, FisbReport[]>(GROUPS.map((k) => [k, []]));
    for (const r of filtered) g.get(groupOf(r.type))!.push(r);
    return g;
  }, [filtered]);
  return (
    <div className="wx">
      <div className="wxbar">
        <input className="freq" style={{ width: 180, textAlign: "left" }} placeholder="filter location / text" value={q} onChange={(e) => setQ(e.target.value)} />
        <span className="dim">
          {GROUPS.map((k) => `${k} ${groups.get(k)!.length}`).join(" · ")} (latest per type and location, max 500)
        </span>
      </div>
      <div className="wxlist">
        {reports.length === 0 && <div className="dim pad">no FIS-B weather reports yet (uplink reports arrive from ground stations; may take minutes)</div>}
        {GROUPS.map((k) => {
          const rows = groups.get(k)!;
          if (!rows.length) return null;
          return (
            <div key={k}>
              <div className="wxhead">{k} <span className="dim">({rows.length})</span></div>
              {rows.map((r) => (
                <div key={r.key} className="wxrow">
                  <span className="wxloc">{r.location || "-"}</span>
                  <span className="dim wxtime" title={`received ${time(r.ts)}`}>{r.time || time(r.ts)}</span>
                  <span className="wxtext">{r.text}</span>
                </div>
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}
