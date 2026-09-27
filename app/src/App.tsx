import { ServerIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { AddBoxDialog } from "@/components/add-box-dialog";
import { AppSidebar } from "@/components/app-sidebar";
import { BoxView } from "@/components/box-view";
import { Onboarding } from "@/components/onboarding";
import { SettingsView } from "@/components/settings-view";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { Spinner } from "@/components/ui/spinner";
import { AnchoredToastProvider, ToastProvider, toastManager } from "@/components/ui/toast";
import { useEvents, useLoad, usePoll } from "@/hooks/use-calport";
import { AUTO_AGENT, calport, type Lifecycle } from "@/lib/calport";
import { lifecycleOf, notice } from "@/lib/format";

const ONBOARDED = "calport.onboarded";

function onboarded(): boolean {
  try {
    return localStorage.getItem(ONBOARDED) === "1";
  } catch {
    return false;
  }
}

function autoAgent(): boolean {
  try {
    return localStorage.getItem(AUTO_AGENT) !== "0";
  } catch {
    return true;
  }
}

// ensureStartsAtLogin installs the background agent the first time the app
// runs, so boxes stay connected after a restart without anyone asking for it.
async function ensureStartsAtLogin() {
  // A dev build's sidecar lives in target/, which must not start at login.
  if (import.meta.env.DEV || !autoAgent()) return;
  try {
    const agent = await calport.agentStatus();
    if (!agent.installed) await calport.installAgent();
  } catch {
    // Settings and the checks show it is not installed, with the fix.
  }
}

const SETTINGS = "\u0000settings";

function Shell() {
  const status = useLoad(() => calport.status(), []);
  const networks = useLoad(() => calport.networks(), []);
  usePoll(status.reload, 5_000);
  const [selected, setSelected] = useState<string>();
  const [adding, setAdding] = useState(false);
  const [version, setVersion] = useState(0);
  const [onboarding, setOnboarding] = useState(false);
  const [lifecycle, setLifecycle] = useState<Record<string, Lifecycle>>({});

  useEffect(() => {
    ensureStartsAtLogin();
  }, []);

  const changed = useCallback(() => {
    setVersion((v) => v + 1);
    status.reload();
  }, [status.reload]);

  useEvents((e) => {
    const n = notice(e);
    if (n) toastManager.add(n);
    const lc = lifecycleOf(e);
    if (lc) setLifecycle((m) => ({ ...m, [lc[0]]: lc[1] }));
    changed();
  });

  const boxes = status.data?.boxes ?? [];
  // First run: guide someone with no boxes through setup, and keep the guide
  // up until they finish it even after their first box connects.
  useEffect(() => {
    if (status.data && status.data.boxes.length === 0 && !onboarded()) setOnboarding(true);
  }, [status.data]);
  useEffect(() => {
    if (!selected && boxes.length > 0) setSelected(boxes[0].name);
  }, [boxes, selected]);

  if (!status.data && status.error) {
    return (
      <main className="flex min-h-svh items-center justify-center p-6">
        <Empty>
          <EmptyHeader>
            <EmptyTitle>Calport could not start</EmptyTitle>
            <EmptyDescription>{status.error}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={status.reload}>Try again</Button>
          </EmptyContent>
        </Empty>
      </main>
    );
  }
  if (!status.data) {
    return (
      <main className="flex min-h-svh items-center justify-center">
        <Spinner />
      </main>
    );
  }
  if (onboarding) {
    return (
      <Onboarding
        networks={networks.data ?? []}
        onNetworksChanged={networks.reload}
        onBoxConnected={(box) => {
          setSelected(box);
          changed();
        }}
        onFinish={() => {
          try {
            localStorage.setItem(ONBOARDED, "1");
          } catch {
            // Without storage the guide shows again next time; nothing breaks.
          }
          setOnboarding(false);
        }}
      />
    );
  }

  const box = boxes.find((b) => b.name === selected);
  return (
    <SidebarProvider>
      <AppSidebar
        boxes={boxes}
        networks={networks.data ?? []}
        selected={selected}
        settings={selected === SETTINGS}
        onSelect={setSelected}
        onSettings={() => setSelected(SETTINGS)}
        onAddBox={() => setAdding(true)}
      />
      <SidebarInset>
        {selected === SETTINGS ? (
          <SettingsView status={status.data} networks={networks.data ?? []} onNetworksChanged={networks.reload} onChanged={changed} />
        ) : box ? (
          <BoxView
            key={box.name}
            box={box}
            network={box.network}
            urlPort={status.data.proxy.url_port || status.data.proxy.port}
            forwards={status.data.forwards}
            routes={status.data.routes ?? []}
            version={version}
            lifecycle={lifecycle}
            onChanged={changed}
          />
        ) : (
          <main className="flex min-h-svh items-center justify-center p-6">
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <ServerIcon />
                </EmptyMedia>
                <EmptyTitle>No box selected</EmptyTitle>
                <EmptyDescription>Pick a box on the left, or connect a new one.</EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button onClick={() => setAdding(true)}>Add box</Button>
              </EmptyContent>
            </Empty>
          </main>
        )}
      </SidebarInset>
      <AddBoxDialog
        open={adding}
        onOpenChange={setAdding}
        networks={networks.data ?? []}
        onNetworksChanged={networks.reload}
        onConnected={(name) => {
          setSelected(name);
          changed();
        }}
      />
    </SidebarProvider>
  );
}

export default function App() {
  return (
    <ToastProvider>
      <AnchoredToastProvider>
        <Shell />
      </AnchoredToastProvider>
    </ToastProvider>
  );
}
