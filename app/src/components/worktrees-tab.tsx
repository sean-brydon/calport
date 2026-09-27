import { openUrl } from "@tauri-apps/plugin-opener";
import {
  EllipsisIcon,
  ExternalLinkIcon,
  FolderGit2Icon,
  FolderIcon,
  GitBranchIcon,
  PanelsTopLeftIcon,
  PlusIcon,
  ScrollTextIcon,
  Trash2Icon,
} from "lucide-react";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardDescription, CardHeader, CardPanel, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogClose,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "@/components/ui/dialog";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Menu, MenuGroup, MenuGroupLabel, MenuItem, MenuPopup, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { toastManager } from "@/components/ui/toast";
import { useLoad, usePoll } from "@/hooks/use-calport";
import { CalKitPanel } from "@/components/cal-kit-panel";
import {
  calport,
  type KitWorktree,
  kitURL,
  type Lifecycle,
  type Location,
  type Service,
  serviceURL,
  type Worktree,
  worktreeURL,
} from "@/lib/calport";

interface WorktreesTabProps {
  box: string;
  urlPort: number;
  version: number;
  lifecycle: Record<string, Lifecycle>;
}

function failure(title: string, err: unknown) {
  toastManager.add({ title, description: err instanceof Error ? err.message : String(err), type: "error" });
}

export function WorktreesTab({ box, urlPort, version, lifecycle }: WorktreesTabProps) {
  const locations = useLoad(() => calport.locations(box), [box, version]);
  const services = useLoad(() => calport.services(box), [box, version]);
  const info = useLoad(() => calport.info(box), [box]);
  const kit = useLoad(() => calport.kit(box), [box, version]);
  usePoll(services.reload, 10_000);
  const [adding, setAdding] = useState(false);
  const [creatingIn, setCreatingIn] = useState<Location>();
  const [editingScripts, setEditingScripts] = useState<Location>();
  const [importing, setImporting] = useState(false);
  const tools = info.data?.tools ?? [];
  const hasOrca = tools.includes("orca");

  const list = locations.data ?? [];
  const servicesFor = (loc: Location, wt: Worktree) =>
    (services.data ?? []).filter((s) => s.location === loc.name && (s.worktree === wt.name || (wt.main && s.main)));

  async function importFromOrca() {
    setImporting(true);
    try {
      const out = await calport.importOrca(box);
      toastManager.add({ title: "Imported from Orca", description: out.trim(), type: "success" });
      locations.reload();
    } catch (err) {
      failure("Could not import from Orca", err);
    } finally {
      setImporting(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <p className="text-muted-foreground text-sm">Repositories on {box}. Worktrees made by Orca, Herdr, agents, or git all show up here.</p>
        <div className="flex shrink-0 gap-2">
          {hasOrca && (
            <Button variant="outline" size="sm" disabled={importing} onClick={importFromOrca}>
              {importing ? <Spinner /> : <PanelsTopLeftIcon />}
              Import from Orca
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            <PlusIcon />
            Add location
          </Button>
        </div>
      </div>

      {locations.error && <p className="text-destructive-foreground text-sm">{locations.error}</p>}
      {!locations.loading && list.length === 0 && !locations.error && (
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <FolderGit2Icon />
            </EmptyMedia>
            <EmptyTitle>No locations yet</EmptyTitle>
            <EmptyDescription>
              A location is a repository on {box}. {hasOrca ? "Import the ones Orca already knows, or add one by path." : "Add one by path."}
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex-row">
            {hasOrca && <Button onClick={importFromOrca}>Import from Orca</Button>}
            <Button variant="outline" onClick={() => setAdding(true)}>
              Add location
            </Button>
          </EmptyContent>
        </Empty>
      )}

      {list.map((loc) => (
        <Card key={loc.name}>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              {loc.repo ? <FolderGit2Icon className="size-4" /> : <FolderIcon className="size-4" />}
              {loc.name}
            </CardTitle>
            <CardDescription className="flex flex-col gap-0.5">
              <span className="font-mono text-xs">{loc.path}</span>
              {loc.scripts.setup && (
                <span className="text-xs">
                  New worktrees run <code className="font-mono">{loc.scripts.setup}</code>
                  {loc.scripts.from === "orca" ? " (from Orca)" : ""}
                </span>
              )}
            </CardDescription>
            <CardAction className="flex gap-2">
              {loc.repo && (
                <Button variant="outline" size="sm" onClick={() => setCreatingIn(loc)}>
                  <GitBranchIcon />
                  New worktree
                </Button>
              )}
              <Menu>
                <MenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`${loc.name} options`} />}>
                  <EllipsisIcon />
                </MenuTrigger>
                <MenuPopup align="end">
                  <MenuItem onClick={() => setEditingScripts(loc)}>
                    <ScrollTextIcon />
                    Setup & archive scripts
                  </MenuItem>
                  <MenuSeparator />
                  <MenuItem
                    variant="destructive"
                    onClick={() =>
                      calport.removeLocation(box, loc.name).then(
                        () => {
                          toastManager.add({ title: `Removed ${loc.name}`, description: "Its files are untouched.", type: "success" });
                          locations.reload();
                        },
                        (err) => failure("Could not remove the location", err),
                      )
                    }
                  >
                    <Trash2Icon />
                    Remove location
                  </MenuItem>
                </MenuPopup>
              </Menu>
            </CardAction>
          </CardHeader>
          {loc.cal && kit.data && (
            <CardPanel className="pb-3">
              <CalKitPanel box={box} location={loc} kit={kit.data} hasOrca={hasOrca} onInstalled={() => {
                kit.reload();
                locations.reload();
              }} />
            </CardPanel>
          )}
          {loc.repo && (
            <CardPanel className="flex flex-col divide-y">
              {(loc.worktrees ?? []).map((wt) => (
                <WorktreeRow
                  key={wt.path}
                  box={box}
                  loc={loc}
                  wt={wt}
                  urlPort={urlPort}
                  services={servicesFor(loc, wt)}
                  tools={tools}
                  lifecycle={lifecycle[`${box}:${wt.path}`]}
                  kit={kit.data?.worktrees?.find((k) => k.path === wt.path)}
                  onRemoved={locations.reload}
                />
              ))}
            </CardPanel>
          )}
        </Card>
      ))}

      <AddLocationDialog box={box} open={adding} onOpenChange={setAdding} onDone={locations.reload} />
      <NewWorktreeDialog
        box={box}
        location={creatingIn}
        defaultProvider={hasOrca ? "orca" : "git"}
        tools={tools}
        onClose={() => setCreatingIn(undefined)}
        onDone={locations.reload}
      />
      <ScriptsDialog box={box} location={editingScripts} onClose={() => setEditingScripts(undefined)} onDone={locations.reload} />
    </div>
  );
}

interface WorktreeRowProps {
  box: string;
  loc: Location;
  wt: Worktree;
  urlPort: number;
  services: Service[];
  tools: string[];
  lifecycle?: Lifecycle;
  kit?: KitWorktree;
  onRemoved: () => void;
}

function WorktreeRow({ box, loc, wt, urlPort, services, tools, lifecycle, kit, onRemoved }: WorktreeRowProps) {
  // A kit worktree's app redirects to its kit hostname, so link that one.
  const running = services.length > 0 || kit?.active === true;
  const primary = kit ? kitURL(kit.host, urlPort) : worktreeURL(wt.name, loc.name, box, urlPort, wt.main);
  const others = kit ? services.filter((s) => s.port !== kit.port) : services.slice(1);
  const handoffs = tools.filter((t): t is "orca" | "herdr" => t === "orca" || t === "herdr");
  const agents = tools.filter((t) => t === "claude" || t === "codex");

  async function open(tool: "orca" | "herdr", agent?: string) {
    try {
      await calport.openWorktree(box, loc.name, wt.name, tool, agent);
      toastManager.add({ title: `Opened ${wt.name} in ${tool === "orca" ? "Orca" : "Herdr"}`, description: agent ? `Starting ${agent}` : undefined, type: "success" });
    } catch (err) {
      failure(`Could not open in ${tool}`, err);
    }
  }

  return (
    <div className="flex items-center gap-3 py-2.5 first:pt-0 last:pb-0">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex flex-wrap items-center gap-2 font-medium text-sm">
          {wt.name}
          {wt.main && <Badge variant="outline">main checkout</Badge>}
          {lifecycle?.state === "started" && (
            <Badge variant="info">
              <Spinner className="size-3" />
              {lifecycle.kind === "setup" ? "Setting up" : "Archiving"}
            </Badge>
          )}
          {lifecycle?.state === "failed" && (
            <Badge variant="error" title={lifecycle.log}>
              {lifecycle.kind === "setup" ? "Setup failed" : "Archive failed"}
            </Badge>
          )}
        </span>
        <span className="truncate font-mono text-muted-foreground text-xs">{wt.branch ?? `detached at ${wt.head}`}</span>
        {running && (
          <span className="flex flex-wrap items-center gap-1.5">
            <Button size="sm" variant="secondary" className="h-6 px-2 text-xs" onClick={() => openUrl(primary)}>
              <ExternalLinkIcon />
              {primary.replace(/^http:\/\//, "").replace(/\/$/, "")}
            </Button>
            {kit && (
              <Button size="sm" variant="ghost" className="h-6 px-2 text-xs" onClick={() => openUrl(`${primary}__worktree/logs`)}>
                Logs
              </Button>
            )}
            {others.map((s) => (
              <Button key={s.port} size="sm" variant="ghost" className="h-6 px-2 font-mono text-xs" title={s.process} onClick={() => openUrl(serviceURL(s.port, box, urlPort))}>
                :{s.port}
              </Button>
            ))}
          </span>
        )}
      </div>
      {handoffs.length > 0 && (
        <Menu>
          <MenuTrigger render={<Button variant="outline" size="sm" />}>
            <PanelsTopLeftIcon />
            Open in…
          </MenuTrigger>
          <MenuPopup align="end">
            {handoffs.map((tool, i) => (
              <MenuGroup key={tool}>
                {i > 0 && <MenuSeparator />}
                <MenuGroupLabel>{tool === "orca" ? "Orca" : "Herdr"}</MenuGroupLabel>
                <MenuItem onClick={() => open(tool)}>Open worktree</MenuItem>
                {agents.map((agent) => (
                  <MenuItem key={agent} onClick={() => open(tool, agent)}>
                    Start {agent === "claude" ? "Claude Code" : "Codex"} here
                  </MenuItem>
                ))}
              </MenuGroup>
            ))}
          </MenuPopup>
        </Menu>
      )}
      {!wt.main && (
        <Menu>
          <MenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`${wt.name} options`} />}>
            <EllipsisIcon />
          </MenuTrigger>
          <MenuPopup align="end">
            <MenuItem
              variant="destructive"
              onClick={() =>
                calport.removeWorktree(box, loc.name, wt.name).then(
                  () => {
                    toastManager.add({
                      title: loc.scripts.archive ? `Archiving ${wt.name}` : `Removed ${wt.name}`,
                      description: loc.scripts.archive ? `Runs ${loc.scripts.archive}, then removes it.` : undefined,
                      type: "success",
                    });
                    onRemoved();
                  },
                  (err) => failure("Could not remove the worktree", err),
                )
              }
            >
              <Trash2Icon />
              Archive and remove
            </MenuItem>
          </MenuPopup>
        </Menu>
      )}
    </div>
  );
}

function AddLocationDialog({ box, open, onOpenChange, onDone }: { box: string; open: boolean; onOpenChange: (open: boolean) => void; onDone: () => void }) {
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPopup>
        <DialogHeader>
          <DialogTitle>Add a location on {box}</DialogTitle>
          <DialogDescription>A repository or folder on the box.</DialogDescription>
        </DialogHeader>
        <form
          className="contents"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            try {
              const loc = await calport.addLocation(box, name.trim(), path.trim());
              toastManager.add({ title: `Added ${loc.name}`, description: loc.path, type: "success" });
              setName("");
              setPath("");
              onDone();
              onOpenChange(false);
            } catch (err) {
              failure("Could not add the location", err);
            } finally {
              setBusy(false);
            }
          }}
        >
          <DialogPanel className="flex flex-col gap-4">
            <Field>
              <FieldLabel>Name</FieldLabel>
              <Input required value={name} placeholder="cal" onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>Path on {box}</FieldLabel>
              <Input required value={path} placeholder="~/work/cal" className="font-mono" onChange={(e) => setPath(e.target.value)} />
              <FieldDescription>~ is the home directory on the box.</FieldDescription>
            </Field>
          </DialogPanel>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" type="button" />}>Cancel</DialogClose>
            <Button type="submit" disabled={busy || !name || !path}>
              Add location
            </Button>
          </DialogFooter>
        </form>
      </DialogPopup>
    </Dialog>
  );
}

const providerItems = [
  { label: "Orca", value: "orca" },
  { label: "Herdr", value: "herdr" },
  { label: "git", value: "git" },
];
const agentItems = [
  { label: "No agent", value: "" },
  { label: "Claude Code", value: "claude" },
  { label: "Codex", value: "codex" },
];

interface NewWorktreeDialogProps {
  box: string;
  location?: Location;
  defaultProvider: string;
  tools: string[];
  onClose: () => void;
  onDone: () => void;
}

function NewWorktreeDialog({ box, location, defaultProvider, tools, onClose, onDone }: NewWorktreeDialogProps) {
  const [name, setName] = useState("");
  const [base, setBase] = useState("main");
  const [provider, setProvider] = useState<string>();
  const [agent, setAgent] = useState(agentItems[0]);
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const available = providerItems.filter((p) => p.value === "git" || tools.includes(p.value));
  const chosen = available.find((p) => p.value === (provider ?? defaultProvider)) ?? available[available.length - 1];
  const willRunSetup = chosen.value === "orca" ? "Orca runs its setup script" : location?.scripts.setup ? `Runs ${location.scripts.setup} after creating it` : undefined;
  return (
    <Dialog open={location !== undefined} onOpenChange={(open) => !open && onClose()}>
      <DialogPopup>
        <DialogHeader>
          <DialogTitle>New worktree in {location?.name}</DialogTitle>
          <DialogDescription>A separate checkout on a new branch, so work can happen in parallel.</DialogDescription>
        </DialogHeader>
        <form
          className="contents"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!location) return;
            setBusy(true);
            try {
              const wt = await calport.newWorktree(box, location.name, name.trim(), {
                base: base.trim() || undefined,
                provider: chosen.value,
                agent: chosen.value === "orca" ? agent.value || undefined : undefined,
                prompt: chosen.value === "orca" && agent.value ? prompt.trim() || undefined : undefined,
              });
              toastManager.add({ title: `Created ${location.name}/${wt.name}`, description: willRunSetup, type: "success" });
              setName("");
              setPrompt("");
              onDone();
              onClose();
            } catch (err) {
              failure("Could not create the worktree", err);
            } finally {
              setBusy(false);
            }
          }}
        >
          <DialogPanel className="flex flex-col gap-4">
            <Field>
              <FieldLabel>Name</FieldLabel>
              <Input required value={name} placeholder="fix-login" onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>Branch from</FieldLabel>
              <Input value={base} className="font-mono" onChange={(e) => setBase(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>Create with</FieldLabel>
              <Select items={available} value={chosen} onValueChange={(v) => v && setProvider(v.value)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectPopup>
                  {available.map((p) => (
                    <SelectItem key={p.value} value={p}>
                      {p.label}
                    </SelectItem>
                  ))}
                </SelectPopup>
              </Select>
              {willRunSetup && <FieldDescription>{willRunSetup}.</FieldDescription>}
            </Field>
            {chosen.value === "orca" && (
              <>
                <Field>
                  <FieldLabel>Start an agent</FieldLabel>
                  <Select items={agentItems} value={agent} onValueChange={(v) => v && setAgent(v)}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectPopup>
                      {agentItems.map((a) => (
                        <SelectItem key={a.value || "none"} value={a}>
                          {a.label}
                        </SelectItem>
                      ))}
                    </SelectPopup>
                  </Select>
                </Field>
                {agent.value && (
                  <Field>
                    <FieldLabel>Prompt</FieldLabel>
                    <Textarea value={prompt} placeholder="What should the agent do?" onChange={(e) => setPrompt(e.target.value)} />
                  </Field>
                )}
              </>
            )}
          </DialogPanel>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" type="button" />}>Cancel</DialogClose>
            <Button type="submit" disabled={busy || !name}>
              {busy && <Spinner />}
              Create worktree
            </Button>
          </DialogFooter>
        </form>
      </DialogPopup>
    </Dialog>
  );
}

function ScriptsDialog({ box, location, onClose, onDone }: { box: string; location?: Location; onClose: () => void; onDone: () => void }) {
  const [setup, setSetup] = useState("");
  const [archive, setArchive] = useState("");
  const fromOrca = location?.scripts.from === "orca";
  return (
    <Dialog
      open={location !== undefined}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      onOpenChangeComplete={(open) => {
        if (open && location) {
          setSetup(location.scripts.from === "calport" ? (location.scripts.setup ?? "") : "");
          setArchive(location.scripts.from === "calport" ? (location.scripts.archive ?? "") : "");
        }
      }}
    >
      <DialogPopup>
        <DialogHeader>
          <DialogTitle>Scripts for {location?.name}</DialogTitle>
          <DialogDescription>
            Run when calport creates a worktree with git or Herdr, and before it removes one. Orca runs its own for
            worktrees it creates. Scripts get ORCA_ROOT_PATH, ORCA_WORKTREE_PATH and ORCA_WORKSPACE_NAME, so Orca scripts
            work unchanged.
          </DialogDescription>
        </DialogHeader>
        <form
          className="contents"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!location) return;
            try {
              await calport.setScripts(box, location.name, setup.trim(), archive.trim());
              toastManager.add({ title: setup || archive ? "Scripts saved" : "Using Orca's scripts", type: "success" });
              onDone();
              onClose();
            } catch (err) {
              failure("Could not save the scripts", err);
            }
          }}
        >
          <DialogPanel className="flex flex-col gap-4">
            <Field>
              <FieldLabel>Setup</FieldLabel>
              <Input value={setup} className="font-mono" placeholder={fromOrca ? location?.scripts.setup : '"$HOME/.local/bin/cal-worktree" setup'} onChange={(e) => setSetup(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>Archive</FieldLabel>
              <Input value={archive} className="font-mono" placeholder={fromOrca ? location?.scripts.archive : '"$HOME/.local/bin/cal-archive"'} onChange={(e) => setArchive(e.target.value)} />
              <FieldDescription>{fromOrca ? "Leave both empty to keep using Orca's scripts, shown as placeholders." : "Leave both empty for none."}</FieldDescription>
            </Field>
          </DialogPanel>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" type="button" />}>Cancel</DialogClose>
            <Button type="submit">Save</Button>
          </DialogFooter>
        </form>
      </DialogPopup>
    </Dialog>
  );
}
