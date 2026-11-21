import { useEffect, useState } from "react";
import type { ConfigApi } from "./useConfig";
import type { LatLon } from "./geo";
import { Popover } from "./Popover";
import type { Antenna } from "./bands";
import { getMeta, type MetaDataset } from "./api";
import { TranscribeSettings } from "./TranscribeSettings";

const parse = (s: string): number | null => (s.trim() === "" || !Number.isFinite(Number(s)) ? null : Number(s));

export function SettingsMenu(props: {
  config: ConfigApi;
  open: boolean;
  setOpen: (o: boolean) => void;
  /** Center of the open map view, if any. */
  mapCenter: (() => LatLon) | null;
  antennas: Antenna[];
}) {
  const { config, open, setOpen, mapCenter, antennas } = props;
  const { receiver } = config;
  const [lat, setLat] = useState("");
  const [lon, setLon] = useState("");
  const [alt, setAlt] = useState("");
  const [busy, setBusy] = useState(false);
  const [ids, setIds] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [datasets, setDatasets] = useState<MetaDataset[] | null>(null);

  // Datasets are read when the panel opens (cheap, local).
  useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    getMeta(ac.signal).then(setDatasets).catch(() => undefined);
    return () => ac.abort();
  }, [open]);

  useEffect(() => {
    setLat(receiver ? String(receiver.lat) : "");
    setLon(receiver ? String(receiver.lon) : "");
    setAlt(receiver?.alt_m === undefined ? "" : String(receiver.alt_m));
  }, [receiver]);

  useEffect(() => setIds(config.meterIds.join(", ")), [config.meterIds]);

  const idList = ids.split(/[,\s]+/).filter((x) => x !== "");
  const idsBad = idList.some((x) => !/^\d+$/.test(x));

  const la = parse(lat);
  const lo = parse(lon);
  const al = parse(alt);
  const latBad = lat !== "" && (la === null || la < -90 || la > 90);
  const lonBad = lon !== "" && (lo === null || lo < -180 || lo > 180);
  const altBad = alt !== "" && al === null;
  const valid = la !== null && lo !== null && !latBad && !lonBad && !altBad;

  const run = async (r: Parameters<ConfigApi["save"]>[0] | string[], ok: string) => {
    setBusy(true);
    setErr(null);
    setMsg(null);
    try {
      if (Array.isArray(r)) await config.saveMeterIds(r);
      else await config.save(r);
      setMsg(ok);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const pickAntenna = async (id: string) => {
    setBusy(true);
    setErr(null);
    setMsg(null);
    try {
      await config.saveAntenna(id === "" ? null : id);
      setMsg("antenna saved");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Popover label="⚙" title="Settings" open={open} setOpen={setOpen} width={300}>
      <div className="label">antenna</div>
      <div className="form">
        <select
          style={{ maxWidth: "100%" }}
          value={config.antenna ?? ""}
          disabled={busy || !config.available || antennas.length === 0}
          onChange={(e) => void pickAntenna(e.target.value)}
        >
          <option value="">(not set)</option>
          {config.antenna && !antennas.some((a) => a.id === config.antenna) && <option value={config.antenna}>{config.antenna}</option>}
          {antennas.map((a) => (
            <option key={a.id} value={a.id}>{a.name}</option>
          ))}
        </select>
      </div>
      <div className="dim pad0">Used to show which bands you can receive and to plan the survey.</div>
      <hr />
      <div className="label">receiver location</div>
      {!config.available && <div className="dim pad0">config API not available on this backend</div>}
      <div className="form">
        <label>lat <input className={`freq ${latBad ? "invalid" : ""}`} value={lat} onChange={(e) => setLat(e.target.value)} placeholder="-90..90" /></label>
        <label>lon <input className={`freq ${lonBad ? "invalid" : ""}`} value={lon} onChange={(e) => setLon(e.target.value)} placeholder="-180..180" /></label>
        <label>alt m <input className={`freq ${altBad ? "invalid" : ""}`} value={alt} onChange={(e) => setAlt(e.target.value)} placeholder="optional" /></label>
      </div>
      {(latBad || lonBad || altBad) && (
        <div className="err pad0">
          {latBad && "lat must be -90..90. "}
          {lonBad && "lon must be -180..180. "}
          {altBad && "altitude must be a number."}
        </div>
      )}
      <div className="row">
        <button
          disabled={busy || !valid || !config.available}
          onClick={() => void run(al === null ? { lat: la!, lon: lo! } : { lat: la!, lon: lo!, alt_m: al }, "saved")}
        >
          Save
        </button>
        <button disabled={busy || !config.available} onClick={() => void run(null, "cleared")}>Clear</button>
        {mapCenter && (
          <button
            title="Use the center of the open map"
            onClick={() => {
              const c = mapCenter();
              setLat(c.lat.toFixed(5));
              setLon(c.lon.toFixed(5));
            }}
          >
            use map center
          </button>
        )}
      </div>
      {err && <div className="err pad0">{err}</div>}
      {msg && <div className="conn-open pad0">{msg}</div>}
      <div className="dim pad0">Restart the ADS-B decoder for a changed location to take effect (Stop, then Start).</div>
      <hr />
      <div className="label">my utility meter IDs</div>
      <div className="form">
        <input
          className={`freq ${idsBad ? "invalid" : ""}`}
          style={{ width: "100%", textAlign: "left" }}
          value={ids}
          onChange={(e) => setIds(e.target.value)}
          placeholder="digits only, comma or space separated"
          inputMode="numeric"
        />
      </div>
      {idsBad && <div className="err pad0">meter IDs must be digits only.</div>}
      <div className="row">
        <button disabled={busy || idsBad || !config.available} onClick={() => void run(idList, "meter IDs saved")}>Save IDs</button>
      </div>
      <div className="dim pad0">The ID is printed on your meter; meters you don't own are never shown.</div>
      <hr />
      <TranscribeSettings open={open} />
      <hr />
      <div className="label">datasets</div>
      {datasets === null && <div className="dim pad0">metadata API not available on this backend</div>}
      {datasets?.map((d) => (
        <div key={d.name} className="pad0" title={d.error}>
          <span className={d.loaded ? "conn-open" : "dim"}>{d.loaded ? "●" : "○"}</span> <b>{d.name}</b>{" "}
          {d.loaded ? (
            <span className="dim">
              {d.records.toLocaleString()} records
              {d.fetched ? ` · fetched ${d.fetched.slice(0, 10)}` : ""}
              {d.license || d.attribution ? ` · ${[d.attribution, d.license].filter(Boolean).join(", ")}` : ""}
            </span>
          ) : (
            <span className="dim">not loaded{d.error ? ` (${d.error})` : ""}</span>
          )}
        </div>
      ))}
      {datasets !== null && datasets.some((d) => !d.loaded) && (
        <div className="dim pad0">
          Run <code>make metadata</code> to download airline, aircraft and satellite data (bulk, offline lookups; nothing is looked up per aircraft or satellite).
        </div>
      )}
    </Popover>
  );
}
