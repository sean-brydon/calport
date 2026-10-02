import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";

const run = promisify(execFile);

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

export async function isKitWorktree(cwd: string, root: string): Promise<boolean> {
  if (resolve(cwd) === resolve(root)) return false; // the checkout itself, not a worktree of it
  const [a, b] = await Promise.all([gitCommonDir(cwd), gitCommonDir(root)]);
  return Boolean(a && b && a === b);
}

// isKitArchive decides whether a worktree being archived is one of ours, and is
// deliberately more willing to say yes than isKitWorktree is on creation.
//
// Getting this wrong in the two directions costs very different things. A false
// no leaks a port and a database for a worktree nobody will ever see again. A
// false yes runs cal-archive against a path it has no record of, which it
// treats as nothing to do.
//
// The git check alone gets it wrong exactly when it matters most: a worktree
// whose setup failed part-way, or whose directory git can no longer read, is
// still holding whatever cal-worktree allocated before it failed - and the
// database is created well before `yarn install` runs.
export async function isKitArchive(cwd: string, root: string, ours: boolean): Promise<boolean> {
  // We set it up this session: the strongest evidence there is, and it holds
  // for a half-finished worktree that no longer looks like a git worktree.
  if (ours) return true;
  if (await isKitWorktree(cwd, root)) return true;
  // Set up by a previous run of the plugin, before the daemon last restarted.
  // A database named by the kit is only there because the kit put it there.
  try {
    const env = await readFile(join(cwd, ".env"), "utf8");
    return /^DATABASE_URL=.*calwt_/m.test(env);
  } catch {
    // The directory is already gone, so nothing here can identify it. That is
    // what `calport kit reclaim` is for.
    return false;
  }
}
