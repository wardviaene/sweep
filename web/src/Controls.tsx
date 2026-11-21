import { useEffect, useRef, useState } from "react";
import type { Status } from "./api";
import type { Tuner } from "./useTuner";
import { clampHz, MAX_HZ, MIN_HZ } from "./useTuner";

export const STEPS: { hz: number; label: string }[] = [
  { hz: 1e3, label: "1 kHz" },
  { hz: 5e3, label: "5 kHz" },
  { hz: 12.5e3, label: "12.5 kHz" },
  { hz: 25e3, label: "25 kHz" },
  { hz: 100e3, label: "100 kHz" },
  { hz: 1e6, label: "1 MHz" },
];
const RATES = [1.024e6, 1.4e6, 1.8e6, 2.048e6, 2.4e6];
const BIG = 10;

/** Parse "433.92", "433.92M", "433920k", "433920000", "1.42G". Bare numbers < 10000 are MHz, else Hz. */
export function parseFreq(text: string): number | null {
  const m = /^\s*(\d*\.?\d+)\s*([kmg]?)(?:hz)?\s*$/i.exec(text);
  if (!m) return null;
  const v = parseFloat(m[1]!);
  const u = m[2]!.toLowerCase();
  const hz = u === "k" ? v * 1e3 : u === "m" ? v * 1e6 : u === "g" ? v * 1e9 : v < 1e4 ? v * 1e6 : v;
  return Number.isFinite(hz) ? Math.round(hz) : null;
}

const mhz = (hz: number) => (hz / 1e6).toFixed(4);

function withCurrent(values: number[], cur: number | undefined, eq: (a: number, b: number) => boolean) {
  if (cur === undefined || values.some((v) => eq(v, cur))) return values;
  return [...values, cur].sort((a, b) => a - b);
}

export function Controls(props: {
  status: Status | null;
  centerHz: number | undefined;
  tuner: Tuner;
  step: number;
  setStep: (hz: number) => void;
}) {
  const { status, centerHz, tuner, step, setStep } = props;
  const [text, setText] = useState("");
  const [focused, setFocused] = useState(false);
  const [bad, setBad] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!focused && centerHz !== undefined) setText(mhz(centerHz));
  }, [centerHz, focused]);

  const apply = () => {
    const hz = parseFreq(text);
    if (hz === null) return setBad("invalid frequency");
    if (hz < MIN_HZ || hz > MAX_HZ) return setBad(`range ${MIN_HZ / 1e6}-${MAX_HZ / 1e6} MHz`);
    setBad(null);
    tuner.setFreq(clampHz(hz));
    inputRef.current?.blur();
  };

  const shift = (n: number) => tuner.shiftFreq(n * step);

  const gains = status ? withCurrent(status.gains_db, status.gain_db ?? undefined, (a, b) => Math.abs(a - b) < 0.05) : [];
  const gainVal = status?.gain_db == null ? "auto" : String(status.gain_db);
  const rates = withCurrent(RATES, status?.sample_rate, (a, b) => a === b);

  return (
    <div className="bar controls">
      <span className="item">
        <span className="label">freq MHz</span>{" "}
        <input
          ref={inputRef}
          className={`freq ${bad ? "invalid" : ""}`}
          value={text}
          spellCheck={false}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            setFocused(false);
            setBad(null);
          }}
          onChange={(e) => {
            setText(e.target.value);
            setBad(null);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") apply();
            else if (e.key === "Escape") inputRef.current?.blur();
          }}
        />
        {bad && <span className="err">{bad}</span>}
      </span>
      <span className="item">
        <button onClick={() => shift(-BIG)} title="-10 steps">«</button>
        <button onClick={() => shift(-1)} title="-1 step">‹</button>
        <button onClick={() => shift(1)} title="+1 step">›</button>
        <button onClick={() => shift(BIG)} title="+10 steps">»</button>{" "}
        <select value={step} onChange={(e) => setStep(Number(e.target.value))} aria-label="step">
          {STEPS.map((s) => (
            <option key={s.hz} value={s.hz}>{s.label}</option>
          ))}
        </select>
      </span>
      <span className="item">
        <span className="label">gain</span>{" "}
        <select
          value={gainVal}
          disabled={!status}
          onChange={(e) => tuner.setParams({ gain_db: e.target.value === "auto" ? null : Number(e.target.value) })}
        >
          <option value="auto">auto</option>
          {gains.map((g) => (
            <option key={g} value={String(g)}>{g.toFixed(1)} dB</option>
          ))}
        </select>
      </span>
      <span className="item">
        <span className="label">rate</span>{" "}
        <select
          value={status?.sample_rate ?? ""}
          disabled={!status}
          onChange={(e) => tuner.setParams({ sample_rate: Number(e.target.value) })}
        >
          {rates.map((r) => (
            <option key={r} value={r}>{(r / 1e6).toFixed(3)} MS/s</option>
          ))}
        </select>
      </span>
      {tuner.error && <span className="err">{tuner.error}</span>}
    </div>
  );
}
