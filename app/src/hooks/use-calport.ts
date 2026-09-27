import { useCallback, useEffect, useRef, useState } from "react";

import { type CalportEvent, watchEvents } from "@/lib/calport";

export interface Loadable<T> {
  data: T | undefined;
  error: string | undefined;
  loading: boolean;
  reload: () => void;
}

// useLoad runs load now, whenever deps change, and on reload. It keeps showing
// the last good data while a reload is in flight so screens do not flicker.
export function useLoad<T>(load: () => Promise<T>, deps: unknown[]): Loadable<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const current = useRef(0);

  useEffect(() => {
    const request = ++current.current;
    setLoading(true);
    load()
      .then((value) => {
        if (request !== current.current) return;
        setData(value);
        setError(undefined);
      })
      .catch((err: unknown) => {
        if (request !== current.current) return;
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (request === current.current) setLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}

// usePoll reloads every interval while the window is visible.
export function usePoll(reload: () => void, intervalMs: number) {
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") reload();
    }, intervalMs);
    return () => window.clearInterval(id);
  }, [reload, intervalMs]);
}

// useEvents delivers every event the agent relays, for the component's lifetime.
export function useEvents(onEvent: (e: CalportEvent) => void) {
  const handler = useRef(onEvent);
  handler.current = onEvent;
  useEffect(() => {
    let stop: (() => void) | undefined;
    let cancelled = false;
    watchEvents((e) => handler.current(e))
      .then((s) => {
        if (cancelled) s();
        else stop = s;
      })
      .catch(() => {
        // Without an agent there are no events; polling still keeps screens fresh.
      });
    return () => {
      cancelled = true;
      stop?.();
    };
  }, []);
}
