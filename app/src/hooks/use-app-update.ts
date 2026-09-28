import { useCallback, useEffect, useRef, useState } from "react";

import { toastManager } from "@/components/ui/toast";
import { findUpdate, installUpdate, type Update } from "@/lib/app-update";

const EVERY = 6 * 60 * 60 * 1000;

export interface AppUpdate {
  update: Update | null;
  checking: boolean;
  installing: boolean;
  error?: string;
  checkNow: () => Promise<void>;
  install: () => Promise<void>;
}

// useAppUpdate checks for a new release at launch and every few hours, and
// offers it once in a toast; Settings shows the same state.
export function useAppUpdate(): AppUpdate {
  const [update, setUpdate] = useState<Update | null>(null);
  const [checking, setChecking] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [error, setError] = useState<string>();
  const offered = useRef<string | undefined>(undefined);

  const install = useCallback(async () => {
    if (!update) return;
    setInstalling(true);
    const id = toastManager.add({ title: `Updating to Calport ${update.version}…`, type: "loading", timeout: 0 });
    try {
      await installUpdate(update, (f) => toastManager.update(id, { description: `${Math.round(f * 100)}%` }));
    } catch (err) {
      setInstalling(false);
      toastManager.update(id, { title: "The update did not install", description: err instanceof Error ? err.message : String(err), type: "error" });
    }
  }, [update]);

  const checkNow = useCallback(async () => {
    setChecking(true);
    setError(undefined);
    try {
      setUpdate(await findUpdate());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setChecking(false);
    }
  }, []);

  useEffect(() => {
    if (import.meta.env.DEV) return;
    checkNow();
    const t = window.setInterval(checkNow, EVERY);
    return () => window.clearInterval(t);
  }, [checkNow]);

  useEffect(() => {
    if (!update || offered.current === update.version) return;
    offered.current = update.version;
    toastManager.add({
      title: `Calport ${update.version} is available`,
      description: "It restarts the app; boxes stay connected.",
      type: "info",
      timeout: 0,
      actionProps: { children: "Restart to update", onClick: install },
    });
  }, [update, install]);

  return { update, checking, installing, error, checkNow, install };
}
