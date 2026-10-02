import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { Text, View } from "react-native";

import { type PluginWorkspacePanelProps, useRpc, useWorkspace } from "@getpaseo/plugin/client";

import { worktreeInfo } from "../shared/worktree";

function Row({
  label,
  value,
  color,
  mutedColor,
}: {
  label: string;
  value: string;
  color: string;
  mutedColor: string;
}) {
  return (
    <View style={{ gap: 2 }}>
      <Text style={{ color: mutedColor, fontSize: 12 }}>{label}</Text>
      <Text selectable style={{ color, fontSize: 14, fontFamily: "monospace" }}>
        {value}
      </Text>
    </View>
  );
}

export function WorktreePanel({ theme, layout, workspaceId }: PluginWorkspacePanelProps) {
  const directory = useWorkspace(workspaceId, (workspace) => workspace.directory);
  const read = useRpc(worktreeInfo);
  const info = useQuery({
    queryKey: ["cal-worktree", directory],
    enabled: Boolean(directory),
    queryFn: () => read({ directory: directory as string }),
    // A worktree being set up, and a dev server coming up, both resolve on
    // their own in a few seconds, so the panel follows them rather than
    // leaving a stale answer on screen.
    refetchInterval: (query) =>
      query.state.data?.state === "pending" || query.state.data?.serving === false ? 3000 : false,
  });

  const styles = useMemo(
    () => ({
      screen: {
        flex: 1,
        padding: layout.compact ? 16 : 24,
        gap: layout.compact ? 12 : 16,
        backgroundColor: theme.colors.surface0,
      },
      title: { color: theme.colors.foreground, fontSize: layout.compact ? 18 : 22 },
      muted: { color: theme.colors.foregroundMuted, fontSize: 13 },
    }),
    [theme, layout.compact],
  );

  const data = info.data;
  const body = () => {
    if (info.isPending) return <Text style={styles.muted}>Reading this worktree…</Text>;
    if (info.error) return <Text style={styles.muted}>{String(info.error)}</Text>;
    if (!data || data.state === "absent") {
      return (
        <Text style={styles.muted}>
          This workspace is not a Cal.com worktree, so it has no port or database of its own.
        </Text>
      );
    }
    if (data.state === "pending") {
      return <Text style={styles.muted}>Giving this worktree its port, database and URL…</Text>;
    }
    if (data.state === "failed") {
      return (
        <View style={{ gap: 8 }}>
          <Text style={{ color: theme.colors.foreground, fontSize: 14 }}>
            Setup did not finish, so the dev server will not come up here.
          </Text>
          {data.detail ? <Text style={styles.muted}>{data.detail}</Text> : null}
          {data.path ? (
            <Row label="Worktree" value={data.path} color={theme.colors.foreground} mutedColor={theme.colors.foregroundMuted} />
          ) : null}
        </View>
      );
    }
    return (
      <View style={{ gap: layout.compact ? 12 : 14 }}>
        <Text style={styles.muted}>
          {data.serving ? "Serving now." : "Set up, but nothing is listening on the port yet."}
        </Text>
        {data.url ? (
          <Row label="URL" value={data.url} color={theme.colors.foreground} mutedColor={theme.colors.foregroundMuted} />
        ) : null}
        {data.port ? (
          <Row label="Port" value={data.port} color={theme.colors.foreground} mutedColor={theme.colors.foregroundMuted} />
        ) : null}
        {data.database ? (
          <Row
            label="Database"
            value={data.databaseHost ? `${data.database} on ${data.databaseHost}` : data.database}
            color={theme.colors.foreground}
            mutedColor={theme.colors.foregroundMuted}
          />
        ) : null}
        {data.path ? (
          <Row label="Worktree" value={data.path} color={theme.colors.foreground} mutedColor={theme.colors.foregroundMuted} />
        ) : null}
      </View>
    );
  };

  return (
    <View style={styles.screen}>
      <Text style={styles.title}>Cal.com worktree</Text>
      {body()}
    </View>
  );
}
