import { CheckIcon, SquareTerminalIcon } from "lucide-react";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardDescription, CardHeader, CardPanel, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { useLoad } from "@/hooks/use-calport";
import { calport, type HerdrBox, type HerdrReport, type HerdrState } from "@/lib/calport";

function failure(title: string, err: unknown) {
  toastManager.add({ title, description: err instanceof Error ? err.message : String(err), type: "error" });
}

// HerdrCard puts every box in this computer's Herdr window. It is opt-in:
// nothing here changes how Orca or anything else reaches a box.
export function HerdrCard() {
  const report = useLoad(() => calport.herdr(), []);
  const [busy, setBusy] = useState<string>();
  const r = report.data;

  const act = async (key: string, title: string, work: () => Promise<unknown>) => {
    setBusy(key);
    try {
      await work();
    } catch (err) {
      failure(title, err);
    } finally {
      setBusy(undefined);
      report.reload();
    }
  };
  const addable = r?.boxes.filter((b) => b.state === "add") ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Herdr
          {r?.version && <Badge variant="secondary">{r.version}</Badge>}
        </CardTitle>
        <CardDescription>{describe(r)}</CardDescription>
        {r?.supported && (
          <CardAction className="flex gap-2">
            {addable.length > 1 && (
              <Button variant="outline" size="sm" disabled={busy !== undefined} onClick={() => act("all", "Could not add the boxes to Herdr", () => calport.herdrSetup())}>
                {busy === "all" && <Spinner />}
                Add all
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => calport.openHerdr().catch((err) => failure("Could not open Herdr", err))}>
              <SquareTerminalIcon />
              Open Herdr
            </Button>
          </CardAction>
        )}
      </CardHeader>
      {(report.error || !r || r.boxes.length > 0) && (
        <CardPanel>
          {report.error ? (
            <p className="text-destructive-foreground text-sm">{report.error}</p>
          ) : !r ? (
            <Spinner />
          ) : (
            <ul className="flex flex-col divide-y rounded-lg border">
              {r.boxes.map((b) => (
                <li key={b.box} className="flex items-center justify-between gap-4 px-4 py-3">
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span className="flex items-center gap-2 font-medium">
                      {b.box}
                      {b.version && <span className="font-mono font-normal text-muted-foreground text-xs">{b.version}</span>}
                    </span>
                    <span className="text-muted-foreground text-sm">{b.detail ?? stateText[b.state]}</span>
                  </span>
                  <BoxAction b={b} supported={r.supported} busy={busy} act={act} />
                </li>
              ))}
            </ul>
          )}
        </CardPanel>
      )}
    </Card>
  );
}

function describe(r?: HerdrReport): string {
  if (!r) return "Every box in one Herdr window, next to this computer.";
  if (!r.installed) return "Install Herdr on this computer (herdr.dev) to see every box in one Herdr window.";
  if (!r.supported) return `Herdr ${r.version} on this computer predates saved machines; run herdr update in a terminal.`;
  return "Every box in one Herdr window, next to this computer. Calport writes how SSH reaches each box as calport-<box>; your keys stay in ~/.ssh/config.";
}

const stateText: Record<HerdrBox["state"], string> = {
  ready: "In your Herdr.",
  add: "Ready to add.",
  ssh: "SSH does not log in without a prompt.",
  update: "Its Herdr is too old for saved machines.",
  packaged: "Its Herdr is too old and belongs to a package manager.",
  missing: "Herdr is not installed there.",
  upgrade: "Its calportd cannot report Herdr yet.",
  offline: "Offline.",
  error: "Something went wrong.",
};

interface BoxActionProps {
  b: HerdrBox;
  supported: boolean;
  busy?: string;
  act: (key: string, title: string, work: () => Promise<unknown>) => Promise<void>;
}

function BoxAction({ b, supported, busy, act }: BoxActionProps) {
  if (b.state === "ready") {
    return (
      <Badge variant="success">
        <CheckIcon />
        In Herdr
      </Badge>
    );
  }
  const actions: Partial<Record<HerdrState, { label: string; title: string; work: () => Promise<unknown> }>> = {
    update: {
      label: "Update Herdr",
      title: `Could not update Herdr on ${b.box}`,
      work: async () => {
        const u = await calport.herdrUpdate(b.box);
        toastManager.add({
          title: `Updated Herdr on ${b.box}`,
          description: u.stopped.length ? `Stopped its idle sessions (${u.stopped.join(", ")}); they start again when something attaches.` : undefined,
          type: "success",
        });
      },
    },
    upgrade: { label: "Upgrade daemon", title: `Could not upgrade ${b.box}`, work: () => calport.upgrade(b.box) },
    // Adding needs this computer's Herdr to know saved machines.
    ...(supported && {
      add: { label: "Add", title: `Could not add ${b.box} to Herdr`, work: () => calport.herdrSetup([b.box]) },
      ssh: { label: "Try again", title: `${b.box} is still not reachable`, work: () => calport.herdrSetup([b.box]) },
    }),
  };
  const action = actions[b.state];
  if (!action) return null;
  return (
    <Button variant="outline" size="sm" className="shrink-0" disabled={busy !== undefined} onClick={() => act(b.box, action.title, action.work)}>
      {busy === b.box && <Spinner />}
      {action.label}
    </Button>
  );
}
