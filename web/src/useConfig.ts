import { useCallback, useEffect, useState } from "react";
import { getConfig, putConfig, type Receiver } from "./api";

export interface ConfigApi {
  receiver: Receiver | null;
  meterIds: string[];
  /** false when the backend has no /api/config (404) or it failed. */
  available: boolean;
  /** Sends only `receiver` (PUT merges). */
  save: (r: Receiver | null) => Promise<void>;
  /** Sends only `meter_ids`. */
  saveMeterIds: (ids: string[]) => Promise<void>;
}

export function useConfig(): ConfigApi {
  const [receiver, setReceiver] = useState<Receiver | null>(null);
  const [meterIds, setMeterIds] = useState<string[]>([]);
  const [available, setAvailable] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    getConfig(ac.signal)
      .then((c) => {
        if (!c) return;
        setAvailable(true);
        setReceiver(c.receiver);
        setMeterIds(c.meter_ids);
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
  }, []);

  const saveMeterIds = useCallback(async (ids: string[]) => {
    const c = await putConfig({ meter_ids: ids });
    setAvailable(true);
    setReceiver(c.receiver);
    setMeterIds(c.meter_ids);
  }, []);

  return { receiver, meterIds, available, save, saveMeterIds };
}
