import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { ScrollView, Text, View } from "react-native";

import { type PluginSurfaceProps, useRpc } from "@getpaseo/plugin/client";

import { boxesStats } from "../shared/boxes";

function gib(bytes: number): string {
  return `${(bytes / 1024 ** 3).toFixed(1)} GiB`;
}

function percent(used: number, total: number): number {
  return total > 0 ? Math.round((used / total) * 100) : 0;
}

function duration(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  if (days > 0) return `up ${days}d`;
  const hours = Math.floor(seconds / 3600);
  if (hours > 0) return `up ${hours}h`;
  return `up ${Math.max(1, Math.floor(seconds / 60))}m`;
}

// Bar is a plain filled track: a number next to a shape is read faster than a
// number alone when several boxes are being compared.
function Bar({ fraction, color, track }: { fraction: number; color: string; track: string }) {
  return (
    <View style={{ height: 6, borderRadius: 3, backgroundColor: track, overflow: "hidden" }}>
      <View
        style={{
          height: 6,
          borderRadius: 3,
          backgroundColor: color,
          width: `${Math.min(100, Math.max(0, fraction))}%`,
        }}
      />
    </View>
  );
}

export function BoxesSurface({ theme, layout }: PluginSurfaceProps) {
  const read = useRpc(boxesStats);
  const stats = useQuery({
    queryKey: ["calport-boxes"],
    queryFn: () => read({}),
    refetchInterval: 15000,
  });

  const styles = useMemo(
    () => ({
      screen: { flex: 1, backgroundColor: theme.colors.surface0 },
      content: { padding: layout.compact ? 16 : 24, gap: layout.compact ? 12 : 16 },
      title: { color: theme.colors.foreground, fontSize: layout.compact ? 20 : 24 },
      muted: { color: theme.colors.foregroundMuted, fontSize: 13 },
      card: {
        padding: layout.compact ? 12 : 16,
        gap: 10,
        borderRadius: 10,
        backgroundColor: theme.colors.surface1,
      },
      name: { color: theme.colors.foreground, fontSize: 16 },
      label: { color: theme.colors.foregroundMuted, fontSize: 12 },
      value: { color: theme.colors.foreground, fontSize: 13 },
    }),
    [theme, layout.compact],
  );

  const data = stats.data;
  const body = () => {
    if (stats.isPending) return <Text style={styles.muted}>Asking calport about its boxes…</Text>;
    if (stats.error) return <Text style={styles.muted}>{String(stats.error)}</Text>;
    if (!data?.available || data.detail) {
      return <Text style={styles.muted}>{data?.detail ?? "calport did not answer."}</Text>;
    }
    if (data.boxes.length === 0) {
      return <Text style={styles.muted}>calport has no boxes paired on this machine.</Text>;
    }
    return data.boxes.map((box) => (
      <View key={box.name} style={styles.card}>
        <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 8 }}>
          <Text style={styles.name}>{box.name}</Text>
          <Text style={styles.label}>
            {box.state}
            {box.latencyMs === undefined ? "" : ` · ${box.latencyMs}ms`}
            {box.network ? ` · ${box.network}` : ""}
          </Text>
        </View>
        {box.detail ? <Text style={styles.muted}>{box.detail}</Text> : null}
        {box.state !== "online" ? null : (
          <View style={{ gap: 10 }}>
            <Text style={styles.label}>
              {box.hostname ?? "?"}
              {box.cpus === undefined ? "" : ` · ${box.cpus} CPU`}
              {box.load?.length ? ` · load ${box.load[0].toFixed(2)}` : ""}
              {box.uptimeSeconds === undefined ? "" : ` · ${duration(box.uptimeSeconds)}`}
            </Text>
            {box.memory ? (
              <View style={{ gap: 4 }}>
                <Text style={styles.value}>
                  Memory {gib(box.memory.used)} of {gib(box.memory.total)} (
                  {percent(box.memory.used, box.memory.total)}%)
                </Text>
                <Bar
                  fraction={percent(box.memory.used, box.memory.total)}
                  color={theme.colors.accent}
                  track={theme.colors.surface2}
                />
              </View>
            ) : null}
            {(box.disks ?? []).map((disk) => (
              <View key={disk.mount} style={{ gap: 4 }}>
                <Text style={styles.value}>
                  {disk.mount} {gib(disk.used)} of {gib(disk.total)} ({percent(disk.used, disk.total)}%)
                </Text>
                <Bar
                  fraction={percent(disk.used, disk.total)}
                  color={theme.colors.accent}
                  track={theme.colors.surface2}
                />
              </View>
            ))}
            <Text style={styles.label}>
              {box.agents?.length
                ? box.agents
                    .map((a) => `${a.tool}${a.worktree ? ` in ${a.worktree}` : ""} (${a.state})`)
                    .join(", ")
                : "No agents running"}
            </Text>
          </View>
        )}
      </View>
    ));
  };

  return (
    <ScrollView style={styles.screen} contentContainerStyle={styles.content}>
      <Text style={styles.title}>Boxes</Text>
      {body()}
    </ScrollView>
  );
}
