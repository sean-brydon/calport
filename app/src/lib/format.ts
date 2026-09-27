import type { BoxState, CalportEvent, Lifecycle } from "@/lib/calport";

export const stateLabel: Record<BoxState, string> = {
  connecting: "Connecting",
  online: "Online",
  offline: "Offline",
  untrusted: "Access revoked",
};

export function since(iso: string): string {
  const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 60) return "just now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

// notice turns an event into a toast worth showing, or nothing for events
// that only refresh what is on screen.
export function notice(e: CalportEvent): { title: string; description?: string; type: "info" | "success" | "warning" | "error" } | undefined {
  const where = [e.box, typeof e.data?.path === "string" ? e.data.path : undefined].filter(Boolean).join(" · ");
  const agent = typeof e.data?.agent === "string" ? e.data.agent : "An agent";
  switch (e.type) {
    case "agent.finished":
      return { title: `${capitalize(agent)} finished`, description: where, type: "success" };
    case "agent.waiting":
      return { title: `${capitalize(agent)} needs you`, description: where, type: "warning" };
    case "box.disconnected":
      return { title: `${e.box} went offline`, description: e.error, type: "error" };
    case "box.untrusted":
      return { title: `${e.box} no longer trusts this laptop`, description: "Pair again to restore access.", type: "error" };
    case "worktree.setup.finished":
      return { title: `${String(e.data?.name)} is set up`, description: e.box, type: "success" };
    case "worktree.setup.failed":
    case "worktree.archive.failed":
      return { title: `${e.type.includes("setup") ? "Setup" : "Archive"} failed for ${String(e.data?.name)}`, description: e.error, type: "error" };
    case "share.stopped":
      return { title: "A public link stopped", description: typeof e.data?.url === "string" ? e.data.url : undefined, type: "info" };
  }
  return undefined;
}

// lifecycleOf reads a worktree setup or archive event, keyed by box and path.
export function lifecycleOf(e: CalportEvent): [string, Lifecycle] | undefined {
  const m = /^worktree\.(setup|archive)\.(started|finished|failed)$/.exec(e.type);
  if (!m || !e.box || typeof e.data?.path !== "string") return undefined;
  return [
    `${e.box}:${e.data.path}`,
    { kind: m[1] as Lifecycle["kind"], state: m[2] as Lifecycle["state"], log: typeof e.data.log === "string" ? e.data.log : undefined, error: e.error },
  ];
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// boxName mirrors calport's NameFromHostname: box names are hostnames in URLs.
export function boxName(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^[-._]+|[-._]+$/g, "")
    .slice(0, 63);
}
