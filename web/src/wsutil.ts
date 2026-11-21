// Reconnecting WebSocket helper (exponential backoff), shared by audio and events sockets.
import type { ConnState } from "./spectrumSocket";

export interface WsHandlers {
  onOpen?(): void;
  onText?(text: string): void;
  onBinary?(buf: ArrayBuffer): void;
  onState?(s: ConnState): void;
}

export function connectWs(path: string, h: WsHandlers): () => void {
  let ws: WebSocket | null = null;
  let stopped = false;
  let attempt = 0;
  let timer: number | undefined;

  const open = () => {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    h.onState?.("connecting");
    const sock = new WebSocket(`${proto}//${location.host}${path}`);
    ws = sock;
    sock.binaryType = "arraybuffer";
    sock.onopen = () => {
      attempt = 0;
      h.onState?.("open");
      h.onOpen?.();
    };
    sock.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data === "string") h.onText?.(ev.data);
      else if (ev.data instanceof ArrayBuffer) h.onBinary?.(ev.data);
    };
    sock.onclose = () => {
      if (ws !== sock) return;
      ws = null;
      h.onState?.("closed");
      if (stopped) return;
      timer = window.setTimeout(open, Math.min(5000, 250 * 2 ** attempt++));
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
