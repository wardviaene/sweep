// Audio playback from /ws/audio: Int16 PCM frames scheduled on an AudioContext clock
// with a small jitter buffer. Nothing here touches React state at audio rate.
import { connectWs } from "./wsutil";
import type { ConnState } from "./spectrumSocket";

const TARGET_S = 0.15; // jitter buffer target
const MAX_AHEAD_S = 0.5; // beyond this, drop and resync

export interface LevelBus {
  db: number;
  open: boolean;
  listeners: Set<(db: number, open: boolean) => void>;
}

export interface AudioInfo {
  rate: number;
  mode: string;
}

export interface PlayerHandlers {
  onLevel(db: number, open: boolean): void;
  onInfo(info: AudioInfo): void;
  onState(s: ConnState): void;
}

export class AudioPlayer {
  private ctx: AudioContext;
  private gain: GainNode;
  private rate = 0;
  private nextTime = 0;
  private sources = new Set<AudioBufferSourceNode>();
  private stopWs: () => void;

  /** Must be constructed inside a user gesture (click handler). */
  constructor(volume: number, h: PlayerHandlers) {
    this.ctx = new AudioContext();
    void this.ctx.resume();
    this.gain = this.ctx.createGain();
    this.gain.gain.value = volume;
    this.gain.connect(this.ctx.destination);
    this.stopWs = connectWs("/ws/audio", {
      onState: h.onState,
      onOpen: () => this.reset(),
      onText: (t) => {
        try {
          const m = JSON.parse(t) as { type?: string; rate?: number; mode?: string; db?: number; open?: boolean };
          if (m.type === "audio" && typeof m.rate === "number") {
            if (m.rate !== this.rate) this.reset();
            this.rate = m.rate;
            h.onInfo({ rate: m.rate, mode: m.mode ?? "" });
          } else if (m.type === "level" && typeof m.db === "number") {
            h.onLevel(m.db, !!m.open);
          }
        } catch {
          /* ignore malformed */
        }
      },
      onBinary: (buf) => this.push(buf),
    });
  }

  setVolume(v: number): void {
    this.gain.gain.value = v;
  }

  private reset(): void {
    for (const s of this.sources) {
      try {
        s.stop();
      } catch {
        /* already ended */
      }
    }
    this.sources.clear();
    this.nextTime = 0;
  }

  private push(buf: ArrayBuffer): void {
    if (!this.rate || buf.byteLength < 2) return;
    const n = buf.byteLength >> 1;
    const pcm = new Int16Array(buf, 0, n);
    const now = this.ctx.currentTime;
    if (this.nextTime < now) this.nextTime = now + TARGET_S; // first frame or underrun: restart clock
    else if (this.nextTime - now > MAX_AHEAD_S) {
      this.reset(); // too far behind real time: drop what is queued and resync
      this.nextTime = now + TARGET_S;
    }
    const ab = this.ctx.createBuffer(1, n, this.rate);
    const ch = ab.getChannelData(0);
    for (let i = 0; i < n; i++) ch[i] = pcm[i]! / 32768;
    const src = this.ctx.createBufferSource();
    src.buffer = ab;
    src.connect(this.gain);
    src.onended = () => this.sources.delete(src);
    src.start(this.nextTime);
    this.sources.add(src);
    this.nextTime += ab.duration;
  }

  close(): void {
    this.stopWs();
    this.reset();
    void this.ctx.close();
  }
}
