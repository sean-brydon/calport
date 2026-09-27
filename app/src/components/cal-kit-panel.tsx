import { CheckIcon, SparklesIcon } from "lucide-react";
import { useState } from "react";

import { CopyCommand } from "@/components/connect-box";
import {
  AlertDialog,
  AlertDialogClose,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { calport, KIT_ARCHIVE_HOOK, KIT_SETUP_HOOK, type KitStatus, type Location } from "@/lib/calport";

interface CalKitPanelProps {
  box: string;
  location: Location;
  kit: KitStatus;
  hasOrca: boolean;
  onInstalled: () => void;
}

// CalKitPanel offers the Cal.com worktree kit for a Cal.com location, and
// once it is in, says what Orca still needs to run it for worktrees made there.
export function CalKitPanel({ box, location, kit, hasOrca, onInstalled }: CalKitPanelProps) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const servesThis = kit.installed && kit.config.root === location.path;

  async function install() {
    setBusy(true);
    try {
      const { kit: res } = await calport.installKit(box, location.name);
      toastManager.add({
        title: `Cal.com worktrees are set up on ${box}`,
        description: [`URLs: ${res.pattern}`, ...(res.notes ?? [])].join(" "),
        type: "success",
      });
      setConfirming(false);
      onInstalled();
    } catch (err) {
      toastManager.add({ title: "Could not set up Cal.com worktrees", description: err instanceof Error ? err.message : String(err), type: "error" });
    } finally {
      setBusy(false);
    }
  }

  if (kit.installed && !servesThis) return null;

  if (!kit.installed) {
    return (
      <div className="flex flex-col gap-3 rounded-lg border border-dashed p-4">
        <div className="flex items-start gap-3">
          <SparklesIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <div className="flex flex-col gap-1">
            <span className="font-medium text-sm">Give every worktree its own running app</span>
            <span className="text-muted-foreground text-sm">
              The Cal.com kit sets up each new worktree with its own port, a copy of the dev database, its own .env and
              a URL like <code className="font-mono">fix-login-a1b2c3.{box}.cal.localhost</code>, then stops it when the
              worktree is archived.
            </span>
          </div>
        </div>
        <div>
          <Button size="sm" onClick={() => setConfirming(true)}>
            Set up on {box}
          </Button>
        </div>
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogPopup>
            <AlertDialogHeader>
              <AlertDialogTitle>Set up Cal.com worktrees on {box}?</AlertDialogTitle>
              <AlertDialogDescription render={<div />} className="flex flex-col gap-2">
                <span>Calport installs, as your user on {box}:</span>
                <ul className="list-disc pl-5">
                  <li>
                    the kit's scripts in <code className="font-mono">~/.local/share/cal-worktrees</code>, linked as{" "}
                    <code className="font-mono">cal-worktree</code>, <code className="font-mono">cal-archive</code> and{" "}
                    <code className="font-mono">cal-setup</code> in <code className="font-mono">~/.local/bin</code>
                  </li>
                  <li>a user service that routes worktree URLs, on the box's loopback only</li>
                  {hasOrca && <li>a user service that stops worktrees Orca archives and restarts ones it restores</li>}
                  <li>a route on this computer, so those URLs open here</li>
                </ul>
                <span>
                  Worktree setup creates a database per worktree on your local Postgres. Nothing needs root, and any
                  script of the same name already there is kept as a backup.
                </span>
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogClose render={<Button variant="ghost" type="button" />}>Cancel</AlertDialogClose>
              <Button type="button" disabled={busy} onClick={install}>
                {busy && <Spinner />}
                Set up
              </Button>
            </AlertDialogFooter>
          </AlertDialogPopup>
        </AlertDialog>
      </div>
    );
  }

  if (hasOrca && location.scripts.from !== "orca") {
    return (
      <div className="flex flex-col gap-3 rounded-lg border p-4">
        <div className="flex flex-col gap-1">
          <span className="font-medium text-sm">One step left: add the hooks in Orca</span>
          <span className="text-muted-foreground text-sm">
            Worktrees made from Calport are set up already. For ones made in Orca's app, open Orca's settings, choose
            the {location.name} repository, and under Worktree Hooks set:
          </span>
        </div>
        <div className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-2 text-sm">
          <span className="text-muted-foreground">Setup</span>
          <CopyCommand command={KIT_SETUP_HOOK} />
          <span className="text-muted-foreground">Archive</span>
          <CopyCommand command={KIT_ARCHIVE_HOOK} />
        </div>
        <span className="text-muted-foreground text-xs">
          Turn on Run by default, and Wait for setup to complete before starting agent. Calport notices once they are
          saved.
        </span>
      </div>
    );
  }

  return (
    <p className="flex items-center gap-1.5 text-muted-foreground text-xs">
      <CheckIcon className="size-3.5 text-success" />
      Cal.com kit: each worktree runs at <code className="font-mono">{kit.pattern}</code>
      {location.scripts.from === "orca" && ", with Orca's hooks"}
    </p>
  );
}
