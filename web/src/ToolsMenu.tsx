import { useEffect, useMemo, useRef, useState } from "react";
import { record, scan, type RecordResult, type ScanResult, type Status } from "./api";
import type { BandView } from "./bands";
import type { DemodApi } from "./useDemod";
import type { Tuner } from "./useTuner";
import { Popover } from "./Popover";
import { parseFreq } from "./Controls";
import { setPeakLabels } from "./meta";

const mhzStr = (hz: number) => String(+(hz / 1e6).toFixed(4));

function useElapsed(running: boolean): number {
  const [t, setT] = useState(0);
  useEffect(() => {
    if (!running) return;
    const t0 = performance.now();
    setT(0);
    const id = window.setInterval(() => setT((performance.now() - t0) / 1000), 200);
    return () => window.clearInterval(id);
  }, [running]);
  return t;
}

type SortKey = "snr" | "freq";

export function ToolsMenu(props: {
  status: Status | null;
  bandViews: BandView[];
  tuner: Tuner;
  demod: DemodApi;
  open: boolean;
  setOpen: (o: boolean) => void;
}) {
  const { status, bandViews, tuner, demod, open, setOpen } = props;
  const [start, setStart] = useState("");
  const [stop, setStop] = useState("");
  const [dwell, setDwell] = useState("500");
  const [minSnr, setMinSnr] = useState("10");
  const [scanning, setScanning] = useState(false);
  const [scanErr, setScanErr] = useState<string | null>(null);
  const [result, setResult] = useState<ScanResult | null>(null);
  const [estimate, setEstimate] = useState(0);
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "snr", dir: -1 });
  const prefilled = useRef(false);

  const [secs, setSecs] = useState("5");
  const [recording, setRecording] = useState(false);
  const [recErr, setRecErr] = useState<string | null>(null);
  const [rec, setRec] = useState<RecordResult | null>(null);
  const [recTotal, setRecTotal] = useState(0);
  const [copied, setCopied] = useState(false);

  const center = status?.center_hz;
  const rate = status?.sample_rate;
  const fillSpan = () => {
    if (center === undefined || rate === undefined) return;
    setStart(mhzStr(center - rate / 2));
    setStop(mhzStr(center + rate / 2));
  };
  const fillBand = () => {
    if (center === undefined) return;
    const b = bandViews.find((v) => center >= v.lo_hz && center <= v.hi_hz);
    if (b) {
      setStart(mhzStr(b.lo_hz));
      setStop(mhzStr(b.hi_hz));
    } else fillSpan();
  };
  // Prefill once when the panel is first opened with a known tuning.
  useEffect(() => {
    if (open && !prefilled.current && center !== undefined && rate !== undefined) {
      prefilled.current = true;
      fillBand();
    }
  }, [open, center, rate]); // eslint-disable-line react-hooks/exhaustive-deps

  const startHz = parseFreq(start);
  const stopHz = parseFreq(stop);
  const dwellN = Number(dwell);
  const snrN = Number(minSnr);
  const inputsOk =
    startHz !== null && stopHz !== null && stopHz > startHz && dwellN >= 100 && dwellN <= 5000 && Number.isFinite(snrN);
  const decoderRunning = !!status?.decoder?.running && status.decoder.name !== "none";
  const busy = scanning || recording;

  const elapsedScan = useElapsed(scanning);
  const elapsedRec = useElapsed(recording);

  const doScan = () => {
    if (!inputsOk || startHz === null || stopHz === null) return;
    const steps = Math.max(1, Math.ceil((stopHz - startHz) / (0.8 * (rate ?? 2.048e6))));
    setEstimate((steps * (dwellN + 100)) / 1000);
    setScanning(true);
    setScanErr(null);
    scan({ start_hz: startHz, stop_hz: stopHz, dwell_ms: dwellN, min_snr: snrN, max: 50 })
      .then(setResult)
      .catch((e: unknown) => setScanErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setScanning(false));
  };

  const secsN = Number(secs);
  const secsOk = Number.isFinite(secsN) && secsN >= 0.1 && secsN <= 60;
  const doRecord = () => {
    if (!secsOk) return;
    setRecTotal(secsN);
    setRecording(true);
    setRecErr(null);
    setRec(null);
    record(secsN)
      .then(setRec)
      .catch((e: unknown) => setRecErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setRecording(false));
  };
  const copy = (text: string) => {
    void navigator.clipboard
      ?.writeText(text)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => undefined);
  };

  const peaks = useMemo(() => {
    const p = (result?.peaks ?? []).slice();
    p.sort((a, b) => sort.dir * (sort.key === "snr" ? a.snr_db - b.snr_db : a.freq_hz - b.freq_hz));
    return p;
  }, [result, sort]);
  const hasLabels = useMemo(() => (result?.peaks ?? []).some((p) => p.label), [result]);
  useEffect(() => setPeakLabels(result?.peaks ?? []), [result]);
  const hdr = (key: SortKey, label: string) => (
    <th className="sortable" onClick={() => setSort((s) => (s.key === key ? { key, dir: (-s.dir) as 1 | -1 } : { key, dir: key === "snr" ? -1 : 1 }))}>
      {label}
      {sort.key === key ? (sort.dir === 1 ? " ▲" : " ▼") : ""}
    </th>
  );

  const tuneTo = (hz: number) => {
    if (status?.demod && status.demod.mode !== "off") demod.send({ freq_hz: hz }, true);
    else tuner.setFreq(hz);
  };

  const fieldOk = (v: number | null) => v !== null;
  return (
    <Popover label={busy ? "Tools …" : "Tools"} title="Scan and record" open={open} setOpen={setOpen} width={380}>
      <div className="label">scan</div>
      <div className="form">
        <label>start MHz <input className={`freq narrow ${fieldOk(startHz) ? "" : "invalid"}`} value={start} onChange={(e) => setStart(e.target.value)} /></label>
        <label>stop MHz <input className={`freq narrow ${fieldOk(stopHz) && (startHz === null || stopHz! > startHz) ? "" : "invalid"}`} value={stop} onChange={(e) => setStop(e.target.value)} /></label>
        <label>dwell ms <input className={`freq narrow ${dwellN >= 100 && dwellN <= 5000 ? "" : "invalid"}`} value={dwell} onChange={(e) => setDwell(e.target.value)} /></label>
        <label>min SNR dB <input className={`freq narrow ${Number.isFinite(snrN) ? "" : "invalid"}`} value={minSnr} onChange={(e) => setMinSnr(e.target.value)} /></label>
      </div>
      <div className="row">
        <button disabled={busy || !inputsOk || decoderRunning} onClick={doScan}>{scanning ? "Scanning…" : "Scan"}</button>
        <button className="mini" onClick={fillSpan} disabled={scanning}>span</button>
        <button className="mini" onClick={fillBand} disabled={scanning}>band</button>
        {decoderRunning && <span className="dim">stop the decoder first</span>}
      </div>
      {scanning && (
        <div className="pad0">
          <div className="indet"><div /></div>
          <span className="dim">~{estimate.toFixed(1)} s estimated, {elapsedScan.toFixed(0)} s elapsed</span>
        </div>
      )}
      {scanErr && <div className="err pad0">{scanErr}</div>}
      {result && (
        <div className="pad0">
          <div className="dim">
            {mhzStr(result.start_hz)}-{mhzStr(result.stop_hz)} MHz, {result.steps.length} steps, {result.duration_s.toFixed(1)} s, {result.peaks.length} peaks
          </div>
          {result.peaks.length > 0 && (
            <div className="tablewrap peaks">
              <table>
                <thead>
                  <tr>{hdr("freq", "MHz")}{hasLabels && <th>label</th>}<th>dB</th>{hdr("snr", "SNR")}<th /></tr>
                </thead>
                <tbody>
                  {peaks.map((p) => (
                    <tr key={p.freq_hz} className="click" onClick={() => tuneTo(p.freq_hz)} title="Tune here">
                      <td>{(p.freq_hz / 1e6).toFixed(4)}</td>
                      {hasLabels && <td className="lbl">{p.label ?? ""}</td>}
                      <td>{p.db.toFixed(1)}</td>
                      <td>{p.snr_db.toFixed(1)}</td>
                      <td>{p.spur && <span className="badge" title="Likely generated inside the dongle">spur</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
      <hr />
      <div className="label">record IQ</div>
      <div className="row">
        <label>seconds <input className={`freq narrow ${secsOk ? "" : "invalid"}`} value={secs} onChange={(e) => setSecs(e.target.value)} /></label>
        <button disabled={busy || !secsOk || decoderRunning} onClick={doRecord}>{recording ? "Recording…" : "Record IQ"}</button>
      </div>
      {recording && (
        <div className="pad0">
          <div className="indet"><div /></div>
          <span className="dim">{Math.max(0, recTotal - elapsedRec).toFixed(1)} s left</span>
        </div>
      )}
      {recErr && <div className="err pad0">{recErr}</div>}
      {rec && (
        <div className="pad0">
          <div className="pathrow">
            <code className="starter">{rec.path}</code>
            <button className="mini" onClick={() => copy(rec.path)}>{copied ? "copied" : "copy"}</button>
          </div>
          <div className="dim">
            {(rec.bytes / 1e6).toFixed(1)} MB, {rec.seconds} s @ {mhzStr(rec.center_hz)} MHz. Play back with: bin/sdrd -iq {rec.path}
          </div>
        </div>
      )}
    </Popover>
  );
}
