import { CheckIcon } from "lucide-react";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { calport } from "@/lib/calport";

const tools = [
  { id: "claude", name: "Claude Code", text: "Tells you when Claude finishes or needs you, and teaches it to use Calport." },
  { id: "cursor", name: "Cursor", text: "Tells you when Cursor's agent finishes. Existing hooks, like Orca's, are kept." },
  { id: "codex", name: "Codex", text: "Teaches Codex to use Calport." },
] as const;

// ToolIntegrations connects coding agents on this computer to calport. Each
// install is idempotent, so connecting twice is harmless.
export function ToolIntegrations() {
  const [done, setDone] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState<string>();
  return (
    <ul className="flex flex-col divide-y rounded-lg border">
      {tools.map((t) => (
        <li key={t.id} className="flex items-center justify-between gap-4 px-4 py-3">
            <span className="flex flex-col gap-0.5">
              <span className="font-medium">{t.name}</span>
              <span className="text-muted-foreground text-sm">{t.text}</span>
            </span>
            {done[t.id] ? (
              <Badge variant="success">
                <CheckIcon />
                Connected
              </Badge>
            ) : (
              <Button
                variant="outline"
                disabled={busy !== undefined}
                onClick={async () => {
                  setBusy(t.id);
                  try {
                    await calport.installIntegration(t.id);
                    setDone((d) => ({ ...d, [t.id]: true }));
                  } catch (err) {
                    toastManager.add({ title: `Could not connect ${t.name}`, description: String(err), type: "error" });
                  } finally {
                    setBusy(undefined);
                  }
                }}
              >
                {busy === t.id && <Spinner />}
                Connect
              </Button>
            )}
        </li>
      ))}
    </ul>
  );
}
