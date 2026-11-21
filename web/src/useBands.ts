import { useEffect, useState } from "react";
import { getBands } from "./api";
import type { Catalog } from "./bands";

/** Loads the band catalog once; retries every 3 s while the backend is unavailable. */
export function useBands(): { catalog: Catalog | null; error: boolean } {
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => {
    const ac = new AbortController();
    let timer: number | undefined;
    const load = async () => {
      try {
        setCatalog(await getBands(ac.signal));
        setError(false);
      } catch {
        if (ac.signal.aborted) return;
        setError(true);
        timer = window.setTimeout(load, 3000);
      }
    };
    void load();
    return () => {
      ac.abort();
      window.clearTimeout(timer);
    };
  }, []);
  return { catalog, error };
}
