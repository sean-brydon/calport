import { openUrl } from "@tauri-apps/plugin-opener";
import { ArrowRightLeftIcon, CopyIcon, ExternalLinkIcon, GlobeIcon, PlugZapIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";

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
import { Button } from "@/components/ui/button";
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
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toastManager } from "@/components/ui/toast";
import { Tooltip, TooltipPopup, TooltipTrigger } from "@/components/ui/tooltip";
import { usePoll, useLoad } from "@/hooks/use-calport";
import { calport, type ForwardStatus, type Port, serviceURL, type Route } from "@/lib/calport";

interface ServicesTabProps {
  box: string;
  urlPort: number;
  forwards: ForwardStatus[];
  routes: Route[];
  version: number;
  onChanged: () => void;
}

// Linux truncates process names to 15 characters; the command line is whole.
function processLabel(p: Port): string | undefined {
  const label = p.command || p.process;
  return label && label.length > 48 ? `${label.slice(0, 48)}…` : label;
}

function bindLabel(address: string): string {
  if (address === "::" || address === "0.0.0.0") return "all interfaces";
  if (address === "127.0.0.1" || address === "::1" || address.startsWith("127.")) return "this box only";
  if (address.startsWith("100.") || address.startsWith("fd7a:115c:a1e0")) return `tailnet (${address})`;
  return address;
}

function failure(title: string, err: unknown) {
  toastManager.add({ title, description: err instanceof Error ? err.message : String(err), type: "error" });
}

export function ServicesTab({ box, urlPort, forwards, routes, version, onChanged }: ServicesTabProps) {
  const ports = useLoad(() => calport.ports(box), [box, version]);
  const services = useLoad(() => calport.services(box), [box, version]);
  usePoll(ports.reload, 10_000);
  usePoll(services.reload, 10_000);
  const [showSystem, setShowSystem] = useState(false);
  const [forwarding, setForwarding] = useState<Port>();
  const [sharing, setSharing] = useState<Port>();

  const url = (port: number) => serviceURL(port, box, urlPort);
  const visible = (ports.data ?? []).filter((p) => showSystem || p.port >= 1024);
  const boxForwards = forwards.filter((f) => f.box === box);
  const boxRoutes = routes.filter((r) => r.box === box);
  const worktreeOf = (port: number) => services.data?.find((s) => s.port === port);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-4">
        <p className="flex flex-wrap items-center gap-x-2 text-muted-foreground text-sm">
          <span>
            Every port is private to this laptop at <code className="font-mono">{serviceURL("PORT", box, urlPort)}</code>
          </span>
          {urlPort !== 80 && (
            <Button
              size="sm"
              variant="link"
              className="h-auto px-0"
              onClick={() =>
                calport.setupPort80().then(
                  (out) => {
                    toastManager.add({ title: "URLs no longer need a port", description: out.trim().split("\n").slice(1).join(" ") || undefined, type: "success" });
                    onChanged();
                  },
                  (err) => failure("Could not set up short URLs", err),
                )
              }
            >
              Drop :{urlPort} from URLs
            </Button>
          )}
        </p>
        <div className="flex items-center gap-2">
          <Switch id="system-ports" checked={showSystem} onCheckedChange={setShowSystem} />
          <Label htmlFor="system-ports">System ports</Label>
        </div>
      </div>

      {ports.error && <p className="text-destructive-foreground text-sm">{ports.error}</p>}
      {!ports.error && visible.length === 0 && !ports.loading && (
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <PlugZapIcon />
            </EmptyMedia>
            <EmptyTitle>Nothing is listening</EmptyTitle>
            <EmptyDescription>Start a dev server on {box} and it shows up here.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}
      {visible.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-20">Port</TableHead>
              <TableHead>Process</TableHead>
              <TableHead className="w-40">Bound to</TableHead>
              <TableHead className="w-44 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {visible.map((p) => (
              <TableRow key={p.port}>
                <TableCell className="font-mono tabular-nums">{p.port}</TableCell>
                <TableCell className="max-w-0 truncate" title={p.command}>
                  {worktreeOf(p.port) && (
                    <span className="mr-2 font-medium">
                      {worktreeOf(p.port)?.location}/{worktreeOf(p.port)?.worktree}
                    </span>
                  )}
                  <span className={worktreeOf(p.port) ? "text-muted-foreground" : undefined}>
                    {processLabel(p) ?? <span className="text-muted-foreground">unknown</span>}
                  </span>
                </TableCell>
                <TableCell className="text-muted-foreground text-xs" title={p.address}>
                  {bindLabel(p.address)}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
                    <Tooltip>
                      <TooltipTrigger render={<Button size="icon-sm" variant="ghost" aria-label={`Open port ${p.port}`} onClick={() => openUrl(url(p.port))} />}>
                        <ExternalLinkIcon />
                      </TooltipTrigger>
                      <TooltipPopup>Open in browser</TooltipPopup>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={`Copy URL for port ${p.port}`}
                            onClick={async () => {
                              await navigator.clipboard.writeText(url(p.port));
                              toastManager.add({ title: "URL copied", description: url(p.port), type: "success" });
                            }}
                          />
                        }
                      >
                        <CopyIcon />
                      </TooltipTrigger>
                      <TooltipPopup>Copy private URL</TooltipPopup>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger render={<Button size="icon-sm" variant="ghost" aria-label={`Forward port ${p.port}`} onClick={() => setForwarding(p)} />}>
                        <ArrowRightLeftIcon />
                      </TooltipTrigger>
                      <TooltipPopup>Forward to a local port</TooltipPopup>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger render={<Button size="icon-sm" variant="ghost" aria-label={`Share port ${p.port} publicly`} onClick={() => setSharing(p)} />}>
                        <GlobeIcon />
                      </TooltipTrigger>
                      <TooltipPopup>Share publicly</TooltipPopup>
                    </Tooltip>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {boxRoutes.length > 0 && (
        <section className="flex flex-col gap-2">
          <h3 className="font-medium text-sm">Routes</h3>
          <p className="text-muted-foreground text-sm">Hostnames sent to {box} unchanged. Manage them in Settings.</p>
          <Table>
            <TableBody>
              {boxRoutes.map((r) => (
                <TableRow key={r.pattern}>
                  <TableCell className="font-mono">
                    {urlPort === 80 ? r.pattern : `${r.pattern}:${urlPort}`}
                  </TableCell>
                  <TableCell className="font-mono text-muted-foreground">
                    → {box}:{r.port}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      )}

      {boxForwards.length > 0 && (
        <section className="flex flex-col gap-2">
          <h3 className="font-medium text-sm">Forwards</h3>
          <Table>
            <TableBody>
              {boxForwards.map((f) => (
                <TableRow key={f.id}>
                  <TableCell className="font-mono">localhost:{f.local}</TableCell>
                  <TableCell className="font-mono text-muted-foreground">→ {box}:{f.remote}</TableCell>
                  <TableCell>
                    {f.state === "listening" ? (
                      <Badge variant="success">Listening</Badge>
                    ) : (
                      <Badge variant="error" title={f.error}>
                        Failed
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Stop forwarding localhost:${f.local}`}
                      onClick={() => calport.unforward(f.id).then(onChanged, (err) => failure("Could not stop the forward", err))}
                    >
                      <Trash2Icon />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      )}

      <ForwardDialog box={box} port={forwarding} onClose={() => setForwarding(undefined)} onDone={onChanged} />
      <ShareDialog box={box} port={sharing} onClose={() => setSharing(undefined)} onDone={onChanged} />
    </div>
  );
}

function ForwardDialog({ box, port, onClose, onDone }: { box: string; port?: Port; onClose: () => void; onDone: () => void }) {
  const [local, setLocal] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Dialog
      open={port !== undefined}
      onOpenChange={(open) => {
        if (!open) onClose();
        else setLocal(String(port?.port ?? ""));
      }}
    >
      <DialogPopup>
        <DialogHeader>
          <DialogTitle>Forward port {port?.port}</DialogTitle>
          <DialogDescription>Reach {box}:{port?.port} at a fixed port on this laptop, for tools that need localhost.</DialogDescription>
        </DialogHeader>
        <form
          className="contents"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!port) return;
            setBusy(true);
            try {
              await calport.forward(box, Number(local || port.port), port.port);
              toastManager.add({ title: `localhost:${local || port.port} → ${box}:${port.port}`, type: "success" });
              onDone();
              onClose();
            } catch (err) {
              failure("Could not forward", err);
            } finally {
              setBusy(false);
            }
          }}
        >
          <DialogPanel>
            <Field>
              <FieldLabel>Local port</FieldLabel>
              <Input inputMode="numeric" value={local} placeholder={String(port?.port ?? "")} onChange={(e) => setLocal(e.target.value.replace(/\D/g, ""))} />
              <FieldDescription>Survives restarts and reconnects on its own.</FieldDescription>
            </Field>
          </DialogPanel>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" type="button" />}>Cancel</DialogClose>
            <Button type="submit" disabled={busy}>
              Forward
            </Button>
          </DialogFooter>
        </form>
      </DialogPopup>
    </Dialog>
  );
}

function ShareDialog({ box, port, onClose, onDone }: { box: string; port?: Port; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <AlertDialog open={port !== undefined} onOpenChange={(open) => !open && onClose()}>
      <AlertDialogPopup>
        <AlertDialogHeader>
          <AlertDialogTitle>Make port {port?.port} public?</AlertDialogTitle>
          <AlertDialogDescription>
            Anyone on the internet with the link can reach {box}:{port?.port} until you stop sharing. Only share things
            without real data.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogClose render={<Button variant="ghost" type="button" />}>Cancel</AlertDialogClose>
          <Button
            type="button"
            disabled={busy}
            onClick={async () => {
              if (!port) return;
              setBusy(true);
              try {
                const share = await calport.share(box, port.port);
                await navigator.clipboard.writeText(share.url);
                toastManager.add({
                  title: "Public link copied",
                  description: `${share.url} — it can take about 10 seconds to start working.`,
                  type: "success",
                });
                onDone();
                onClose();
              } catch (err) {
                failure("Could not share", err);
              } finally {
                setBusy(false);
              }
            }}
          >
            Share publicly
          </Button>
        </AlertDialogFooter>
      </AlertDialogPopup>
    </AlertDialog>
  );
}
