import { useCallback, useEffect, useRef, useState } from "react";
import { AudioPlayer, type AudioInfo, type LevelBus } from "./audio";
import type { ConnState } from "./spectrumSocket";

export interface AudioApi {
  on: boolean;
  toggle: () => void; // call from a click handler (needs a user gesture)
  volume: number;
  setVolume: (v: number) => void;
  conn: ConnState | null;
  info: AudioInfo | null;
  level: LevelBus;
}

export function useAudio(): AudioApi {
  const [on, setOn] = useState(false);
  const [volume, setVol] = useState(0.8);
  const [conn, setConn] = useState<ConnState | null>(null);
  const [info, setInfo] = useState<AudioInfo | null>(null);
  const player = useRef<AudioPlayer | null>(null);
  const volRef = useRef(volume);
  const level = useRef<LevelBus>({ db: -120, open: false, listeners: new Set() });

  const toggle = useCallback(() => {
    if (player.current) {
      player.current.close();
      player.current = null;
      setOn(false);
      setConn(null);
      setInfo(null);
      level.current.db = -120;
      level.current.open = false;
      level.current.listeners.forEach((l) => l(-120, false));
      return;
    }
    player.current = new AudioPlayer(volRef.current, {
      onLevel: (db, open) => {
        const b = level.current;
        b.db = db;
        b.open = open;
        b.listeners.forEach((l) => l(db, open));
      },
      onInfo: setInfo,
      onState: setConn,
    });
    setOn(true);
  }, []);

  const setVolume = useCallback((v: number) => {
    volRef.current = v;
    setVol(v);
    player.current?.setVolume(v);
  }, []);

  useEffect(
    () => () => {
      player.current?.close();
      player.current = null;
    },
    [],
  );

  return { on, toggle, volume, setVolume, conn, info, level: level.current };
}
