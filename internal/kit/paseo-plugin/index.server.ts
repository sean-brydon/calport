import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";

import type { PluginServerContext } from "@getpaseo/plugin/server";

import { readBoxesStats } from "./server/boxes";
import { type Setup, readWorktreeInfo } from "./server/worktree";
import { isKitArchive, isKitWorktree } from "./server/worktree-identity";
import { boxesStats } from "./shared/boxes";
import { worktreeInfo } from "./shared/worktree";

const run = promisify(execFile);
const home = homedir();
const KIT_CONFIG = join(home, ".local/share/cal-worktrees/config.json");

// setups holds the setup still running for a workspace directory.
//
// This is the whole reason the plugin is not three independent callbacks.
// Paseo's reference says plainly that "workspace.created is not a setup barrier
// before agent startup": the agent can be running before the hook that gives
// its worktree a port and a database has finished. For Cal.com that is not a
// small race - an agent's first move is usually to start the dev server, which
// would bind nothing and talk to no database.
//
// agent.session_open is a *before* hook, so it is allowed to be slow. Parking
// the promise here and awaiting it there turns the race into a barrier, without
// delaying workspace creation itself.
const setups = new Map<string, Promise<Record<string, string>>>();

// outcomes is what the workspace panel reads. A promise cannot be asked
// whether it is still pending, and the panel has to distinguish "still being
// made" from "made" from "failed, which is why there is no port here".
const outcomes = new Map<string, Setup>();

// message is what the panel shows when setup fails, so it has to survive
// anything a rejected exec can carry.
function message(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

async function kitRoot(): Promise<string | undefined> {
  try {
    return JSON.parse(await readFile(KIT_CONFIG, "utf8")).root;
  } catch {
    // No kit on this machine: every hook below becomes a no-op.
    return undefined;
  }
}

// setup gives the worktree its port, database and URL, and reports the
// variables the agent needs to use them.
async function setup(cwd: string, root: string, name: string): Promise<Record<string, string>> {
  const env = { ...process.env, ORCA_ROOT_PATH: root, ORCA_WORKTREE_PATH: cwd, ORCA_WORKSPACE_NAME: name };
  await run(join(home, ".local/bin/cal-worktree"), ["setup"], { cwd, env });
  return { ORCA_ROOT_PATH: root, ORCA_WORKTREE_PATH: cwd, ORCA_WORKSPACE_NAME: name };
}

export default function contribute(server: PluginServerContext) {
  server.on("workspace.created", async (event) => {
    const root = await kitRoot();
    if (!root || !(await isKitWorktree(event.workspace.cwd, root))) return;
    const name = event.workspace.name ?? "";
    // Started here, awaited in session_open: creation stays fast, startup waits.
    const cwd = event.workspace.cwd;
    outcomes.set(cwd, { state: "pending" });
    const pending = setup(cwd, root, name);
    setups.set(cwd, pending);
    pending.then(
      () => outcomes.set(cwd, { state: "ready" }),
      (err) => outcomes.set(cwd, { state: "failed", detail: message(err) }),
    );
  });

  server.before("agent.session_open", async ({ request }) => {
    const pending = setups.get(request.cwd);
    if (!pending) return request;
    try {
      const vars = await pending;
      return { ...request, env: { ...request.env, ...vars } };
    } catch (err) {
      // Let the agent start anyway: a worktree without a dev server is still
      // worth working in, and failing to open would hide why.
      console.error(`cal.com kit setup failed for ${request.cwd}:`, err);
      return request;
    }
  });

  server.on("workspace.archived", async (event) => {
    const cwd = event.workspace.cwd;
    // Whether we set this one up has to be read before the record is dropped.
    const ours = outcomes.has(cwd);
    setups.delete(cwd);
    outcomes.delete(cwd);
    const root = await kitRoot();
    if (!root || !(await isKitArchive(cwd, root, ours))) return;
    const env = { ...process.env, ORCA_ROOT_PATH: root, ORCA_WORKTREE_PATH: cwd };
    await run(join(home, ".local/bin/cal-archive"), [], { cwd: home, env }).catch((err) => {
      console.error(`cal.com kit archive failed for ${cwd}:`, err);
    });
  });

  server.handle(boxesStats, () => readBoxesStats());
  server.handle(worktreeInfo, (input) => readWorktreeInfo(input, outcomes.get(input.directory)));

  return () => {
    setups.clear();
    outcomes.clear();
  };
}
