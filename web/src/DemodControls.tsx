import { useEffect, useRef, useState } from "react";
import type { DemodMode, Status, Transcript } from "./api";
import { TranscribeControl } from "./TranscribeControl";
import type { AudioApi } from "./useAudio";
import type { DemodApi } from "./useDemod";
import { roundVfo } from "./SpectrumView";

const MODES: DemodMode[] = ["off", "nfm", "am", "usb", "lsb"];
const SQ_MIN = -100;
const SQ_MAX = -20;

export function DemodControls({ status, demod, audio, locked = false, transcripts }: { status: Status | null; demod: DemodApi; audio: AudioApi; locked?: boolean; transcripts: Transcript[] }) {
  const d = status?.demod;
  const mode = d?.mode ?? "off";
  const sqDb = d?.squelch_db ?? null;

  const [bwText, setBwText] = useState("");
  const [bwFocus, setBwFocus] = useState(false);
  useEffect(() => {
    if (!bwFocus && d) setBwText(String(d.bandwidth_hz / 1000));
  }, [d?.bandwidth_hz, bwFocus]); // eslint-disable-line react-hooks/exhaustive-deps

  const [sq, setSq] = useState(-60);
  const sqDrag = useRef(false);
  useEffect(() => {
    if (!sqDrag.current && sqDb !== null) setSq(sqDb);
  }, [sqDb]);

  const changeMode = (m: DemodMode) => {
    if (m === "off") return demod.send({ mode: "off" }, true);
    // From "off" there may be no sensible VFO frequency: park it a quarter span above center
    // (the same offset sdrd uses to keep the VFO off the DC spike).
    let f = d?.freq_hz;
    if (!f && status) f = roundVfo(status.center_hz + status.sample_rate / 4, m);
    demod.send(f ? { mode: m, freq_hz: f } : { mode: m }, true);
  };

  const commitBw = () => {
    setBwFocus(false);
    const khz = parseFloat(bwText);
    if (Number.isFinite(khz) && khz > 0) demod.send({ bandwidth_hz: Math.round(khz * 1000) }, true);
    else if (d) setBwText(String(d.bandwidth_hz / 1000));
  };

  // Level meter: updated by direct DOM writes from the audio socket, no React renders.
  const fillRef = useRef<HTMLDivElement>(null);
  const sqlRef = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const upd = (db: number, open: boolean) => {
      const f = fillRef.current;
      const t = sqlRef.current;
      if (f) {
        f.style.width = `${Math.min(100, Math.max(0, ((db + 100) / 80) * 100))}%`;
        f.className = open ? "fill open" : "fill";
      }
      if (t) {
        t.textContent = audio.on ? (open ? `open ${db.toFixed(0)} dB` : `closed ${db.toFixed(0)} dB`) : "";
        t.className = open ? "conn-open" : "dim";
      }
    };
    audio.level.listeners.add(upd);
    upd(audio.level.db, audio.level.open);
    return () => void audio.level.listeners.delete(upd);
  }, [audio.level, audio.on]);

  return (
    <>
      <span className="sep" />
      <span className="item">
        <span className="label">demod</span>{" "}
        <select value={mode} disabled={!status || locked} onChange={(e) => changeMode(e.target.value as DemodMode)}>
          {MODES.map((m) => (
            <option key={m} value={m}>{m === "off" ? "off" : m.toUpperCase()}</option>
          ))}
        </select>
      </span>
      <span className="item">
        <span className="label">bw kHz</span>{" "}
        <input
          className="freq narrow"
          value={bwText}
          disabled={mode === "off" || locked}
          spellCheck={false}
          onFocus={() => setBwFocus(true)}
          onBlur={commitBw}
          onChange={(e) => setBwText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") (e.target as HTMLInputElement).blur();
          }}
        />
      </span>
      <span className="item">
        <span className="label">squelch</span>{" "}
        <input
          type="range"
          min={SQ_MIN}
          max={SQ_MAX}
          step={1}
          value={sq}
          disabled={mode === "off" || sqDb === null || locked}
          onPointerDown={() => (sqDrag.current = true)}
          onPointerUp={() => (sqDrag.current = false)}
          onChange={(e) => {
            const v = Number(e.target.value);
            setSq(v);
            demod.send({ squelch_db: v });
          }}
        />{" "}
        <label className="chk">
          <input
            type="checkbox"
            checked={sqDb === null}
            disabled={mode === "off" || locked}
            onChange={(e) => demod.send({ squelch_db: e.target.checked ? null : sq }, true)}
          />
          off
        </label>{" "}
        <span className="dim">{sqDb === null ? "" : `${sq} dB`}</span>
      </span>
      <span className="item">
        <button className={audio.on ? "on" : ""} onClick={audio.toggle} title="Audio needs a click to start (browser policy)">
          audio {audio.on ? "on" : "off"}
        </button>{" "}
        {audio.on && silentReason(status) && <span className="warn-inline">{silentReason(status)}</span>}{" "}
        <span className="label">vol</span>{" "}
        <input
          type="range"
          min={0}
          max={1}
          step={0.01}
          value={audio.volume}
          onChange={(e) => audio.setVolume(Number(e.target.value))}
        />
      </span>
      <TranscribeControl enabled={mode !== "off"} transcripts={transcripts} />
      <span className="item">
        <span className="meter"><div ref={fillRef} className="fill" /></span> <span ref={sqlRef} className="dim" />
        {audio.on && audio.conn !== "open" && <span className="conn-connecting"> audio link {audio.conn}</span>}
      </span>
      {demod.error && <span className="err">{demod.error}</span>}
    </>
  );
}

/** Why audio is on but nothing will play, if there's an obvious reason. */
function silentReason(status: Status | null): string {
  if (!status) return "";
  const dec = status.decoder;
  if (dec?.running && dec.name === "dsd") return "digital voice only: silent until a DMR/D-STAR/YSF call (Listen to switch back)";
  if ((status.demod?.mode ?? "off") === "off") return "demod off: pick a mode or click Listen on a channel";
  return "";
}
