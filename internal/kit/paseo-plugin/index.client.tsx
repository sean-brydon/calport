import type { PluginClientContext } from "@getpaseo/plugin/client";

import { BoxesSurface } from "./client/boxes-surface";
import { WorktreePanel } from "./client/worktree-panel";

const PANEL = "cal-worktree";

// How long the workspace subscription is given to replay what already exists
// before a new workspace is treated as a creation. Long enough that a slow
// replay is not mistaken for new worktrees appearing all at once.
const REPLAY_SETTLE_MS = 2000;

export default function contribute(client: PluginClientContext) {
  client.addWorkspacePanel({
    id: PANEL,
    title: "Cal.com worktree",
    icon: "Server",
    context: "workspace",
    Component: WorktreePanel,
  });
  client.addCommandCenterItem({
    id: "open-cal-worktree",
    title: "Show this worktree's port, URL and database",
    icon: "Server",
    context: "workspace",
    onSelect({ openPanel }) {
      openPanel(PANEL);
    },
  });

  client.addSurface("boxes", BoxesSurface);
  client.addSidebarItem({
    id: "boxes",
    title: "Boxes",
    icon: "Server",
    surface: "boxes",
  });

  // Open the panel when a worktree is created, so its port and URL - or, when
  // setup fails, the reason - are there without anyone going looking.
  //
  // The subscription replays the workspaces that already exist before it
  // reports new ones, and those are not creations: opening on everything it
  // delivers would stack up a panel for every worktree each time the app
  // starts. So the replay only records ids, and just the ones first seen after
  // it count as new. The cost is that a worktree created in the first moments
  // after the plugin loads does not auto-open, which is the right way round.
  const seen = new Set<string>();
  let replayed = false;
  const unsubscribe = client.paseo.workspaces.subscribe((update) => {
    if (update.kind !== "upsert") return;
    const { id, workspaceKind } = update.workspace;
    // Any other kind is the checkout itself, which has no port of its own.
    if (workspaceKind !== "worktree") return;
    const known = seen.has(id);
    seen.add(id);
    // Already open, or part of the replay: leave the user's tabs alone.
    if (known || !replayed) return;
    client.openPanel(PANEL, { workspaceId: id });
  });
  const settle = setTimeout(() => {
    replayed = true;
  }, REPLAY_SETTLE_MS);

  return () => {
    clearTimeout(settle);
    unsubscribe();
    seen.clear();
  };
}
