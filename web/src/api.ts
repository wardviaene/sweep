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

// --- scan / record ---

export interface ScanPeak {
  freq_hz: number;
  db: number;
  snr_db: number;
  spur: boolean;
}

export interface ScanResult {
  start_hz: number;
  stop_hz: number;
  duration_s: number;
  steps: { center_hz: number; floor_db: number }[];
  peaks: ScanPeak[];
}

export interface ScanParams {
  start_hz: number;
  stop_hz: number;
  dwell_ms: number;
  min_snr: number;
  max?: number;
}

/** Blocks for the whole scan (seconds). */
export function scan(p: ScanParams, signal?: AbortSignal): Promise<ScanResult> {
  return request<ScanResult>("/api/scan", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(p),
    signal,
  });
}

export interface RecordResult {
  path: string;
  bytes: number;
  seconds: number;
  center_hz: number;
  sample_rate: number;
}

/** Blocks while recording. */
export function record(seconds: number): Promise<RecordResult> {
  return post<RecordResult>("/api/record", { seconds });
}

// --- config ---

export interface Receiver {
  lat: number;
  lon: number;
  alt_m?: number;
}

export interface Config {
  receiver: Receiver | null;
}

/** Resolves null when the backend has no /api/config (404). */
export async function getConfig(signal?: AbortSignal): Promise<Config | null> {
  const res = await fetch("/api/config", { signal });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`/api/config: ${res.status} ${await res.text()}`);
  const c = (await res.json()) as Partial<Config>;
  return { receiver: c.receiver ?? null };
}

export async function putConfig(receiver: Receiver | null): Promise<Config> {
  const res = await fetch("/api/config", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ receiver }),
  });
  const text = await res.text();
  if (!res.ok) {
    let msg = text;
    try {
      const j = JSON.parse(text) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* not JSON */
    }
    throw new Error(msg || `status ${res.status}`);
  }
  const c = JSON.parse(text) as Partial<Config>;
  return { receiver: c.receiver ?? null };
}
