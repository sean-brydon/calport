import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { useLoad } from "@/hooks/use-calport";
import { calport } from "@/lib/calport";
import { bytes } from "@/lib/format";

// KitLeftovers says what worktrees whose folder is gone still hold on the
// box, which calport frees on its own after a grace period, and frees it now.
export function KitLeftovers({ box }: { box: string }) {
  const pending = useLoad(() => calport.reclaim(box, { dryRun: true, all: true }), [box]);
  const [busy, setBusy] = useState(false);
  const r = pending.data;
  if (!r || r.bytes === 0) return null;
  const worktrees = r.reclaimed.length;
  return (
    <p className="flex flex-wrap items-center gap-x-2 text-muted-foreground text-xs">
      <span>
        {worktrees > 0 && `${worktrees} removed worktree${worktrees === 1 ? "" : "s"}`}
        {worktrees > 0 && r.templates.length > 0 && " and "}
        {r.templates.length > 0 && `${r.templates.length} old snapshot${r.templates.length === 1 ? "" : "s"}`} still hold{" "}
        {bytes(r.bytes)}. Calport frees them a week after their folder goes, in case a worktree is restored.
      </span>
      <Button
        size="sm"
        variant="link"
        className="h-auto px-0 text-xs"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            const done = await calport.reclaim(box, { all: true });
            toastManager.add({ title: `Freed ${bytes(done.bytes)} on ${box}`, type: "success" });
            pending.reload();
          } catch (err) {
            toastManager.add({ title: "Could not free space", description: err instanceof Error ? err.message : String(err), type: "error" });
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy && <Spinner className="size-3" />}
        Free now
      </Button>
    </p>
  );
}
