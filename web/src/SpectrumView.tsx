import { useEffect, useRef } from "react";
import { LUT32 } from "./colormap";
import { fmtMHz, niceStep, resample } from "./draw";
import type { SpectrumStream } from "./useSpectrum";
import type { Meta } from "./spectrumSocket";

const AXIS_H = 22; // css px
const GRID = "#243040";
const TEXT = "#8aa0b8";
const TRACE = "#4fd1c5";

interface Size {
  w: number; // css px
  h: number;
}

function fitCanvas(c: HTMLCanvasElement): Size {
  const dpr = window.devicePixelRatio || 1;
  const r = c.getBoundingClientRect();
  const w = Math.max(1, Math.round(r.width * dpr));
  const h = Math.max(1, Math.round(r.height * dpr));
  if (c.width !== w || c.height !== h) {
    c.width = w;
    c.height = h;
  }
  return { w: r.width, h: r.height };
}

interface Props {
  stream: SpectrumStream;
  meta: Meta | null;
  onTuneHz: (hz: number) => void; // click-to-tune (already rounded to 1 kHz)
  onWheel: (dir: 1 | -1) => void; // +1 = step up
}

export function SpectrumView({ stream, meta, onTuneHz, onWheel }: Props) {
  const onTuneRef = useRef(onTuneHz);
  const onWheelRef = useRef(onWheel);
  onTuneRef.current = onTuneHz;
  onWheelRef.current = onWheel;
  const specRef = useRef<HTMLCanvasElement>(null);
  const axisRef = useRef<HTMLCanvasElement>(null);
  const wfRef = useRef<HTMLCanvasElement>(null);
  const cursorRef = useRef<HTMLDivElement>(null);
  const tipRef = useRef<HTMLDivElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const metaRef = useRef<Meta | null>(meta);
  const clearWf = useRef<() => void>(() => {});
  const redraw = useRef<() => void>(() => {});

  // Clear the waterfall when the tuning/scale changes.
  useEffect(() => {
    metaRef.current = meta;
    clearWf.current();
    redraw.current();
  }, [meta?.center_hz, meta?.sample_rate, meta?.bins]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const spec = specRef.current!;
    const axis = axisRef.current!;
    const wf = wfRef.current!;
    const wrap = wrapRef.current!;
    const sctx = spec.getContext("2d")!;
    const actx = axis.getContext("2d")!;
    const wctx = wf.getContext("2d", { willReadFrequently: false })!;

    let line = new Float32Array(0);
    let rowU8 = new Uint8Array(0);
    let row: ImageData | null = null;
    let row32: Uint32Array | null = null;

    const drawSpectrum = (frame: Uint8Array | null, m: Meta | null) => {
      const dpr = window.devicePixelRatio || 1;
      const W = spec.width;
      const H = spec.height;
      sctx.clearRect(0, 0, W, H);
      sctx.font = `${10 * dpr}px ui-monospace, Menlo, monospace`;
      sctx.textBaseline = "top";
      if (!m) return;
      // dB grid
      const step = niceStep(m.max_db - m.min_db, 8);
      sctx.lineWidth = 1;
      for (let db = Math.ceil(m.min_db / step) * step; db <= m.max_db; db += step) {
        const y = Math.round(H - ((db - m.min_db) / (m.max_db - m.min_db)) * H) + 0.5;
        sctx.strokeStyle = GRID;
        sctx.beginPath();
        sctx.moveTo(0, y);
        sctx.lineTo(W, y);
        sctx.stroke();
        sctx.fillStyle = TEXT;
        sctx.fillText(`${db.toFixed(0)} dB`, 4 * dpr, y + 2 * dpr);
      }
      // vertical grid aligned with axis ticks
      for (const { x } of freqTicks(m, W).ticks) {
        sctx.strokeStyle = GRID;
        sctx.beginPath();
        sctx.moveTo(Math.round(x) + 0.5, 0);
        sctx.lineTo(Math.round(x) + 0.5, H);
        sctx.stroke();
      }
      if (!frame || frame.length !== m.bins) return;
      if (line.length !== W) line = new Float32Array(W);
      resample(frame, W, line);
      sctx.beginPath();
      for (let x = 0; x < W; x++) {
        const y = H - (line[x]! / 255) * H;
        if (x === 0) sctx.moveTo(x, y);
        else sctx.lineTo(x, y);
      }
      sctx.strokeStyle = TRACE;
      sctx.lineWidth = Math.max(1, dpr);
      sctx.stroke();
      sctx.lineTo(W, H);
      sctx.lineTo(0, H);
      sctx.closePath();
      sctx.fillStyle = "rgba(79,209,197,0.15)";
      sctx.fill();
    };

    const freqTicks = (m: Meta, W: number) => {
      const lo = m.center_hz - m.sample_rate / 2;
      const step = niceStep(m.sample_rate, Math.max(2, W / (90 * (window.devicePixelRatio || 1))));
      const out: { x: number; hz: number }[] = [];
      for (let f = Math.ceil(lo / step) * step; f <= lo + m.sample_rate; f += step) {
        out.push({ x: ((f - lo) / m.sample_rate) * W, hz: f });
      }
      return { ticks: out, step };
    };

    const drawAxis = (m: Meta | null) => {
      const dpr = window.devicePixelRatio || 1;
      const W = axis.width;
      const H = axis.height;
      actx.clearRect(0, 0, W, H);
      if (!m) return;
      const { ticks, step } = freqTicks(m, W);
      const decimals = Math.min(6, Math.max(0, Math.ceil(-Math.log10(step / 1e6))));
      actx.font = `${10 * dpr}px ui-monospace, Menlo, monospace`;
      actx.textBaseline = "top";
      actx.textAlign = "center";
      actx.strokeStyle = TEXT;
      actx.fillStyle = TEXT;
      actx.lineWidth = 1;
      for (const t of ticks) {
        const x = Math.round(t.x) + 0.5;
        actx.beginPath();
        actx.moveTo(x, 0);
        actx.lineTo(x, 4 * dpr);
        actx.stroke();
        const label = fmtMHz(t.hz, decimals);
        const tw = actx.measureText(label).width / 2;
        actx.fillText(label, Math.min(W - tw, Math.max(tw, x)), 6 * dpr);
      }
    };

    const wfClear = () => {
      wctx.fillStyle = "#000";
      wctx.fillRect(0, 0, wf.width, wf.height);
    };
    clearWf.current = wfClear;

    const pushWaterfall = (frame: Uint8Array) => {
      const W = wf.width;
      const H = wf.height;
      // one CSS pixel per frame, so the waterfall scrolls at the same speed on retina screens
      const rh = Math.max(1, Math.round(window.devicePixelRatio || 1));
      if (!row || row.width !== W || row.height !== rh) {
        row = wctx.createImageData(W, rh);
        row32 = new Uint32Array(row.data.buffer);
        rowU8 = new Uint8Array(W);
      }
      resample(frame, W, rowU8);
      for (let x = 0; x < W; x++) row32![x] = LUT32[rowU8[x]!]!;
      for (let y = 1; y < rh; y++) row32!.copyWithin(y * W, 0, W);
      wctx.drawImage(wf, 0, 0, W, H - rh, 0, rh, W, H - rh);
      wctx.putImageData(row, 0, 0);
    };

    const resize = () => {
      fitCanvas(spec);
      fitCanvas(axis);
      const before = wf.width * 10000 + wf.height;
      fitCanvas(wf);
      if (wf.width * 10000 + wf.height !== before) wfClear(); // canvas resize already blanks it
      drawAxis(metaRef.current);
      drawSpectrum(stream.latest, metaRef.current);
    };

    redraw.current = () => {
      drawAxis(metaRef.current);
      drawSpectrum(stream.latest, metaRef.current);
    };

    const onFrame = (frame: Uint8Array, m: Meta) => {
      metaRef.current = m;
      drawSpectrum(frame, m);
      pushWaterfall(frame);
    };

    stream.listeners.add(onFrame);
    const ro = new ResizeObserver(resize);
    ro.observe(spec);
    ro.observe(axis);
    ro.observe(wf);
    resize();
    wfClear();

    // Hover cursor
    const cursor = cursorRef.current!;
    const tip = tipRef.current!;
    const onMove = (ev: MouseEvent) => {
      const m = metaRef.current;
      const wr = wrap.getBoundingClientRect();
      const sr = spec.getBoundingClientRect();
      const x = ev.clientX - sr.left;
      if (!m || x < 0 || x > sr.width) return onLeave();
      const hz = m.center_hz - m.sample_rate / 2 + (x / sr.width) * m.sample_rate;
      let db: number | null = null;
      const frame = stream.latest;
      const over = ev.currentTarget as HTMLCanvasElement;
      if (over === spec) {
        const y = ev.clientY - sr.top;
        db = m.max_db - (y / sr.height) * (m.max_db - m.min_db);
      } else if (frame && frame.length === m.bins) {
        const i = Math.min(m.bins - 1, Math.max(0, Math.floor((x / sr.width) * m.bins)));
        db = m.min_db + (frame[i]! * (m.max_db - m.min_db)) / 255;
      }
      cursor.style.display = "block";
      cursor.style.left = `${sr.left - wr.left + x}px`;
      tip.style.display = "block";
      tip.textContent = `${fmtMHz(hz, 4)} MHz` + (db === null ? "" : `  ${db.toFixed(1)} dB`);
      const flip = x > sr.width - 170;
      tip.style.left = `${sr.left - wr.left + x + (flip ? -tip.offsetWidth - 8 : 10)}px`;
      tip.style.top = `${ev.clientY - wr.top + 12}px`;
    };
    const onLeave = () => {
      cursor.style.display = "none";
      tip.style.display = "none";
    };
    const targets = [spec, axis, wf];
    let down: { x: number; y: number } | null = null;
    const onDown = (ev: MouseEvent) => {
      down = { x: ev.clientX, y: ev.clientY };
    };
    const onClick = (ev: MouseEvent) => {
      const m = metaRef.current;
      const d = down;
      down = null;
      if (!m || !d || Math.hypot(ev.clientX - d.x, ev.clientY - d.y) > 4) return;
      const sr = spec.getBoundingClientRect();
      const x = Math.min(sr.width, Math.max(0, ev.clientX - sr.left));
      const hz = m.center_hz - m.sample_rate / 2 + (x / sr.width) * m.sample_rate;
      onTuneRef.current(Math.round(hz / 1000) * 1000);
    };
    const onWheelEv = (ev: WheelEvent) => {
      ev.preventDefault();
      if (ev.deltaY !== 0) onWheelRef.current(ev.deltaY < 0 ? 1 : -1);
    };
    for (const c of [spec, wf]) {
      c.addEventListener("mousedown", onDown);
      c.addEventListener("click", onClick);
      c.addEventListener("wheel", onWheelEv, { passive: false });
    }
    for (const c of targets) {
      c.addEventListener("mousemove", onMove);
      c.addEventListener("mouseleave", onLeave);
    }

    return () => {
      stream.listeners.delete(onFrame);
      ro.disconnect();
      for (const c of targets) {
        c.removeEventListener("mousemove", onMove);
        c.removeEventListener("mouseleave", onLeave);
      }
      for (const c of [spec, wf]) {
        c.removeEventListener("mousedown", onDown);
        c.removeEventListener("click", onClick);
        c.removeEventListener("wheel", onWheelEv);
      }
    };
  }, [stream]);

  return (
    <div className="view" ref={wrapRef}>
      <canvas ref={specRef} className="spec" />
      <canvas ref={axisRef} className="axis" style={{ height: AXIS_H }} />
      <canvas ref={wfRef} className="wf" />
      <div ref={cursorRef} className="cursor" />
      <div ref={tipRef} className="tip" />
    </div>
  );
}
