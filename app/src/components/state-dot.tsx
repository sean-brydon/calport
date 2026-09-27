import type { BoxState } from "@/lib/calport";
import { stateLabel } from "@/lib/format";
import { cn } from "@/lib/utils";

const colour: Record<BoxState, string> = {
  connecting: "bg-warning",
  online: "bg-success",
  offline: "bg-muted-foreground/40",
  untrusted: "bg-destructive",
};

export function StateDot({ state, className }: { state: BoxState; className?: string }) {
  return (
    <span
      role="img"
      aria-label={stateLabel[state]}
      className={cn("inline-block size-2 shrink-0 rounded-full", colour[state], className)}
    />
  );
}
