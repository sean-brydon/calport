import { BotIcon, HourglassIcon } from "lucide-react";

import { CopyCommand } from "@/components/connect-box";
import { Badge } from "@/components/ui/badge";
import { Card, CardDescription, CardHeader, CardPanel, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Meter, MeterIndicator, MeterTrack } from "@/components/ui/meter";
import { Spinner } from "@/components/ui/spinner";
import { useLoad, usePoll } from "@/hooks/use-calport";
import { type BoxAgent, type BoxStats, calport, type Usage } from "@/lib/calport";
import { bytes, duration, groupAgents } from "@/lib/format";
import { cn } from "@/lib/utils";

const toolLabel: Record<BoxAgent["tool"], string> = { claude: "Claude Code", codex: "Codex", cursor: "Cursor" };

export function OverviewTab({ box, version }: { box: string; version: number }) {
  const stats = useLoad(() => calport.stats(box), [box, version]);
  usePoll(stats.reload, 5_000);
  if (stats.error && !stats.data) return <p className="text-destructive-foreground text-sm">{stats.error}</p>;
  if (!stats.data) return <Spinner />;
  const s = stats.data;
  const waiting = s.agents.filter((a) => a.state === "waiting").length;
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard label="Agents running" value={String(s.agents.length)} detail={s.hooks ? `${waiting} waiting for you` : "states need hooks"} emphasis={waiting > 0} />
        <UsageCard label="Memory" usage={s.memory} detail={s.swap.used > 0 ? `swap ${bytes(s.swap.used)} used` : undefined} />
        {s.disks.slice(0, 1).map((d) => (
          <UsageCard key={d.mount} label={`Disk ${d.mount}`} usage={d} detail={`${bytes(d.total - d.used)} free`} />
        ))}
        <StatCard
          label="Load"
          value={s.load?.[0]?.toFixed(2) ?? "–"}
          detail={`${s.cpus} CPUs${s.uptime_s ? ` · up ${duration(s.uptime_s)}` : ""}`}
          emphasis={(s.load?.[0] ?? 0) > s.cpus}
        />
      </div>
      <Agents stats={s} />
    </div>
  );
}

function StatCard({ label, value, detail, emphasis }: { label: string; value: string; detail?: string; emphasis?: boolean }) {
  return (
    <Card>
      <CardPanel className="flex flex-col gap-1">
        <span className="text-muted-foreground text-xs">{label}</span>
        <span className={cn("font-heading font-semibold text-2xl tabular-nums", emphasis && "text-warning-foreground")}>{value}</span>
        {detail && <span className="text-muted-foreground text-xs">{detail}</span>}
      </CardPanel>
    </Card>
  );
}

function UsageCard({ label, usage, detail }: { label: string; usage: Usage; detail?: string }) {
  const pct = usage.total ? Math.round((usage.used / usage.total) * 100) : 0;
  return (
    <Card>
      <CardPanel className="flex flex-col gap-2">
        <span className="text-muted-foreground text-xs">{label}</span>
        <span className="font-heading font-semibold text-2xl tabular-nums">{pct}%</span>
        <Meter value={pct} aria-label={`${label} used`}>
          <MeterTrack className="h-1.5 rounded-full">
            <MeterIndicator className={cn(pct >= 90 ? "bg-destructive" : pct >= 75 ? "bg-warning" : "bg-primary")} />
          </MeterTrack>
        </Meter>
        <span className="text-muted-foreground text-xs">
          {bytes(usage.used)} of {bytes(usage.total)}
          {detail ? ` · ${detail}` : ""}
        </span>
      </CardPanel>
    </Card>
  );
}

function Agents({ stats }: { stats: BoxStats }) {
  const groups = groupAgents(stats.agents);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Agents</CardTitle>
        <CardDescription>Coding agents running on the box, by where they work.</CardDescription>
      </CardHeader>
      <CardPanel className="flex flex-col gap-4">
        {!stats.hooks && (
          <div className="flex flex-col gap-2 rounded-lg border p-3">
            <span className="text-sm">
              To see which agents are waiting for you or have finished, let Claude Code on the box report to calportd.
              On the box, run:
            </span>
            <CopyCommand command="calportd integrations install claude" />
          </div>
        )}
        {groups.length === 0 ? (
          <Empty className="py-6">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BotIcon />
              </EmptyMedia>
              <EmptyTitle>No agents running</EmptyTitle>
              <EmptyDescription>Start one in a worktree from the Worktrees tab, or in Orca or Herdr.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ul className="flex flex-col divide-y rounded-lg border">
            {groups.map((g) => (
              <li key={g.key} className="flex items-center gap-3 px-3 py-2.5">
                <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <span className="truncate font-medium text-sm">{g.label}</span>
                  {g.path && <span className="truncate font-mono text-muted-foreground text-xs">{g.path}</span>}
                </div>
                <span className="flex shrink-0 flex-wrap justify-end gap-1.5">
                  {g.agents.map((a) => (
                    <AgentBadge key={a.pid} agent={a} />
                  ))}
                </span>
              </li>
            ))}
          </ul>
        )}
      </CardPanel>
    </Card>
  );
}

function AgentBadge({ agent }: { agent: BoxAgent }) {
  const name = toolLabel[agent.tool];
  if (agent.state === "waiting") {
    return (
      <Badge variant="warning">
        <HourglassIcon />
        {name} needs you
      </Badge>
    );
  }
  if (agent.state === "finished") return <Badge variant="success">{name} finished</Badge>;
  return <Badge variant="outline">{name}</Badge>;
}
