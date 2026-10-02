import { execFile } from "node:child_process";
import { constants } from "node:fs";
import { access } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";

import type { RpcOutput } from "@getpaseo/plugin";

import type { boxesStats } from "../shared/boxes";

const run = promisify(execFile);

// Where calport installs itself, plus the app bundle. The daemon runs under a
// service manager, so its PATH is not a login shell's and ~/.local/bin in
// particular is usually missing from it.
function candidates(): string[] {
  const home = homedir();
  return [
    join(home, ".local", "bin", "calport"),
    "/opt/homebrew/bin/calport",
    "/usr/local/bin/calport",
    "/Applications/Calport.app/Contents/Resources/calport",
  ];
}

async function calport(): Promise<string | undefined> {
  for (const path of candidates()) {
    try {
      await access(path, constants.X_OK);
      return path;
    } catch {
      // Try the next one.
    }
  }
  try {
    // Last resort: whatever PATH has, if anything.
    await run("calport", ["id"], { timeout: 5000 });
    return "calport";
  } catch {
    return undefined;
  }
}

type Json = Record<string, unknown>;

async function json(bin: string, args: string[], timeout: number): Promise<unknown> {
  const { stdout } = await run(bin, args, { timeout, maxBuffer: 8 * 1024 * 1024 });
  return JSON.parse(stdout);
}

function usage(value: unknown): { total: number; used: number } | undefined {
  const v = value as { total?: unknown; used?: unknown } | undefined;
  if (typeof v?.total !== "number" || typeof v?.used !== "number") return undefined;
  return { total: v.total, used: v.used };
}

export async function readBoxesStats(): Promise<RpcOutput<typeof boxesStats>> {
  const bin = await calport();
  if (!bin) {
    return {
      available: false,
      detail: "calport is not installed on this host, so it knows of no boxes.",
      boxes: [],
    };
  }
  let listed: Json[];
  try {
    listed = (await json(bin, ["boxes", "--json"], 15000)) as Json[];
  } catch (err) {
    return { available: true, detail: err instanceof Error ? err.message : String(err), boxes: [] };
  }
  // One box's daemon being slow must not hold up the others.
  return {
    available: true,
    boxes: await Promise.all(
      (listed ?? []).map(async (box) => {
        const base = {
          name: String(box.name ?? ""),
          state: String(box.state ?? "unknown"),
          network: typeof box.network === "string" ? box.network : undefined,
          latencyMs: typeof box.latency_ms === "number" ? box.latency_ms : undefined,
        };
        if (base.state !== "online") return base;
        try {
          const s = (await json(bin, ["stats", base.name, "--json"], 20000)) as Json;
          const agents = Array.isArray(s.agents) ? (s.agents as Json[]) : [];
          return {
            ...base,
            hostname: typeof s.hostname === "string" ? s.hostname : undefined,
            uptimeSeconds: typeof s.uptime_s === "number" ? s.uptime_s : undefined,
            cpus: typeof s.cpus === "number" ? s.cpus : undefined,
            load: Array.isArray(s.load) ? (s.load as number[]) : undefined,
            memory: usage(s.memory),
            swap: usage(s.swap),
            disks: Array.isArray(s.disks)
              ? (s.disks as Json[]).flatMap((d) => {
                  const u = usage(d);
                  return u ? [{ ...u, mount: String(d.mount ?? "") }] : [];
                })
              : undefined,
            agents: agents.map((a) => ({
              tool: String(a.tool ?? ""),
              state: String(a.state ?? ""),
              worktree: typeof a.worktree === "string" ? a.worktree : undefined,
            })),
          };
        } catch (err) {
          return { ...base, detail: err instanceof Error ? err.message : String(err) };
        }
      }),
    ),
  };
}
