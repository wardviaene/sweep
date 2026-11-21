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
  | "rtl_433" | "meters" | "adsb" | "uat" | "aprs" | "ais" | "packet" | "radiosonde" | "dsd" | "sstv";

/** Per-decoder start options (packet, dsd, radiosonde). */
export interface DecoderOpts {
  freq_hz?: number;
  baud?: number;
  /** sstv: "auto" or a mode name */
  mode?: string;
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
  if (opts?.mode !== undefined) body.mode = opts.mode;
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

// --- scanner (docs/scanner.md) ---

export type ScannerRunState = "idle" | "watching" | "locked" | "stopped";

export interface ScannerWindow {
  center_hz: number;
  lo_hz: number;
  hi_hz: number;
  index: number;
  count: number;
}

export interface ScannerActive {
  freq_hz: number;
  label?: string;
  since: number; // server epoch seconds
  level_db: number;
}

export interface ScannerTx {
  /** transcript id when the backend assigns one to the transmission */
  id?: string;
  freq_hz: number;
  label?: string;
  start: number;
  duration_s: number;
  peak_db: number;
  mode?: string;
}

export interface ScannerChannel {
  freq_hz: number;
  label?: string;
  count: number;
  total_s: number;
  last: number;
}

export interface ScannerState {
  state: ScannerRunState;
  band?: string;
  lo_hz: number;
  hi_hz: number;
  mode: string;
  window?: ScannerWindow | null;
  active?: ScannerActive | null;
  recent: ScannerTx[];
  channels: ScannerChannel[];
  lockout_hz: number[];
  started?: number;
}

export interface ScannerParams {
  band?: string;
  lo_hz?: number;
  hi_hz?: number;
  mode?: string;
  bandwidth_hz?: number;
  step_hz?: number;
  margin_db?: number;
  hang_s?: number;
  dwell_ms?: number;
  max_tx_s?: number;
  lockout_hz?: number[];
  priority_hz?: number[];
}

type RawScanner = Partial<ScannerState>;
function toScanner(s: RawScanner): ScannerState {
  return {
    state: s.state ?? "idle",
    band: s.band,
    lo_hz: s.lo_hz ?? 0,
    hi_hz: s.hi_hz ?? 0,
    mode: s.mode ?? "",
    window: s.window ?? null,
    active: s.active ?? null,
    recent: Array.isArray(s.recent) ? s.recent : [],
    channels: Array.isArray(s.channels) ? s.channels : [],
    lockout_hz: Array.isArray(s.lockout_hz) ? s.lockout_hz : [],
    started: s.started,
  };
}

export async function getScanner(signal?: AbortSignal): Promise<ScannerState> {
  return toScanner(await request<RawScanner>("/api/scanner", { signal }));
}
export async function startScanner(p: ScannerParams): Promise<ScannerState> {
  return toScanner(await post<RawScanner>("/api/scanner", p));
}
export async function stopScanner(): Promise<ScannerState> {
  return toScanner(await post<RawScanner>("/api/scanner/stop", {}));
}
export async function scannerLockout(freq_hz: number, remove?: boolean): Promise<void> {
  await post<unknown>("/api/scanner/lockout", remove ? { freq_hz, remove: true } : { freq_hz });
}

// ---- pass autopilot (docs/autopilot.md) ----

export interface ApPass {
  norad: number;
  name: string;
  aos: number;
  los: number;
  max_elevation_deg: number;
  downlink_hz: number;
  decoder: string;
  baud?: number;
}
export interface ApCurrent extends ApPass {
  doppler_hz: number;
  elevation_deg: number;
  azimuth_deg: number;
  packets: number;
  images: number;
  dir: string;
}
export interface ApNext extends ApPass {
  will_run: boolean;
  reason: string;
}
export interface ApHistory {
  norad: number;
  name: string;
  aos: number;
  los: number;
  max_elevation_deg: number;
  decoder: string;
  packets: number;
  images: number;
  audio: string;
  dir: string;
  state: string;
  reason: string;
}
export interface Autopilot {
  enabled: boolean;
  min_elevation: number;
  satellites: number[];
  preempt: boolean;
  listen: boolean;
  record_iq: boolean;
  receiver_set: boolean;
  datasets_ok: boolean;
  current: ApCurrent | null;
  next: ApNext[];
  history: ApHistory[];
}
export type AutopilotPatch = Partial<Pick<Autopilot, "enabled" | "min_elevation" | "satellites" | "preempt" | "listen" | "record_iq">>;

const str = (v: unknown): string => (typeof v === "string" ? v : "");
const n0 = (v: unknown): number => num(v) ?? 0;

function toApPass(r: Record<string, unknown>): ApPass {
  return {
    norad: n0(r.norad), name: str(r.name), aos: n0(r.aos), los: n0(r.los),
    max_elevation_deg: n0(r.max_elevation_deg), downlink_hz: n0(r.downlink_hz), decoder: str(r.decoder), baud: num(r.baud),
  };
}
const rec = (v: unknown): Record<string, unknown> | null => (v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null);
const arr = (v: unknown): Record<string, unknown>[] => (Array.isArray(v) ? (v as unknown[]).map(rec).filter((x): x is Record<string, unknown> => x !== null) : []);

function toAutopilot(j: Record<string, unknown>): Autopilot {
  const c = rec(j.current);
  return {
    enabled: j.enabled === true,
    min_elevation: num(j.min_elevation) ?? 20,
    satellites: Array.isArray(j.satellites) ? (j.satellites as unknown[]).filter((x): x is number => typeof x === "number") : [],
    preempt: j.preempt === true,
    listen: j.listen !== false,
    record_iq: j.record_iq === true,
    receiver_set: j.receiver_set !== false,
    datasets_ok: j.datasets_ok !== false,
    current: c
      ? { ...toApPass(c), doppler_hz: n0(c.doppler_hz), elevation_deg: n0(c.elevation_deg), azimuth_deg: n0(c.azimuth_deg), packets: n0(c.packets), images: n0(c.images), dir: str(c.dir) }
      : null,
    next: arr(j.next).slice(0, 50).map((r) => ({ ...toApPass(r), will_run: r.will_run === true, reason: str(r.reason) })),
    history: arr(j.history).slice(0, 50).map((r) => ({
      norad: n0(r.norad), name: str(r.name), aos: n0(r.aos), los: n0(r.los), max_elevation_deg: n0(r.max_elevation_deg),
      decoder: str(r.decoder), packets: n0(r.packets), images: n0(r.images), audio: str(r.audio), dir: str(r.dir),
      state: str(r.state), reason: str(r.reason),
    })),
  };
}

export async function getAutopilot(signal?: AbortSignal): Promise<Autopilot> {
  return toAutopilot(await request<Record<string, unknown>>("/api/autopilot", { signal }));
}
export async function putAutopilot(patch: AutopilotPatch): Promise<Autopilot> {
  return toAutopilot(
    await request<Record<string, unknown>>("/api/autopilot", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}
export async function autopilotStop(): Promise<void> {
  await post<unknown>("/api/autopilot/stop", {});
}
export async function autopilotRun(norad: number): Promise<void> {
  await post<unknown>("/api/autopilot/run", { norad });
}

// ---- transcription (docs/transcription.md) ----

export type TranscriptSource = "scanner" | "survey" | "manual";

export interface Transcript {
  id: string;
  source: TranscriptSource;
  freq_hz: number;
  label: string;
  mode: string;
  /** clip start, epoch seconds */
  start: number;
  duration_s: number;
  text: string;
  empty: boolean;
  url: string;
}

/** Validates one transcript record (an event's data, a list item or a POST result). */
export function toTranscript(d: unknown): Transcript | null {
  if (!d || typeof d !== "object") return null;
  const r = d as Record<string, unknown>;
  const f = r.freq_hz;
  const s = r.start;
  if (typeof f !== "number" || typeof s !== "number") return null;
  const text = typeof r.text === "string" ? r.text.trim() : "";
  const src = r.source === "survey" || r.source === "manual" ? r.source : "scanner";
  return {
    id: typeof r.id === "string" && r.id !== "" ? r.id : `${f}:${s}`,
    source: src,
    freq_hz: f,
    label: typeof r.label === "string" ? r.label : "",
    mode: typeof r.mode === "string" ? r.mode : "",
    start: s,
    duration_s: typeof r.duration_s === "number" ? r.duration_s : 0,
    text,
    empty: r.empty === true || text === "",
    url: typeof r.url === "string" ? r.url : "",
  };
}

/** Newest first; resolves [] on a backend without the endpoint is left to the caller (it throws). */
export async function getTranscripts(
  o: { q?: string; freq_hz?: number; limit?: number },
  signal?: AbortSignal,
): Promise<Transcript[]> {
  const u = new URLSearchParams({ since: "0", limit: String(o.limit ?? 100) });
  if (o.q) u.set("q", o.q);
  if (o.freq_hz) u.set("freq_hz", String(o.freq_hz));
  const j = await request<unknown>(`/api/transcripts?${u.toString()}`, { signal });
  const arr = Array.isArray(j) ? j : j && typeof j === "object" && Array.isArray((j as { transcripts?: unknown }).transcripts) ? (j as { transcripts: unknown[] }).transcripts : [];
  const out: Transcript[] = [];
  for (const x of arr) {
    const t = toTranscript(x);
    if (t) out.push(t);
  }
  return out;
}

export interface TranscribeStatus {
  enabled: boolean;
  model: string;
  server: "running" | "stopped" | "error" | string;
  queue: number;
  last_error: string;
}

function toTranscribeStatus(j: Partial<TranscribeStatus>): TranscribeStatus {
  return {
    enabled: j.enabled === true,
    model: typeof j.model === "string" ? j.model : "",
    server: typeof j.server === "string" ? j.server : "stopped",
    queue: typeof j.queue === "number" ? j.queue : 0,
    last_error: typeof j.last_error === "string" ? j.last_error : "",
  };
}

export async function getTranscribe(signal?: AbortSignal): Promise<TranscribeStatus> {
  return toTranscribeStatus(await request<Partial<TranscribeStatus>>("/api/transcribe", { signal }));
}

export async function putTranscribe(enabled: boolean): Promise<TranscribeStatus> {
  return toTranscribeStatus(
    await request<Partial<TranscribeStatus>>("/api/transcribe", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
  );
}

/** Records the next `seconds` of the VFO audio and transcribes it; resolves when done (the record may be null). */
export async function postTranscribe(seconds: number): Promise<Transcript | null> {
  return toTranscript(await post<unknown>("/api/transcribe", { seconds }));
}
