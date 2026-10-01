import { EllipsisIcon, RefreshCwIcon, SquareTerminalIcon, StethoscopeIcon, UnlinkIcon } from "lucide-react";
import { useState } from "react";

import { CheckList } from "@/components/check-list";
import { OverviewTab } from "@/components/overview-tab";
import { ServicesTab } from "@/components/services-tab";
import { SharingTab } from "@/components/sharing-tab";
import { StateDot } from "@/components/state-dot";
import { WorktreesTab } from "@/components/worktrees-tab";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogClose,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogDescription, DialogHeader, DialogPanel, DialogPopup, DialogTitle } from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Menu, MenuItem, MenuPopup, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs";
import { toastManager } from "@/components/ui/toast";
import { useLoad } from "@/hooks/use-calport";
import { calport, type BoxStatus, type ForwardStatus, type Lifecycle, type Route } from "@/lib/calport";
import { stateLabel } from "@/lib/format";

interface BoxViewProps {
  box: BoxStatus;
  network?: string;
  urlPort: number;
  forwards: ForwardStatus[];
  routes: Route[];
  version: number;
  lifecycle: Record<string, Lifecycle>;
  onChanged: () => void;
}

export function BoxView({ box, network, urlPort, forwards, routes, version, lifecycle, onChanged }: BoxViewProps) {
  const [tab, setTab] = useState("overview");
  const [forgetting, setForgetting] = useState(false);
  const [checking, setChecking] = useState(false);
  return (
    <div className="flex min-h-svh flex-col">
      <header className="flex items-center gap-3 border-b px-6 py-4">
        <SidebarTrigger />
        <div className="flex min-w-0 flex-1 flex-col">
          <h1 className="flex items-center gap-2 font-heading font-semibold text-lg">
            {box.name}
            <StateDot state={box.state} />
            <span className="font-normal text-muted-foreground text-sm">{stateLabel[box.state]}</span>
          </h1>
          <p className="truncate font-mono text-muted-foreground text-xs">
            {box.address} · key {box.fingerprint.slice(0, 12)}
          </p>
        </div>
        {network && <Badge variant="outline">via {network}</Badge>}
        <Menu>
          <MenuTrigger render={<Button size="icon-sm" variant="ghost" aria-label={`${box.name} options`} />}>
            <EllipsisIcon />
          </MenuTrigger>
          <MenuPopup align="end">
            <MenuItem
              onClick={async () => {
                const id = toastManager.add({ title: `Upgrading ${box.name}…`, type: "loading" });
                try {
                  const out = await calport.upgrade(box.name);
                  toastManager.update(id, { title: out.trim().split("\n").pop() ?? "Upgraded", type: "success" });
                  onChanged();
                } catch (err) {
                  toastManager.update(id, { title: "Upgrade failed", description: err instanceof Error ? err.message : String(err), type: "error" });
                }
              }}
            >
              <RefreshCwIcon />
              Upgrade daemon
            </MenuItem>
            <MenuItem onClick={() => setChecking(true)}>
              <StethoscopeIcon />
              Run checks
            </MenuItem>
            <MenuItem
              onClick={() =>
                calport.openHerdr().catch((err) =>
                  toastManager.add({ title: "Could not open Herdr", description: err instanceof Error ? err.message : String(err), type: "error" }),
                )
              }
            >
              <SquareTerminalIcon />
              Open in Herdr
            </MenuItem>
            <MenuSeparator />
            <MenuItem variant="destructive" onClick={() => setForgetting(true)}>
              <UnlinkIcon />
              Forget this box
            </MenuItem>
          </MenuPopup>
        </Menu>
        <BoxChecks box={box.name} open={checking} onOpenChange={setChecking} />
        <AlertDialog open={forgetting} onOpenChange={setForgetting}>
          <AlertDialogPopup>
            <AlertDialogHeader>
              <AlertDialogTitle>Forget {box.name}?</AlertDialogTitle>
              <AlertDialogDescription>
                This laptop stops connecting to it and its forwards stop working. Reconnecting needs a new pairing. The
                box itself is untouched.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogClose render={<Button variant="ghost" type="button" />}>Cancel</AlertDialogClose>
              <Button
                type="button"
                variant="destructive"
                onClick={() =>
                  calport.forget(box.name).then(
                    () => {
                      setForgetting(false);
                      toastManager.add({ title: `Forgot ${box.name}`, type: "success" });
                      onChanged();
                    },
                    (err) => toastManager.add({ title: "Could not forget the box", description: String(err), type: "error" }),
                  )
                }
              >
                Forget box
              </Button>
            </AlertDialogFooter>
          </AlertDialogPopup>
        </AlertDialog>
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-4 px-6 py-5">
        {box.state === "offline" && (
          <Alert variant="warning">
            <AlertTitle>{box.name} is not answering</AlertTitle>
            <AlertDescription>Calport keeps retrying and reconnects on its own. {box.error}</AlertDescription>
          </Alert>
        )}
        {box.state === "untrusted" && (
          <Alert variant="error">
            <AlertTitle>{box.name} no longer trusts this laptop</AlertTitle>
            <AlertDescription>Its owner revoked access. Pair again with a new link to reconnect.</AlertDescription>
          </Alert>
        )}
        <Tabs value={tab} onValueChange={(v) => setTab(String(v))} className="flex min-h-0 flex-1 flex-col">
          <TabsList>
            <TabsTab value="overview">Overview</TabsTab>
            <TabsTab value="services">Services</TabsTab>
            <TabsTab value="worktrees">Worktrees</TabsTab>
            <TabsTab value="sharing">Sharing</TabsTab>
          </TabsList>
          <TabsPanel value="overview" className="pt-4">
            <OverviewTab box={box.name} version={version} />
          </TabsPanel>
          <TabsPanel value="services" className="pt-4">
            <ServicesTab box={box.name} urlPort={urlPort} forwards={forwards} routes={routes} version={version} onChanged={onChanged} />
          </TabsPanel>
          <TabsPanel value="worktrees" className="pt-4">
            <WorktreesTab box={box.name} urlPort={urlPort} version={version} lifecycle={lifecycle} />
          </TabsPanel>
          <TabsPanel value="sharing" className="pt-4">
            <SharingTab box={box.name} version={version} onChanged={onChanged} />
          </TabsPanel>
        </Tabs>
      </div>
    </div>
  );
}

// BoxChecks asks the box for its own report, loading only while open.
function BoxChecks({ box, open, onOpenChange }: { box: string; open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPopup className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Checks on {box}</DialogTitle>
          <DialogDescription>What calportd sees on the box: its service, tools, Orca, and agents.</DialogDescription>
        </DialogHeader>
        <DialogPanel>{open && <BoxCheckResults box={box} />}</DialogPanel>
      </DialogPopup>
    </Dialog>
  );
}

function BoxCheckResults({ box }: { box: string }) {
  const checks = useLoad(() => calport.doctor(box), [box]);
  if (checks.error) return <p className="text-destructive-foreground text-sm">{checks.error}</p>;
  if (!checks.data) return <Spinner />;
  return <CheckList checks={checks.data} />;
}
