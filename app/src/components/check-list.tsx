import { CircleAlertIcon, CircleCheckIcon, CircleXIcon, InfoIcon } from "lucide-react";

import { CopyCommand } from "@/components/connect-box";
import type { Check } from "@/lib/calport";
import { cn } from "@/lib/utils";

const icons = {
  ok: { icon: CircleCheckIcon, className: "text-success" },
  warn: { icon: CircleAlertIcon, className: "text-warning" },
  fail: { icon: CircleXIcon, className: "text-destructive" },
  info: { icon: InfoIcon, className: "text-info" },
} as const;

// fixCommand is the runnable part of a fix, which calport writes before any
// two-space aside such as "(or open the Calport app)".
function fixCommand(fix: string): { command?: string; note?: string } {
  const [head, ...rest] = fix.split("  ");
  const looksRunnable = /^(calport|calportd|sudo|orca|herdr|curl|systemctl|loginctl)\b/.test(head);
  return looksRunnable ? { command: head, note: rest.join(" ").trim() || undefined } : { note: fix };
}

export function CheckList({ checks }: { checks: Check[] }) {
  const areas = [...new Set(checks.map((c) => c.area))];
  return (
    <div className="flex flex-col gap-5">
      {areas.map((area) => (
        <section key={area} className="flex flex-col gap-1">
          <h3 className="font-medium text-muted-foreground text-xs uppercase tracking-wide">{area}</h3>
          <ul className="flex flex-col divide-y">
            {checks
              .filter((c) => c.area === area)
              .map((c) => {
                const { icon: Icon, className } = icons[c.status];
                const fix = c.fix && c.status !== "ok" ? fixCommand(c.fix) : undefined;
                return (
                  <li key={`${c.area}-${c.name}`} className="flex gap-3 py-2.5">
                    <Icon className={cn("mt-0.5 size-4 shrink-0", className)} aria-label={c.status} />
                    <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                      <span className="text-sm">
                        <span className="font-medium">{c.name}</span>
                        {c.detail && <span className="text-muted-foreground"> · {c.detail}</span>}
                      </span>
                      {fix?.command && <CopyCommand command={fix.command} />}
                      {fix?.note && <span className="text-muted-foreground text-xs">{fix.note}</span>}
                    </div>
                  </li>
                );
              })}
          </ul>
        </section>
      ))}
    </div>
  );
}
