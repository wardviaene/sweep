import { useEffect, useRef, useState } from "react";
import { connectSpectrum, type ConnState, type Meta } from "./spectrumSocket";

export type FrameListener = (frame: Uint8Array, meta: Meta) => void;

/** Mutable, non-reactive handle. Frames never touch React state. */
export interface SpectrumStream {
  latest: Uint8Array | null;
  meta: Meta | null;
  /** Total frames received; sample it once per second for fps. */
  count: number;
  listeners: Set<FrameListener>;
}

export interface SpectrumHook {
  stream: SpectrumStream;
  /** Re-renders only on connect/disconnect and meta changes. */
  conn: ConnState;
  meta: Meta | null;
  /** Measured frames per second, updated once per second. */
  fps: number;
}

export function useSpectrum(): SpectrumHook {
  const streamRef = useRef<SpectrumStream>({ latest: null, meta: null, count: 0, listeners: new Set() });
  const [conn, setConn] = useState<ConnState>("connecting");
  const [meta, setMeta] = useState<Meta | null>(null);
  const [fps, setFps] = useState(0);

  useEffect(() => {
    const s = streamRef.current;
    const stop = connectSpectrum({
      onState: (st) => {
        setConn(st);
        if (st !== "open") setFps(0);
      },
      onMeta: (m) => {
        s.meta = m;
        setMeta(m);
      },
      onFrame: (frame, m) => {
        s.latest = frame;
        s.meta = m;
        s.count++;
        s.listeners.forEach((l) => l(frame, m));
      },
    });

    let last = s.count;
    let lastT = performance.now();
    const t = window.setInterval(() => {
      const now = performance.now();
      setFps(((s.count - last) * 1000) / (now - lastT));
      last = s.count;
      lastT = now;
    }, 1000);

    return () => {
      stop();
      window.clearInterval(t);
    };
  }, []);

  return { stream: streamRef.current, conn, meta, fps };
}
