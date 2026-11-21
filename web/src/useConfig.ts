import { useCallback, useEffect, useState } from "react";
import { getConfig, putConfig, type Receiver } from "./api";

export interface ConfigApi {
  receiver: Receiver | null;
  meterIds: string[];
  antenna: string | null;
  /** false when the backend has no /api/config (404) or it failed. */
  available: boolean;
  /** Sends only `receiver` (PUT merges). */
  save: (r: Receiver | null) => Promise<void>;
  /** Sends only `meter_ids`. */
  saveMeterIds: (ids: string[]) => Promise<void>;
  /** Sends only `antenna`. */
  saveAntenna: (id: string | null) => Promise<void>;
}

export function useConfig(): ConfigApi {
  const [receiver, setReceiver] = useState<Receiver | null>(null);
  const [meterIds, setMeterIds] = useState<string[]>([]);
  const [antenna, setAntenna] = useState<string | null>(null);
  const [available, setAvailable] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    getConfig(ac.signal)
      .then((c) => {
        if (!c) return;
        setAvailable(true);
        setReceiver(c.receiver);
        setMeterIds(c.meter_ids);
        setAntenna(c.antenna);
      })
      .catch(() => {
        /* unavailable */
      });
    return () => ac.abort();
  }, []);

  const save = useCallback(async (r: Receiver | null) => {
    const c = await putConfig({ receiver: r });
    setAvailable(true);
    setReceiver(c.receiver);
    setMeterIds(c.meter_ids);
    setAntenna(c.antenna);
  }, []);

  const saveMeterIds = useCallback(async (ids: string[]) => {
    const c = await putConfig({ meter_ids: ids });
    setAvailable(true);
    setReceiver(c.receiver);
    setMeterIds(c.meter_ids);
    setAntenna(c.antenna);
    setAntenna(c.antenna);
  }, []);

  const saveAntenna = useCallback(async (id: string | null) => {
    const c = await putConfig({ antenna: id });
    setAvailable(true);
    setReceiver(c.receiver);
    setMeterIds(c.meter_ids);
    setAntenna(c.antenna);
  }, []);

  return { receiver, meterIds, antenna, available, save, saveMeterIds, saveAntenna };
}
