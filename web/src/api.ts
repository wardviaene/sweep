// Typed client for the sdrd REST API (see docs/protocol.md).

export interface Status {
  device: string;
  tuner: string;
  gains_db: number[];
  center_hz: number;
  sample_rate: number;
  gain_db: number | null; // null = auto gain
  fft_size: number;
  fps: number;
  mode: string;
  connected: boolean;
  demod?: Demod; // absent on older backends
  decoder?: DecoderStatus;
}

export type DemodMode = "off" | "nfm" | "am" | "usb" | "lsb";

export interface Demod {
  mode: DemodMode;
  freq_hz: number;
  bandwidth_hz: number;
  squelch_db: number | null;
  audio_rate: number;
  level_db: number;
}

export interface DemodParams {
  mode?: DemodMode;
  freq_hz?: number;
  bandwidth_hz?: number;
  squelch_db?: number | null;
}

export type DecoderName = "rtl_433" | "adsb" | "aprs" | "ais";

export interface DecoderStatus {
  name: DecoderName | "none";
  running: boolean;
  freq_hz: number;
  started: number;
  events: number;
  error: string;
}

export interface TuneParams {
  freq_hz?: number;
  sample_rate?: number;
  gain_db?: number | null; // null = auto
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  if (!res.ok) {
    const text = await res.text();
    let msg = text;
    try {
      const j = JSON.parse(text) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* not JSON */
    }
    throw new Error(init?.method === "POST" ? msg : `${path}: ${res.status} ${msg}`);
  }
  return (await res.json()) as T;
}

export function getStatus(signal?: AbortSignal): Promise<Status> {
  return request<Status>("/api/status", { signal });
}

export function tune(params: TuneParams): Promise<Status> {
  return request<Status>("/api/tune", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(params),
  });
}

function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export function setDemod(params: DemodParams): Promise<Status> {
  return post<Status>("/api/demod", params);
}

export function setDecoder(decoder: DecoderName | "none", region?: string): Promise<Status> {
  return post<Status>("/api/decoder", region ? { decoder, region } : { decoder });
}

export function getBands(signal?: AbortSignal): Promise<import("./bands").Catalog> {
  return request("/api/bands", { signal });
}
