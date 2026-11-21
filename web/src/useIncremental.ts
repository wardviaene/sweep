import { useEffect, useRef, useState } from "react";
import type { DecEvent } from "./useEvents";

/**
 * Folds new events of one decoder into a mutable model and publishes an immutable view.
 * `events` already arrives batched (every 250 ms), so React state updates at most that often.
 * The model resets if event ids go backwards (backend restart).
 */
export function useIncremental<M, V>(
  events: DecEvent[],
  decoder: string,
  init: () => M,
  apply: (m: M, e: DecEvent) => boolean,
  view: (m: M) => V,
): V {
  const model = useRef<M | null>(null);
  const lastId = useRef(0);
  const applyRef = useRef(apply);
  const viewRef = useRef(view);
  applyRef.current = apply;
  viewRef.current = view;
  if (model.current === null) model.current = init();
  const [v, setV] = useState<V>(() => view(model.current as M));

  useEffect(() => {
    const last = events[events.length - 1];
    if (!last) return;
    if (last.id < lastId.current) {
      model.current = init();
      lastId.current = 0;
    }
    let changed = false;
    for (const e of events) {
      if (e.id <= lastId.current) continue;
      lastId.current = e.id;
      if (e.decoder === decoder && applyRef.current(model.current as M, e)) changed = true;
    }
    if (changed) setV(viewRef.current(model.current as M));
  }, [events]); // eslint-disable-line react-hooks/exhaustive-deps

  return v;
}
