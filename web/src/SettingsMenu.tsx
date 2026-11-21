import { useEffect, useState } from "react";
import type { ConfigApi } from "./useConfig";
import type { LatLon } from "./geo";
import { Popover } from "./Popover";

const parse = (s: string): number | null => (s.trim() === "" || !Number.isFinite(Number(s)) ? null : Number(s));

export function SettingsMenu(props: {
  config: ConfigApi;
  open: boolean;
  setOpen: (o: boolean) => void;
  /** Center of the open map view, if any. */
  mapCenter: (() => LatLon) | null;
}) {
  const { config, open, setOpen, mapCenter } = props;
  const { receiver } = config;
  const [lat, setLat] = useState("");
  const [lon, setLon] = useState("");
  const [alt, setAlt] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  useEffect(() => {
    setLat(receiver ? String(receiver.lat) : "");
    setLon(receiver ? String(receiver.lon) : "");
    setAlt(receiver?.alt_m === undefined ? "" : String(receiver.alt_m));
  }, [receiver]);

  const la = parse(lat);
  const lo = parse(lon);
  const al = parse(alt);
  const latBad = lat !== "" && (la === null || la < -90 || la > 90);
  const lonBad = lon !== "" && (lo === null || lo < -180 || lo > 180);
  const altBad = alt !== "" && al === null;
  const valid = la !== null && lo !== null && !latBad && !lonBad && !altBad;

  const run = async (r: Parameters<ConfigApi["save"]>[0], ok: string) => {
    setBusy(true);
    setErr(null);
    setMsg(null);
    try {
      await config.save(r);
      setMsg(ok);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Popover label="⚙" title="Settings" open={open} setOpen={setOpen} width={300}>
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
    </Popover>
  );
}
