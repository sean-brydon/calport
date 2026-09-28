import { openUrl } from "@tauri-apps/plugin-opener";
import { CheckIcon, CopyIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { toastManager } from "@/components/ui/toast";
import { Badge } from "@/components/ui/badge";
import { useLoad } from "@/hooks/use-calport";
import { calport, type Machine, type Network } from "@/lib/calport";
import { sshTarget } from "@/lib/ssh-target";
import { cn } from "@/lib/utils";
import { boxName } from "@/lib/format";

interface ConnectBoxProps {
  networks: Network[];
  onConnected: (box: string) => void;
  onNetworksChanged: () => void;
}

const LOCAL = { label: "This computer's network", value: "" };

interface NetworkSelectProps {
  networks: Network[];
  value: string;
  onChange: (v: string) => void;
  onNetworksChanged: () => void;
}

function NetworkSelect({ networks, value, onChange, onNetworksChanged }: NetworkSelectProps) {
  const [joining, setJoining] = useState(false);
  const items = [LOCAL, ...networks.map((n) => ({ label: `${n.name}${n.tailnet ? ` (${n.tailnet})` : ""}`, value: n.name }))];
  const selected = items.find((i) => i.value === value) ?? LOCAL;
  if (joining) {
    return (
      <div className="flex flex-col gap-3 rounded-lg border p-4">
        <JoinNetwork
          onJoined={(name) => {
            onNetworksChanged();
            onChange(name);
            setJoining(false);
          }}
        />
        <div>
          <Button type="button" variant="ghost" size="sm" onClick={() => setJoining(false)}>
            Cancel
          </Button>
        </div>
      </div>
    );
  }
  return (
    <Field>
      <FieldLabel>Reach it through</FieldLabel>
      <Select items={items} value={selected} onValueChange={(v) => onChange(v?.value ?? "")}>
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectPopup>
          {items.map((i) => (
            <SelectItem key={i.value || "local"} value={i}>
              {i.label}
            </SelectItem>
          ))}
        </SelectPopup>
      </Select>
      <FieldDescription>
        Is the box on a tailnet this computer is not on?{" "}
        <button type="button" className="underline underline-offset-4" onClick={() => setJoining(true)}>
          Join that tailnet
        </button>
      </FieldDescription>
    </Field>
  );
}

function BoxNameField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const slug = boxName(value);
  return (
    <Field>
      <FieldLabel>Name (optional)</FieldLabel>
      <Input value={value} placeholder="The box's hostname" onChange={(e) => onChange(e.target.value)} />
      <FieldDescription>
        {slug ? (
          <>
            Its URLs look like <code className="font-mono">3000.{slug}.localhost</code>
          </>
        ) : (
          "Used in its URLs, like 3000.NAME.localhost"
        )}
      </FieldDescription>
    </Field>
  );
}

function Progress({ lines, busy }: { lines: string[]; busy: boolean }) {
  if (lines.length === 0) return null;
  return (
    <ol aria-live="polite" className="flex w-full flex-col gap-1.5 rounded-lg border bg-muted/40 p-3 font-mono text-xs">
      {lines.map((line, i) => (
        <li key={i} className="flex items-start gap-2">
          {busy && i === lines.length - 1 ? <Spinner className="mt-px size-3.5" /> : <CheckIcon className="mt-px size-3.5 text-success" />}
          <span className="break-all">{line}</span>
        </li>
      ))}
    </ol>
  );
}

function errorText(err: unknown) {
  return err instanceof Error ? err.message : String(err);
}

interface FormProps {
  networks: Network[];
  network: string;
  setNetwork: (v: string) => void;
  onNetworksChanged: () => void;
  onConnected: (box: string) => void;
}

export function ConnectBox({ networks, onConnected, onNetworksChanged }: ConnectBoxProps) {
  const [network, setNetwork] = useState("");
  const props = { networks, network, setNetwork, onNetworksChanged, onConnected };
  return (
    <Tabs defaultValue="ssh" className="w-full">
      <TabsList>
        <TabsTab value="ssh">I can SSH to it</TabsTab>
        <TabsTab value="link">Install on the box</TabsTab>
      </TabsList>
      <TabsPanel value="ssh" className="pt-4">
        <SSHForm {...props} />
      </TabsPanel>
      <TabsPanel value="link" className="pt-4">
        <LinkForm {...props} />
      </TabsPanel>
    </Tabs>
  );
}

function SSHForm({ networks, network, setNetwork, onNetworksChanged, onConnected }: FormProps) {
  const machines = useLoad(() => calport.discover(network || undefined), [network]);
  const [picked, setPicked] = useState<Machine>();
  const [manual, setManual] = useState(false);
  const [user, setUser] = useState<string>();
  const [host, setHost] = useState("");
  const [name, setName] = useState("");
  const [lines, setLines] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const login = user ?? machines.data?.user ?? "";
  const resolved = manual || !picked ? { host: host.trim(), options: [] } : sshTarget(picked, login, network);
  const target = resolved.host;
  const fallbackName = manual ? "" : (picked?.name ?? "");

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(undefined);
        setLines([]);
        let paired = "";
        const chosenName = boxName(name) || boxName(fallbackName);
        try {
          await calport.addSSHStreaming(
            target,
            (line) => {
              setLines((l) => [...l, line]);
              const m = line.match(/^Paired with (\S+)/);
              if (m) paired = m[1];
            },
            chosenName || undefined,
            network || undefined,
            resolved.options,
          );
          toastManager.add({ title: `Connected ${paired || target}`, type: "success" });
          onConnected(paired || chosenName || target);
        } catch (err) {
          setError(errorText(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <p className="text-muted-foreground text-sm">
        Calport uses your SSH access once to install its small daemon on the box, then pairs with it. After that it
        never needs SSH again. Keys from your SSH agent, like 1Password, work as usual; a new host's fingerprint or a
        password is asked for once.
      </p>
      <NetworkSelect
        networks={networks}
        value={network}
        onChange={(v) => {
          setNetwork(v);
          setPicked(undefined);
        }}
        onNetworksChanged={onNetworksChanged}
      />
      {!manual && (
        <MachinePicker
          machines={machines.data?.machines ?? []}
          loading={machines.loading && !machines.data}
          error={machines.error}
          picked={picked}
          onPick={(m) => {
            setPicked(m);
            setName("");
          }}
          onManual={() => setManual(true)}
        />
      )}
      {manual && (
        <Field>
          <FieldLabel>Host</FieldLabel>
          <Input required value={host} placeholder="dev-alex or alex@203.0.113.5" className="font-mono" onChange={(e) => setHost(e.target.value)} />
          <FieldDescription>
            Anything you can type after <code>ssh</code>.{" "}
            <button type="button" className="underline underline-offset-4" onClick={() => setManual(false)}>
              Pick from the tailnet
            </button>
          </FieldDescription>
        </Field>
      )}
      {!manual && picked && (
        <Field>
          <FieldLabel>Username on {picked.name}</FieldLabel>
          <Input value={login} className="font-mono" onChange={(e) => setUser(e.target.value)} />
          <FieldDescription>
            Connects as <code className="font-mono">{target}</code>
          </FieldDescription>
        </Field>
      )}
      {(manual || picked) && <BoxNameField value={name || (manual ? "" : fallbackName)} onChange={setName} />}
      <Progress lines={lines} busy={busy} />
      {error && <p className="text-destructive-foreground text-sm">{error}</p>}
      <div>
        <Button type="submit" disabled={busy || !target}>
          {busy && <Spinner />}
          Install and pair
        </Button>
      </div>
    </form>
  );
}

interface MachinePickerProps {
  machines: Machine[];
  loading: boolean;
  error?: string;
  picked?: Machine;
  onPick: (m: Machine) => void;
  onManual: () => void;
}

function MachinePicker({ machines, loading, error, picked, onPick, onManual }: MachinePickerProps) {
  const [query, setQuery] = useState("");
  const shown = machines.filter((m) => !query || m.name.toLowerCase().includes(query.toLowerCase()) || m.ip.includes(query));
  return (
    <Field>
      <FieldLabel>Machine</FieldLabel>
      {machines.length > 6 && <Input value={query} placeholder="Search machines" onChange={(e) => setQuery(e.target.value)} />}
      {loading ? (
        <div className="flex items-center gap-2 text-muted-foreground text-sm">
          <Spinner /> Looking for machines…
        </div>
      ) : error ? (
        <p className="text-muted-foreground text-sm">{error}</p>
      ) : (
        <ul role="listbox" aria-label="Machines" className="flex max-h-56 w-full flex-col divide-y overflow-y-auto overscroll-contain rounded-lg border">
          {shown.map((m) => {
            const selectable = m.online && !m.box;
            return (
              <li key={m.ip}>
                <button
                  type="button"
                  role="option"
                  aria-selected={picked?.ip === m.ip}
                  disabled={!selectable}
                  onClick={() => onPick(m)}
                  className={cn(
                    "flex w-full items-center gap-3 px-3 py-2 text-left text-sm hover:bg-accent disabled:cursor-default disabled:opacity-50 disabled:hover:bg-transparent",
                    picked?.ip === m.ip && "bg-accent",
                  )}
                >
                  <span className="flex-1 truncate font-medium">{m.name}</span>
                  <span className="w-32 shrink-0 text-right font-mono text-muted-foreground text-xs">{m.ip}</span>
                  <span className="flex w-16 shrink-0 justify-end">
                    {m.box ? <Badge variant="outline">paired</Badge> : !m.online ? <Badge variant="secondary">offline</Badge> : m.os === "macOS" && <Badge variant="outline">macOS</Badge>}
                  </span>
                </button>
              </li>
            );
          })}
          {shown.length === 0 && <li className="px-3 py-2 text-muted-foreground text-sm">No machines{query ? " match" : " found"}.</li>}
        </ul>
      )}
      <FieldDescription>
        Not listed?{" "}
        <button type="button" className="underline underline-offset-4" onClick={onManual}>
          Enter a host
        </button>
      </FieldDescription>
    </Field>
  );
}

export const INSTALL_COMMAND = "curl -fsSL https://raw.githubusercontent.com/sean-brydon/calport/main/install.sh | sh";

export function CopyCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-center gap-2 rounded-lg border bg-muted/40 py-1.5 pr-1.5 pl-3">
      <code className="min-w-0 flex-1 break-all font-mono text-xs">{command}</code>
      <Button
        type="button"
        size="icon-sm"
        variant="ghost"
        aria-label="Copy command"
        onClick={() => {
          navigator.clipboard.writeText(command);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </Button>
    </div>
  );
}

function LinkForm({ networks, network, setNetwork, onNetworksChanged, onConnected }: FormProps) {
  const [link, setLink] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(undefined);
        try {
          const peer = (await calport.pair(link.trim(), boxName(name) || undefined, network || undefined)) as { name: string };
          toastManager.add({ title: `Connected ${peer.name}`, type: "success" });
          onConnected(peer.name);
        } catch (err) {
          setError(errorText(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <p className="text-muted-foreground text-sm">
        When this computer cannot SSH to the box, or someone else runs it. On the box, run:
      </p>
      <CopyCommand command={INSTALL_COMMAND} />
      <p className="text-muted-foreground text-sm">
        It downloads calportd from GitHub, checks its checksum, starts it at boot, and prints a pairing link. Already
        installed? Run <code className="font-mono">calportd pair</code> for a new link.
      </p>
      <Field>
        <FieldLabel>Pairing link</FieldLabel>
        <Textarea required value={link} placeholder="calport://…" className="font-mono" onChange={(e) => setLink(e.target.value)} />
        <FieldDescription>Single use and valid for 10 minutes.</FieldDescription>
      </Field>
      <BoxNameField value={name} onChange={setName} />
      <NetworkSelect networks={networks} value={network} onChange={setNetwork} onNetworksChanged={onNetworksChanged} />
      {error && <p className="text-destructive-foreground text-sm">{error}</p>}
      <div>
        <Button type="submit" disabled={busy || !link.startsWith("calport://")}>
          {busy && <Spinner />}
          Pair
        </Button>
      </div>
    </form>
  );
}

export function JoinNetwork({ onJoined }: { onJoined: (name: string) => void }) {
  const [name, setName] = useState("personal");
  const [url, setUrl] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  // Not a <form>: this sits inside the Add box forms, and forms cannot nest.
  async function join() {
    setBusy(true);
    setError(undefined);
    setUrl(undefined);
    try {
      await calport.networkLogin(name.trim(), (line) => {
        const found = line.match(/https:\/\/\S+/);
        if (found) setUrl(found[0]);
      });
      toastManager.add({ title: `Joined the ${name} tailnet`, type: "success" });
      onJoined(name.trim());
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="flex flex-col gap-4">
      <p className="text-muted-foreground text-sm">
        Reach boxes on a tailnet this computer is not joined to, like a personal one while your Mac is on work. Calport
        joins it as its own device, separate from the Tailscale app.
      </p>
      <Field>
        <FieldLabel>Network name</FieldLabel>
        <Input value={name} onChange={(e) => setName(e.target.value)} />
        <FieldDescription>Your label for it, e.g. personal.</FieldDescription>
      </Field>
      {busy && (
        <div aria-live="polite" className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-3 text-sm">
          <span className="flex items-center gap-2">
            <Spinner />
            Waiting for you to sign in with the account that owns this tailnet…
          </span>
          {url && (
            <button type="button" className="truncate text-left font-mono text-xs underline underline-offset-4" onClick={() => openUrl(url)}>
              {url}
            </button>
          )}
        </div>
      )}
      {error && <p className="text-destructive-foreground text-sm">{error}</p>}
      <div>
        <Button type="button" disabled={busy || !name} onClick={join}>
          Sign in to the tailnet
        </Button>
      </div>
    </div>
  );
}
