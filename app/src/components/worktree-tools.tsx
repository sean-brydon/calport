import { CheckIcon } from "lucide-react";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { useLoad } from "@/hooks/use-calport";
import { calport, type KitTool } from "@/lib/calport";

const stateBadge: Record<Exclude<KitTool["state"], "missing">, { label: string; variant: "success" | "outline" }> = {
  configured: { label: "Set up", variant: "success" },
  tracked: { label: "Committed file", variant: "outline" },
  skipped: { label: "Skipped", variant: "outline" },
};

interface WorktreeToolsProps {
  box: string;
  location: string;
}

// WorktreeTools sets up each worktree tool (Orca, Cursor, Codex, Superset,
// Herdr) to run the kit's hooks for worktrees it makes in the checkout.
export function WorktreeTools({ box, location }: WorktreeToolsProps) {
  const tools = useLoad(() => calport.kitTools(box, location), [box, location]);
  const [busy, setBusy] = useState<string>();
  const list = tools.data?.tools ?? [];
  const pending = list.filter((t) => t.state === "missing" && !t.opt_in);

  async function setUp(key: string, ids: string[]) {
    setBusy(key);
    try {
      const res = await calport.setUpKitTools(box, location, ids);
      toastManager.add({ title: "Worktree tools set up", description: (res.written ?? []).join(", ") || undefined, type: "success" });
      tools.reload();
    } catch (err) {
      toastManager.add({ title: "Could not set up worktree tools", description: err instanceof Error ? err.message : String(err), type: "error" });
    } finally {
      setBusy(undefined);
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-0.5">
          <span className="font-medium text-sm">Worktree tools</span>
          <span className="text-muted-foreground text-xs">
            Each tool's config goes in the checkout and is excluded locally through{" "}
            <code className="font-mono">.git/info/exclude</code>, so it is never committed.
          </span>
        </div>
        {pending.length > 0 && (
          <Button size="sm" variant="outline" disabled={busy !== undefined} onClick={() => setUp("all", [])}>
            {busy === "all" && <Spinner />}
            Set up all
          </Button>
        )}
      </div>
      {tools.error && <span className="text-destructive-foreground text-xs">{tools.error}</span>}
      {list.length > 0 && (
        <ul className="flex flex-col divide-y rounded-lg border">
          {list.map((t) => (
            <li key={t.tool} className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="flex min-w-0 flex-col gap-0.5">
                <span className="flex flex-wrap items-baseline gap-x-2 text-sm">
                  {t.name}
                  {t.file && <code className="break-all font-mono text-muted-foreground text-xs">{t.file}</code>}
                </span>
                {t.detail && <span className="text-muted-foreground text-xs">{t.detail}</span>}
              </span>
              {t.state === "missing" ? (
                <Button size="sm" variant="outline" disabled={busy !== undefined} onClick={() => setUp(t.tool, [t.tool])}>
                  {busy === t.tool && <Spinner />}
                  Set up
                </Button>
              ) : (
                <Badge variant={stateBadge[t.state].variant} className="shrink-0">
                  {t.state === "configured" && <CheckIcon />}
                  {stateBadge[t.state].label}
                </Badge>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
