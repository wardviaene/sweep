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

export type DecoderName =
  | "rtl_433" | "meters" | "adsb" | "uat" | "aprs" | "ais" | "packet" | "radiosonde" | "dsd";

/** Per-decoder start options (packet, dsd, radiosonde). */
export interface DecoderOpts {
  freq_hz?: number;
  baud?: number;
}

export interface DecoderStatus {
  name: DecoderName | "none";
  running: boolean;
  freq_hz: number;
  started: number;
  events: number;
  error: string;
  baud?: number; // packet only, when the backend reports it
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

export function setDecoder(decoder: DecoderName | "none", region?: string, opts?: DecoderOpts): Promise<Status> {
  const body: Record<string, unknown> = { decoder };
  if (region) body.region = region;
  if (opts?.freq_hz !== undefined) body.freq_hz = opts.freq_hz;
  if (opts?.baud !== undefined) body.baud = opts.baud;
  return post<Status>("/api/decoder", body);
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
  /** catalog channel or visible satellite downlink near this carrier */
  label?: string;
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
  meter_ids: string[];
  /** antenna id from the catalog; null = unknown */
  antenna: string | null;
}

/** PUT merges: send only the fields being changed. */
export interface ConfigPatch {
  receiver?: Receiver | null;
  meter_ids?: string[];
  antenna?: string | null;
}

type RawConfig = { receiver?: Receiver | null; meter_ids?: unknown; antenna?: unknown };

function toConfig(c: RawConfig): Config {
  const ids = Array.isArray(c.meter_ids) ? c.meter_ids.filter((x): x is string => typeof x === "string") : [];
  return { receiver: c.receiver ?? null, meter_ids: ids, antenna: typeof c.antenna === "string" && c.antenna !== "" ? c.antenna : null };
}

/** Resolves null when the backend has no /api/config (404). */
export async function getConfig(signal?: AbortSignal): Promise<Config | null> {
  const res = await fetch("/api/config", { signal });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`/api/config: ${res.status} ${await res.text()}`);
  return toConfig((await res.json()) as RawConfig);
}

export async function putConfig(patch: ConfigPatch): Promise<Config> {
  const res = await fetch("/api/config", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
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
  return toConfig(JSON.parse(text) as RawConfig);
}

// --- survey ("what's on the air?") ---

export interface SurveyPeak {
  freq_hz: number;
  db: number;
  snr_db: number;
  label?: string;
}

export type SurveyState = "idle" | "running" | "done" | "stopped" | "error";
export type SurveyStepState = "pending" | "running" | "done" | "skipped" | "error";

export interface SurveyStep {
  id: string;
  kind: "scan" | "decoder";
  decoder?: DecoderName;
  band?: string;
  label?: string;
  freq_hz?: number;
  freq_lo_hz?: number;
  freq_hi_hz?: number;
  state: SurveyStepState;
  seconds?: number;
  found?: number;
  summary?: string;
  peaks?: SurveyPeak[];
  samples?: Record<string, unknown>[];
  error?: string;
  reason?: string;
  listened?: SurveyListened;
}

/** What a scan step played after its scan (listen hold). */
export interface SurveyListened {
  freq_hz: number;
  mode: string;
  seconds: number;
  level_db: number;
  label?: string;
}

/** Set while a listen hold is active. */
export interface SurveyListening {
  step: string;
  freq_hz: number;
  mode: string;
  label?: string;
  seconds: number;
  started: number;
}

export interface Survey {
  id: number;
  state: SurveyState;
  antenna?: string;
  region?: string;
  started?: number;
  elapsed_s: number;
  estimated_s: number;
  steps: SurveyStep[];
  listen?: boolean;
  listen_seconds?: number;
  listening?: SurveyListening;
}

export interface SurveyParams {
  antenna?: string;
  region?: string;
  seconds?: number;
  include_fair?: boolean;
  steps?: string[];
  listen?: boolean;
  listen_seconds?: number;
}

function toSurvey(j: Partial<Survey>): Survey {
  return {
    id: j.id ?? 0,
    state: j.state ?? "idle",
    antenna: j.antenna,
    region: j.region,
    started: j.started,
    elapsed_s: j.elapsed_s ?? 0,
    estimated_s: j.estimated_s ?? 0,
    steps: Array.isArray(j.steps) ? j.steps : [],
    listen: j.listen,
    listen_seconds: j.listen_seconds,
    listening: j.listening,
  };
}

export async function getSurvey(signal?: AbortSignal): Promise<Survey> {
  return toSurvey(await request<Partial<Survey>>("/api/survey", { signal }));
}

export async function startSurvey(p: SurveyParams): Promise<Survey> {
  return toSurvey(await post<Partial<Survey>>("/api/survey", p));
}

export async function stopSurvey(): Promise<Survey> {
  return toSurvey(await post<Partial<Survey>>("/api/survey/stop", {}));
}

// --- metadata (docs/metadata.md); every field is optional on the wire ---

export interface MetaDataset {
  name: string;
  loaded: boolean;
  records: number;
  fetched?: string;
  license?: string;
  attribution?: string;
  error?: string;
}

/** Resolves null when the backend has no /api/meta. */
export async function getMeta(signal?: AbortSignal): Promise<MetaDataset[] | null> {
  const res = await fetch("/api/meta", { signal });
  if (!res.ok) return null;
  const j = (await res.json()) as { datasets?: Partial<MetaDataset>[] };
  if (!Array.isArray(j.datasets)) return null;
  return j.datasets.map((d) => ({
    name: String(d.name ?? "?"),
    loaded: !!d.loaded,
    records: typeof d.records === "number" ? d.records : 0,
    fetched: d.fetched,
    license: d.license,
    attribution: d.attribution,
    error: d.error,
  }));
}

export interface SatDownlink {
  freq_hz: number;
  mode?: string;
  baud?: number;
  /** "packet" (AX.25 1200/9600), "nfm" (FM voice) or null */
  decoder?: "packet" | "nfm" | null;
  transmitter?: string;
}

export interface SatOverhead {
  norad: number;
  name: string;
  elevation_deg: number;
  azimuth_deg: number;
  range_km?: number;
  doppler_hz?: number;
  downlinks: SatDownlink[];
}

export interface SatPass {
  norad: number;
  name: string;
  /** unix seconds */
  aos: number;
  los: number;
  max_elevation_deg: number;
  max_time?: number;
  downlinks: SatDownlink[];
}

const num = (v: unknown): number | undefined => (typeof v === "number" && Number.isFinite(v) ? v : undefined);

function toDownlinks(v: unknown): SatDownlink[] {
  if (!Array.isArray(v)) return [];
  const out: SatDownlink[] = [];
  for (const r of v as Record<string, unknown>[]) {
    const f = num(r.freq_hz) ?? num(r.downlink_hz);
    if (f === undefined) continue;
    out.push({
      freq_hz: f,
      mode: typeof r.mode === "string" ? r.mode : undefined,
      baud: num(r.baud),
      decoder: r.decoder === "packet" || r.decoder === "nfm" ? r.decoder : null,
      transmitter: typeof r.transmitter === "string" ? r.transmitter : undefined,
    });
  }
  return out;
}

/** The overhead wrapper key is not pinned down by the contract: accept a bare array or the first array property. */
function firstArray(j: unknown): Record<string, unknown>[] {
  if (Array.isArray(j)) return j as Record<string, unknown>[];
  if (j && typeof j === "object") for (const v of Object.values(j)) if (Array.isArray(v)) return v as Record<string, unknown>[];
  return [];
}

export async function getOverhead(signal?: AbortSignal): Promise<SatOverhead[]> {
  const res = await fetch("/api/satellites/overhead", { signal });
  if (!res.ok) throw new Error(`overhead: ${res.status}`);
  const out: SatOverhead[] = [];
  for (const r of firstArray(await res.json())) {
    const el = num(r.elevation_deg);
    const az = num(r.azimuth_deg);
    if (typeof r.name !== "string" || el === undefined || az === undefined) continue;
    let dl = toDownlinks(r.downlinks);
    if (!dl.length && num(r.downlink_hz) !== undefined) dl = toDownlinks([r]);
    out.push({ norad: num(r.norad) ?? 0, name: r.name, elevation_deg: el, azimuth_deg: az, range_km: num(r.range_km), doppler_hz: num(r.doppler_hz), downlinks: dl });
  }
  return out;
}

/** `noReceiver` when the backend answers 400 (receiver location not set). */
export async function getPasses(
  hours: number,
  minElevation: number,
  signal?: AbortSignal,
): Promise<{ passes: SatPass[]; noReceiver: boolean }> {
  const res = await fetch(`/api/satellites/passes?hours=${hours}&min_elevation=${minElevation}`, { signal });
  if (res.status === 400) return { passes: [], noReceiver: true };
  if (!res.ok) throw new Error(`passes: ${res.status}`);
  const passes: SatPass[] = [];
  for (const r of firstArray(((await res.json()) as { passes?: unknown }).passes ?? [])) {
    const aos = num(r.aos);
    const los = num(r.los);
    if (typeof r.name !== "string" || aos === undefined || los === undefined) continue;
    passes.push({
      norad: num(r.norad) ?? 0, name: r.name, aos, los,
      max_elevation_deg: num(r.max_elevation_deg) ?? 0, max_time: num(r.max_time), downlinks: toDownlinks(r.downlinks),
    });
  }
  return { passes, noReceiver: false };
}
