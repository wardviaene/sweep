import { useCallback, useEffect, useState } from "react";
import { getConfig, putConfig, type Receiver } from "./api";

export interface ConfigApi {
  receiver: Receiver | null;
  /** false when the backend has no /api/config (404) or it failed. */
  available: boolean;
  save: (r: Receiver | null) => Promise<void>;
}

export function useConfig(): ConfigApi {
  const [receiver, setReceiver] = useState<Receiver | null>(null);
  const [available, setAvailable] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    getConfig(ac.signal)
      .then((c) => {
        if (!c) return;
        setAvailable(true);
        setReceiver(c.receiver);
      })
      .catch(() => {
        /* unavailable */
      });
    return () => ac.abort();
  }, []);

  const save = useCallback(async (r: Receiver | null) => {
    const c = await putConfig(r);
    setAvailable(true);
    setReceiver(c.receiver);
  }, []);

  return { receiver, available, save };
}
