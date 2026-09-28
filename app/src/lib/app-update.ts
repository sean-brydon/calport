import { getVersion } from "@tauri-apps/api/app";
import { relaunch } from "@tauri-apps/plugin-process";
import { check, type Update } from "@tauri-apps/plugin-updater";

import { inApp } from "@/lib/calport";

export type { Update };

// findUpdate asks the release feed for a newer, signed build of the app.
// Outside the app (the browser preview) there is nothing to update.
export async function findUpdate(): Promise<Update | null> {
  if (!inApp) return null;
  return check();
}

// installUpdate downloads the update, verifies its signature, replaces the
// app, and starts the new version.
export async function installUpdate(update: Update, onProgress?: (fraction: number) => void): Promise<void> {
  let total = 0;
  let done = 0;
  await update.downloadAndInstall((e) => {
    if (e.event === "Started") total = e.data.contentLength ?? 0;
    if (e.event === "Progress") {
      done += e.data.chunkLength;
      if (total) onProgress?.(done / total);
    }
  });
  await relaunch();
}

export async function appVersion(): Promise<string> {
  return inApp ? getVersion() : "preview";
}
