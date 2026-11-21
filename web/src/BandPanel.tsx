import { useMemo, useState } from "react";
import { bandChannels, bandRange, type Band, type Catalog, type Channel } from "./bands";
import type { DecoderName, DecoderOpts } from "./api";

type Filter = "all" | "stock" | "hf";

interface Props {
  catalog: Catalog | null;
  loadError: boolean;
  region: string;
  setRegion: (r: string) => void;
  centerHz: number | undefined;
  onJump: (b: Band) => void;
  onChannel: (b: Band, c: Channel) => void;
  onDecoder: (d: DecoderName, opts?: DecoderOpts) => void;
  error: string | null;
}

const FREQ_DECODERS = new Set<DecoderName>(["packet", "dsd", "radiosonde"]);
const optsFor = (d: DecoderName, mhz: number): DecoderOpts | undefined =>
  FREQ_DECODERS.has(d) ? { freq_hz: Math.round(mhz * 1e6) } : undefined;

const fmt = (mhz: number) => String(+mhz.toFixed(6));

export function BandPanel(p: Props) {
  const [open, setOpen] = useState(true);
  const [filter, setFilter] = useState<Filter>("all");
  const [sel, setSel] = useState<string | null>(null);
  const { catalog, region } = p;

  const bands = useMemo(() => {
    if (!catalog) return [];
    return catalog.bands.filter(
      (b) => b.regions.includes(region) && (filter === "all" || (filter === "hf") === b.hf),
    );
  }, [catalog, region, filter]);

  if (!open) {
    return (
      <aside className="bands collapsed">
        <button onClick={() => setOpen(true)} title="Show bands">»</button>
        <span className="vtext">bands</span>
      </aside>
    );
  }

  const cmhz = p.centerHz === undefined ? undefined : p.centerHz / 1e6;
  const regions = catalog ? Object.entries(catalog.regions) : [];

  return (
    <aside className="bands">
      <div className="bands-head">
        <span className="label">bands</span>
        <span className="seg">
          {regions.map(([id]) => (
            <button key={id} className={id === region ? "on" : ""} onClick={() => p.setRegion(id)} title={catalog?.regions[id]}>
              {id.toUpperCase()}
            </button>
          ))}
        </span>
        <span className="seg">
          {(["all", "stock", "hf"] as const).map((f) => (
            <button key={f} className={f === filter ? "on" : ""} onClick={() => setFilter(f)}>
              {f === "hf" ? "HF only" : f}
            </button>
          ))}
        </span>
        <button onClick={() => setOpen(false)} title="Hide bands">«</button>
      </div>
      <div className="bands-list">
        {!catalog && <div className="dim pad">{p.loadError ? "band catalog unavailable, retrying..." : "loading..."}</div>}
        {bands.map((b) => {
          const [lo, hi] = bandRange(b, region);
          const here = cmhz !== undefined && cmhz >= lo && cmhz <= hi;
          const expanded = sel === b.id;
          return (
            <div key={b.id} className={`band ${here ? "here" : ""}`}>
              <div className="band-row" onClick={() => setSel(expanded ? null : b.id)}>
                <span className="band-name">{b.name}</span>
                <span className="dim">{fmt(lo)}-{fmt(hi)} MHz</span>
                {b.hf && <span className="badge">HF · direct sampling</span>}
              </div>
              {expanded && <Detail band={b} {...p} />}
            </div>
          );
        })}
        {catalog && bands.length === 0 && <div className="dim pad">no bands for this filter</div>}
      </div>
      {p.error && <div className="err pad">{p.error}</div>}
    </aside>
  );
}

function Detail({ band: b, region, onJump, onChannel, onDecoder }: Props & { band: Band }) {
  const [copied, setCopied] = useState(false);
  const chans = bandChannels(b, region);
  const copy = () => {
    try {
      void navigator.clipboard.writeText(b.starter).then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      });
    } catch {
      /* clipboard unavailable */
    }
  };
  return (
    <div className="detail">
      <div className="btns">
        <button onClick={() => onJump(b)} title={`Tune to ${fmt(b.tune_mhz)} MHz`}>Jump</button>
        {b.decoder && <button onClick={() => onDecoder(b.decoder!, optsFor(b.decoder!, b.tune_mhz))}>Start {b.decoder}</button>}
      </div>
      <Field k="on air" v={b.on_air} />
      <Field k="decode with" v={b.decode_with.join(", ")} />
      <Field k="antenna" v={b.antenna} />
      <Field k="tx license" v={b.tx_license} />
      <div className="field">
        <span className="label">starter</span> <button className="mini" onClick={copy}>{copied ? "copied" : "copy"}</button>
        <pre className="starter">{b.starter}</pre>
      </div>
      {chans.length > 0 && (
        <div className="field">
          <span className="label">channels</span>
          {chans.map((c, i) => (
            <div key={i} className="chan" onClick={() => onChannel(b, c)} title={c.mode === "none" ? "Tune center" : `Listen (${c.mode})`}>
              <span className="mhz">{fmt(c.mhz)}</span>
              <span className="cname">{c.name}</span>
              <span className="dim">{c.mode === "none" ? "" : c.mode}</span>
              {c.decoder && (
                <button
                  className="mini"
                  onClick={(e) => {
                    e.stopPropagation();
                    onDecoder(c.decoder!, optsFor(c.decoder!, c.mhz));
                  }}
                >
                  decode
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Field({ k, v }: { k: string; v: string }) {
  return (
    <div className="field">
      <span className="label">{k}</span>
      <div>{v}</div>
    </div>
  );
}
