import { connect } from "node:net";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

import type { RpcInput, RpcOutput } from "@getpaseo/plugin";

import type { worktreeInfo } from "../shared/worktree";

export type Setup = { state: "pending" | "ready" | "failed"; detail?: string };

// env reads the few values the kit writes into a worktree's .env. It is not a
// general dotenv parser: it takes the last assignment of each key it wants and
// strips one layer of surrounding quotes, which is the shape cal-worktree
// writes.
function env(text: string, keys: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const eq = line.indexOf("=");
    if (eq < 0) continue;
    const key = line.slice(0, eq).trim();
    if (!keys.includes(key)) continue;
    out[key] = line
      .slice(eq + 1)
      .trim()
      .replace(/^(['"])(.*)\1$/, "$2");
  }
  return out;
}

// database names the database without its credentials: a Postgres URL carries
// the password, and this value is rendered on screen.
function database(url: string): { database?: string; databaseHost?: string } {
  try {
    const parsed = new URL(url);
    return {
      database: parsed.pathname.replace(/^\//, "") || undefined,
      databaseHost: parsed.host || undefined,
    };
  } catch {
    return {};
  }
}

// serving reports whether anything accepts a connection on the port. A refused
// connection is the ordinary "not started yet" case, not an error.
function serving(port: string): Promise<boolean> {
  const number = Number(port);
  if (!Number.isInteger(number) || number <= 0) return Promise.resolve(false);
  return new Promise((resolve) => {
    const socket = connect({ host: "127.0.0.1", port: number });
    const done = (answer: boolean) => {
      socket.destroy();
      resolve(answer);
    };
    socket.setTimeout(1000);
    socket.once("connect", () => done(true));
    socket.once("timeout", () => done(false));
    socket.once("error", () => done(false));
  });
}

export async function readWorktreeInfo(
  { directory }: RpcInput<typeof worktreeInfo>,
  setup: Setup | undefined,
): Promise<RpcOutput<typeof worktreeInfo>> {
  // A setup still running has nothing to read yet, and one that failed is the
  // reason the values below would be missing: say so rather than show blanks.
  if (setup?.state === "pending") return { state: "pending", path: directory };
  if (setup?.state === "failed") {
    return { state: "failed", path: directory, detail: setup.detail };
  }
  let text: string;
  try {
    text = await readFile(join(directory, ".env"), "utf8");
  } catch {
    // No .env and no setup of ours: some other project's workspace.
    if (!setup) return { state: "absent" };
    return { state: "failed", path: directory, detail: "The kit set this worktree up, but it has no .env." };
  }
  const values = env(text, ["PORT", "NEXT_PUBLIC_WEBAPP_URL", "DATABASE_URL"]);
  const port = values.PORT;
  if (!port) {
    if (!setup) return { state: "absent" };
    return { state: "failed", path: directory, detail: "This worktree's .env has no PORT." };
  }
  return {
    state: "ready",
    path: directory,
    port,
    url: values.NEXT_PUBLIC_WEBAPP_URL,
    ...database(values.DATABASE_URL ?? ""),
    serving: await serving(port),
  };
}
