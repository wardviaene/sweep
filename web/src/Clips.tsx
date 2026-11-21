import { useEffect, useState } from "react";
import type { Transcript } from "./api";

// One shared <audio> element: starting a clip stops the previous one.
let el: HTMLAudioElement | null = null;
let current = "";
const subs = new Set<(url: string) => void>();
const announce = (u: string) => {
  current = u;
  subs.forEach((f) => f(u));
};

function toggleClip(url: string) {
  if (!el) {
    el = new Audio();
    el.addEventListener("ended", () => announce(""));
    el.addEventListener("error", () => announce(""));
  }
  if (current === url) {
    el.pause();
    announce("");
    return;
  }
  el.src = url;
  announce(url);
  el.play().catch(() => announce(""));
}

/** ▶ / ■ button replaying a clip through a shared <audio> element. */
export function PlayBtn({ url }: { url: string }) {
  const [now, setNow] = useState(current);
  useEffect(() => {
    subs.add(setNow);
    setNow(current);
    return () => void subs.delete(setNow);
  }, []);
  if (!url) return null;
  const on = now === url;
  return (
    <button className={`mini ${on ? "on" : ""}`} title={on ? "Stop" : "Replay the clip"} onClick={() => toggleClip(url)}>
      {on ? "■" : "▶"}
    </button>
  );
}

/** Transcript text in a quoted line, "(no speech)" when silent, "transcribing…" while pending. */
export function TranscriptLine({ t, pending }: { t?: Transcript; pending?: boolean }) {
  if (!t) return pending ? <span className="tr-pend">transcribing…</span> : null;
  return (
    <span className="tr-line">
      {t.empty ? <span className="dim">(no speech)</span> : <q>{t.text}</q>}
      <PlayBtn url={t.url} />
    </span>
  );
}
