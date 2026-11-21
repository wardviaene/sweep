// WebSocket client for /ws/spectrum. Frames are delivered through callbacks,
// never through React state, so 20+ fps does not cause re-renders.

export interface Meta {
  type: "meta";
  center_hz: number;
  sample_rate: number;
  bins: number;
  min_db: number;
  max_db: number;
  fps: number;
}

export type ConnState = "connecting" | "open" | "closed";

export interface SpectrumHandlers {
  onMeta(meta: Meta): void;
  onFrame(frame: Uint8Array, meta: Meta): void;
  onState(state: ConnState): void;
}

export function connectSpectrum(h: SpectrumHandlers): () => void {
  let ws: WebSocket | null = null;
  let meta: Meta | null = null;
  let stopped = false;
  let attempt = 0;
  let timer: number | undefined;

  const open = () => {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    h.onState("connecting");
    const sock = new WebSocket(`${proto}//${location.host}/ws/spectrum`);
    ws = sock;
    sock.binaryType = "arraybuffer";
    sock.onopen = () => {
      attempt = 0;
      meta = null; // wait for a fresh meta before accepting frames
      h.onState("open");
    };
    sock.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data === "string") {
        try {
          const m = JSON.parse(ev.data) as Meta;
          if (m.type === "meta") {
            meta = m;
            h.onMeta(m);
          }
        } catch {
          /* ignore malformed text frame */
        }
        return;
      }
      if (!meta || !(ev.data instanceof ArrayBuffer)) return; // drop until meta
      if (ev.data.byteLength !== meta.bins) return; // stale/mismatched frame
      h.onFrame(new Uint8Array(ev.data), meta);
    };
    sock.onclose = () => {
      if (ws !== sock) return;
      ws = null;
      h.onState("closed");
      if (stopped) return;
      const delay = Math.min(5000, 250 * 2 ** attempt++);
      timer = window.setTimeout(open, delay);
    };
    sock.onerror = () => sock.close();
  };

  open();

  return () => {
    stopped = true;
    window.clearTimeout(timer);
    const s = ws;
    ws = null;
    s?.close();
  };
}
