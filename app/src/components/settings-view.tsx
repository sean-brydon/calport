import { GlobeLockIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";

import { CheckList } from "@/components/check-list";
import { JoinNetwork } from "@/components/connect-box";
import { HerdrCard } from "@/components/herdr-card";
import { ToolIntegrations } from "@/components/tool-integrations";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardDescription, CardHeader, CardPanel, CardTitle } from "@/components/ui/card";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { toastManager } from "@/components/ui/toast";
import type { AppUpdate } from "@/hooks/use-app-update";
import { useLoad } from "@/hooks/use-calport";
import { appVersion } from "@/lib/app-update";
import { AUTO_AGENT, calport, type Network, type Status } from "@/lib/calport";

interface SettingsViewProps {
  status: Status;
  networks: Network[];
  appUpdate: AppUpdate;
  onNetworksChanged: () => void;
  onChanged: () => void;
}

function failure(title: string, err: unknown) {
  toastManager.add({ title, description: err instanceof Error ? err.message : String(err), type: "error" });
}

export function SettingsView({ status, networks, appUpdate, onNetworksChanged, onChanged }: SettingsViewProps) {
  const checks = useLoad(() => calport.doctor(), [status.proxy.url_port, status.routes.length]);
  return (
    <div className="flex min-h-svh flex-col">
      <header className="flex items-center gap-3 border-b px-6 py-4">
        <SidebarTrigger />
        <h1 className="font-heading font-semibold text-lg">Settings</h1>
      </header>
      <div className="flex max-w-3xl flex-col gap-6 px-6 py-5">
        <Updates appUpdate={appUpdate} />
        <StartAtLogin onChanged={checks.reload} />
        <ShortURLs urlPort={status.proxy.url_port || status.proxy.port} onChanged={onChanged} />
        <Routes status={status} onChanged={onChanged} />
        <Networks networks={networks} onNetworksChanged={onNetworksChanged} />
        <HerdrCard />
        <Card>
          <CardHeader>
            <CardTitle>Agents on this computer</CardTitle>
            <CardDescription>Hear when an agent finishes or needs you, and let agents use Calport themselves.</CardDescription>
          </CardHeader>
          <CardPanel>
            <ToolIntegrations />
          </CardPanel>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Checks</CardTitle>
            <CardDescription>What Calport sees on this computer. It never changes anything by checking.</CardDescription>
            <CardAction>
              <Button variant="outline" size="sm" disabled={checks.loading} onClick={checks.reload}>
                {checks.loading && <Spinner />}
                Check again
              </Button>
            </CardAction>
          </CardHeader>
          <CardPanel>
            {checks.error ? <p className="text-destructive-foreground text-sm">{checks.error}</p> : checks.data ? <CheckList checks={checks.data} /> : <Spinner />}
          </CardPanel>
        </Card>
      </div>
    </div>
  );
}

function Updates({ appUpdate }: { appUpdate: AppUpdate }) {
  const version = useLoad(() => appVersion(), []);
  const { update, checking, installing, error, checkNow, install } = appUpdate;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Updates
          {update ? <Badge variant="info">{update.version} available</Badge> : <Badge variant="secondary">{version.data ?? "…"}</Badge>}
        </CardTitle>
        <CardDescription>
          {update
            ? "A signed update is ready. Installing restarts the app; boxes, forwards and URLs keep running."
            : error
              ? `Could not check for updates: ${error}`
              : "Calport checks for signed updates when it starts and every few hours. After updating, it offers to upgrade your boxes too."}
        </CardDescription>
        <CardAction>
          {update ? (
            <Button size="sm" disabled={installing} onClick={install}>
              {installing && <Spinner />}
              Restart to update
            </Button>
          ) : (
            <Button variant="outline" size="sm" disabled={checking} onClick={checkNow}>
              {checking && <Spinner />}
              Check now
            </Button>
          )}
        </CardAction>
      </CardHeader>
    </Card>
  );
}

function StartAtLogin({ onChanged }: { onChanged: () => void }) {
  const agent = useLoad(() => calport.agentStatus(), []);
  const [busy, setBusy] = useState(false);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Start at login</CardTitle>
        <CardDescription>
          Keeps boxes connected and URLs working with this window closed, and restarts Calport if it ever stops. Runs as
          your user only.
        </CardDescription>
        <CardAction>
          <Switch
            aria-label="Start at login"
            checked={agent.data?.installed ?? false}
            disabled={busy || !agent.data}
            onCheckedChange={async (on) => {
              setBusy(true);
              try {
                if (on) await calport.installAgent();
                else await calport.uninstallAgent();
                try {
                  // Turning it off is a choice the app must not undo at next launch.
                  localStorage.setItem(AUTO_AGENT, on ? "1" : "0");
                } catch {
                  // Without storage the app may reinstall it at next launch.
                }
                agent.reload();
                onChanged();
              } catch (err) {
                failure("Could not change start at login", err);
              } finally {
                setBusy(false);
              }
            }}
          />
        </CardAction>
      </CardHeader>
    </Card>
  );
}

function ShortURLs({ urlPort, onChanged }: { urlPort: number; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const on = urlPort === 80;
  async function change(remove: boolean) {
    setBusy(true);
    try {
      await (remove ? calport.removePort80() : calport.setupPort80());
      toastManager.add({ title: remove ? "URLs include the port again" : "Short URLs are on", type: "success" });
      onChanged();
    } catch (err) {
      failure(remove ? "Could not remove short URLs" : "Could not set up short URLs", err);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Short URLs
          {on ? <Badge variant="success">On</Badge> : <Badge variant="secondary">Off</Badge>}
        </CardTitle>
        <CardDescription>
          {on ? (
            <>
              Dev servers open at <code className="font-mono">http://3000.devl.localhost/</code>, with no port.
            </>
          ) : (
            <>
              URLs include <code className="font-mono">:{urlPort}</code>. Short URLs redirect port 80 on this computer's
              loopback address to Calport, so they read <code className="font-mono">http://3000.devl.localhost/</code>.
              macOS asks for your password once; nothing leaves this computer.
            </>
          )}
        </CardDescription>
        <CardAction>
          <Button variant={on ? "outline" : "default"} size="sm" disabled={busy} onClick={() => change(on)}>
            {busy && <Spinner />}
            {on ? "Remove" : "Set up"}
          </Button>
        </CardAction>
      </CardHeader>
    </Card>
  );
}

function Routes({ status, onChanged }: { status: Status; onChanged: () => void }) {
  const boxItems = status.boxes.map((b) => ({ label: b.name, value: b.name }));
  const [pattern, setPattern] = useState("");
  const [box, setBox] = useState(boxItems[0]);
  const [port, setPort] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Routes</CardTitle>
        <CardDescription>
          Send your own hostnames to a port on a box, unchanged. Use this for a box-side router that picks the worktree
          by hostname, like a Cal.com worktree proxy.
        </CardDescription>
      </CardHeader>
      <CardPanel className="flex flex-col gap-4">
        {status.routes.length > 0 && (
          <ul className="flex flex-col divide-y rounded-lg border">
            {status.routes.map((r) => (
              <li key={r.pattern} className="flex items-center gap-3 px-3 py-2">
                <code className="flex-1 truncate font-mono text-sm">{r.pattern}</code>
                <span className="font-mono text-muted-foreground text-sm">
                  {r.box}:{r.port}
                </span>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Remove ${r.pattern}`}
                  onClick={() => calport.removeRoute(r.pattern).then(onChanged, (err) => failure("Could not remove the route", err))}
                >
                  <Trash2Icon />
                </Button>
              </li>
            ))}
          </ul>
        )}
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!box) return;
            setBusy(true);
            try {
              await calport.addRoute(pattern.trim(), box.value, Number(port));
              setPattern("");
              setPort("");
              onChanged();
            } catch (err) {
              failure("Could not add the route", err);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field className="min-w-56 flex-1">
            <FieldLabel>Hostnames</FieldLabel>
            <Input required value={pattern} placeholder="*.cal.localhost" className="font-mono" onChange={(e) => setPattern(e.target.value)} />
          </Field>
          <Field className="w-32">
            <FieldLabel>Box</FieldLabel>
            <Select items={boxItems} value={box} onValueChange={(v) => v && setBox(v)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectPopup>
                {boxItems.map((b) => (
                  <SelectItem key={b.value} value={b}>
                    {b.label}
                  </SelectItem>
                ))}
              </SelectPopup>
            </Select>
          </Field>
          <Field className="w-24">
            <FieldLabel>Port</FieldLabel>
            <Input required inputMode="numeric" value={port} placeholder="18080" className="font-mono" onChange={(e) => setPort(e.target.value)} />
          </Field>
          <Button type="submit" variant="outline" disabled={busy || !box || !pattern || !port}>
            <PlusIcon />
            Add route
          </Button>
        </form>
        <p className="text-muted-foreground text-xs">Only names ending in .localhost, so nothing outside this computer is affected.</p>
      </CardPanel>
    </Card>
  );
}

function Networks({ networks, onNetworksChanged }: { networks: Network[]; onNetworksChanged: () => void }) {
  const [joining, setJoining] = useState(false);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Networks</CardTitle>
        <CardDescription>
          Tailnets Calport joins on its own, separate from the Tailscale app, to reach boxes on a tailnet this computer
          is not on. A box on your current tailnet needs none.
        </CardDescription>
        {!joining && (
          <CardAction>
            <Button variant="outline" size="sm" onClick={() => setJoining(true)}>
              <PlusIcon />
              Join a tailnet
            </Button>
          </CardAction>
        )}
      </CardHeader>
      {(networks.length > 0 || joining) && (
        <CardPanel className="flex flex-col gap-4">
          {networks.length > 0 && (
            <ul className="flex flex-col divide-y rounded-lg border">
              {networks.map((n) => (
                <li key={n.name} className="flex items-center gap-3 px-3 py-2">
                  <GlobeLockIcon className="size-4 text-muted-foreground" />
                  <span className="flex-1 font-medium text-sm">{n.name}</span>
                  <span className="text-muted-foreground text-sm">{n.tailnet}</span>
                  <Badge variant={n.state === "Running" ? "success" : "secondary"}>{n.state === "Running" ? "Connected" : n.state}</Badge>
                </li>
              ))}
            </ul>
          )}
          {joining && (
            <div className="flex flex-col gap-3 rounded-lg border p-4">
              <JoinNetwork
                onJoined={() => {
                  setJoining(false);
                  onNetworksChanged();
                }}
              />
              <div>
                <Button variant="ghost" size="sm" onClick={() => setJoining(false)}>
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </CardPanel>
      )}
    </Card>
  );
}
