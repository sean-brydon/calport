import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";

import type { PluginServerContext } from "@getpaseo/plugin/server";

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

async function kitRoot(): Promise<string | undefined> {
  try {
    return JSON.parse(await readFile(KIT_CONFIG, "utf8")).root;
  } catch {
    // No kit on this machine: every hook below becomes a no-op.
    return undefined;
  }
}

// gitCommonDir identifies the repository a path belongs to, so a worktree of
// some other checkout is left alone. Paseo runs these hooks for every project.
async function gitCommonDir(cwd: string): Promise<string | undefined> {
  try {
    const { stdout } = await run("git", ["-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"]);
    return resolve(stdout.trim());
  } catch {
    return undefined;
  }
}

async function isKitWorktree(cwd: string, root: string): Promise<boolean> {
  if (resolve(cwd) === resolve(root)) return false; // the checkout itself, not a worktree of it
  const [a, b] = await Promise.all([gitCommonDir(cwd), gitCommonDir(root)]);
  return Boolean(a && b && a === b);
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
    setups.set(event.workspace.cwd, setup(event.workspace.cwd, root, name));
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
    const root = await kitRoot();
    setups.delete(event.workspace.cwd);
    // The directory may still exist here - the reference warns that archive
    // events can precede worktree cleanup - which is what we want: the port and
    // database are released while the worktree is still identifiable.
    if (!root || !(await isKitWorktree(event.workspace.cwd, root))) return;
    const env = { ...process.env, ORCA_ROOT_PATH: root, ORCA_WORKTREE_PATH: event.workspace.cwd };
    await run(join(home, ".local/bin/cal-archive"), [], { cwd: home, env }).catch((err) => {
      console.error(`cal.com kit archive failed for ${event.workspace.cwd}:`, err);
    });
  });

  return () => setups.clear();
}
