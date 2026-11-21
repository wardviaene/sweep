import { useEffect, useState } from "react";
import type { SstvImage, SstvProgress, SstvView as View } from "./useSstv";

const time = (ts: number) => new Date(ts * 1000).toLocaleTimeString([], { hour12: false });
const STALE_S = 20; // a progress card disappears when no progress arrives for this long

/** The partially decoded PNG is rewritten server side; re-point the <img> at most once per second. */
function LiveImage({ p }: { p: SstvProgress }) {
  const [src, setSrc] = useState(p.url);
  useEffect(() => {
    const id = window.setInterval(() => setSrc(`${p.url}${p.url.includes("?") ? "&" : "?"}t=${Date.now()}`), 1000);
    setSrc(`${p.url}${p.url.includes("?") ? "&" : "?"}t=${Date.now()}`);
    return () => window.clearInterval(id);
  }, [p.url]);
  return <img className="sstv-live" src={src} alt="image being received" />;
}

export function SstvView({ view }: { view: View }) {
  const [open, setOpen] = useState<SstvImage | null>(null);
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now() / 1000), 2000);
    return () => window.clearInterval(id);
  }, []);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(null);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  const p = view.progress && now - view.progress.ts < STALE_S ? view.progress : null;
  return (
    <div className="sstv">
      {p && (
        <div className="sstv-rx">
          <LiveImage p={p} />
          <div>
            <div><span className="badge okb">receiving</span> {p.mode}</div>
            <div className="dim">{p.lines} / {p.total || "?"} lines</div>
            <div className="apbar"><div style={{ width: `${p.total ? Math.min(100, (100 * p.lines) / p.total) : 0}%` }} /></div>
          </div>
        </div>
      )}
      {!view.images.length && !p && (
        <div className="dim pad">no pictures yet. The ISS sends SSTV on 145.800 MHz during events; the autopilot can catch it for you.</div>
      )}
      <div className="sstv-grid">
        {view.images.map((im) => (
          <figure key={im.id} className="sstv-item">
            <img src={im.url} loading="lazy" alt={`${im.mode} ${time(im.ts)}`} onClick={() => setOpen(im)} />
            <figcaption>
              {time(im.ts)} · {im.mode}{im.width ? ` ${im.width}x${im.height}` : ""}
              {im.satellite && <> · <span className="lbl">{im.satellite}</span></>}{" "}
              <a href={im.url} download>download</a>
            </figcaption>
          </figure>
        ))}
      </div>
      {open && (
        <div className="lightbox" onClick={() => setOpen(null)}>
          <img src={open.url} alt="" onClick={(e) => e.stopPropagation()} />
          <div className="lightbox-bar" onClick={(e) => e.stopPropagation()}>
            {time(open.ts)} · {open.mode}{open.satellite && ` · ${open.satellite}`}{" "}
            <a href={open.url} download>download</a> <button className="mini" onClick={() => setOpen(null)}>close</button>
          </div>
        </div>
      )}
    </div>
  );
}
