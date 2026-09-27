import { openUrl } from "@tauri-apps/plugin-opener";
import { GlobeIcon, GlobeLockIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toastManager } from "@/components/ui/toast";
import { useLoad } from "@/hooks/use-calport";
import { calport } from "@/lib/calport";
import { since } from "@/lib/format";

export function SharingTab({ box, version, onChanged }: { box: string; version: number; onChanged: () => void }) {
  const shares = useLoad(() => calport.shares(box), [box, version]);
  const list = shares.data ?? [];
  if (!shares.loading && list.length === 0 && !shares.error) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <GlobeLockIcon />
          </EmptyMedia>
          <EmptyTitle>Nothing is public</EmptyTitle>
          <EmptyDescription>Everything on {box} is private to your paired laptops. Share a port from Services to hand someone a link.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      {shares.error && <p className="text-destructive-foreground text-sm">{shares.error}</p>}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-24">Port</TableHead>
            <TableHead>Public link</TableHead>
            <TableHead className="w-28">Since</TableHead>
            <TableHead className="w-32 text-right">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.map((s) => (
            <TableRow key={s.id}>
              <TableCell>
                <span className="flex items-center gap-2 font-mono">
                  {s.port}
                  <Badge variant="warning">
                    <GlobeIcon />
                    Public
                  </Badge>
                </span>
              </TableCell>
              <TableCell>
                <button type="button" className="truncate font-mono text-sm underline-offset-4 hover:underline" onClick={() => openUrl(s.url)}>
                  {s.url}
                </button>
              </TableCell>
              <TableCell className="text-muted-foreground">{since(s.started)}</TableCell>
              <TableCell className="text-right">
                <Button
                  size="sm"
                  variant="destructive-outline"
                  onClick={() =>
                    calport.unshare(box, s.id).then(
                      () => {
                        toastManager.add({ title: `Port ${s.port} is private again`, description: `${s.url} no longer works.`, type: "success" });
                        onChanged();
                      },
                      (err) => toastManager.add({ title: "Could not stop sharing", description: String(err), type: "error" }),
                    )
                  }
                >
                  Stop sharing
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
