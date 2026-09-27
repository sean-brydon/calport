# Integrations

calport works with the tools around it in both directions: tools drive
calport through its CLI, and calport drives tools through hooks. Every event
records the tool it came from, so a change never bounces back and forth
between two tools.

## Agent tools → calport

### Claude Code, Cursor, Codex

```sh
calport integrations install claude     # on a laptop
calportd integrations install claude    # on a box, where agents usually run
calport integrations install all
```

- **Claude Code**: installs the calport skill at `~/.claude/skills/calport/` and
  `Stop` / `Notification` hooks in `~/.claude/settings.json`.
- **Cursor**: appends a `stop` hook to `~/.cursor/hooks.json`, after any hooks
  already there (Orca's are kept).
- **Codex**: installs the skill at `~/.codex/skills/calport/` and prints the
  `notify` line to add to `~/.codex/config.toml`, which calport does not edit
  because it holds a single value you may already use.

Installs are idempotent, keep every existing setting, back up the previous
file to `*.calport-backup`, and refuse to touch a file that is not valid JSON.

The hooks run `calport hook TOOL EVENT`, which turns the tool's payload into an
event (`agent.finished`, `agent.waiting`, `agent.started`). Only identifiers and
the working directory are kept: prompts, messages, and transcripts never
leave the tool. A hook never fails or blocks the tool that called it.

### The skill

The skill teaches agents to list boxes and locations, create worktrees, start
sessions, read another session's screen, find a service's URL, and announce
events, with `--json` output. It tells agents never to share a port publicly
unless a person asked.

### Anything else

Any script can announce an event:

```sh
calport emit agent.finished path="$PWD" --origin=mytool     # on a laptop
calportd emit deploy.done url=https://… --origin mytool      # on a box
```

## Worktrees from any tool

calportd watches its locations. A worktree created by Orca, Herdr, an agent,
or plain `git worktree add` shows up in calport and produces a
`worktree.created` event with origin `detected`. Nothing needs configuring in
those tools.

## calport → tools

### Creating worktrees through Orca or Herdr

```sh
calport worktree new devl/cal/fix-login --provider orca --agent claude --prompt "…"
calport worktree new devl/cal/fix-login --provider herdr --herdr-session agents
```

- **Orca**: registers the repository with Orca first if Orca does not know it
  (`orca repo add`), then `orca worktree create`. Orca decides where the
  worktree lives and runs the repository's `orca.yaml` setup policy. Removing it
  through calport goes through `orca worktree rm`, so Orca's records stay right.
- **Herdr**: `herdr worktree create`, which also opens a workspace with a pane
  in the new worktree.

### Hooks

Hooks run shell commands when events happen. The box reads
`<state>/box/hooks.json`; the laptop reads `<state>/client/hooks.json`. Both
are re-read for every event.

```json
{
  "hooks": [
    { "on": "agent.finished", "run": "herdr notification show \"Agent finished in $CALPORT_PATH\"", "tool": "herdr" },
    { "on": "worktree.created", "run": "orca worktree set --worktree path:$CALPORT_PATH --comment 'from calport'", "tool": "orca" },
    { "on": "session.*", "run": "cat >> ~/calport-sessions.log" }
  ]
}
```

- `on` is an event type, a prefix like `worktree.*`, or `*`.
- The command gets the event as JSON on stdin and as `CALPORT_EVENT`,
  `CALPORT_EVENT_BOX`, `CALPORT_EVENT_ORIGIN`, and `CALPORT_<FIELD>` (for
  example `CALPORT_PATH`, `CALPORT_NAME`, `CALPORT_URL`).
- `tool` names the tool the hook drives. Events that came from that tool never
  trigger it, and calport calls the hook makes are attributed to that tool.
  This is what stops loops.
- `timeout` (default `1m`) bounds each run.

## Events

| Event | From |
| --- | --- |
| `box.connected`, `box.disconnected`, `box.untrusted` | laptop agent |
| `forward.started`, `forward.failed`, `forward.removed` | laptop agent |
| `location.added`, `location.removed` | box |
| `worktree.created`, `worktree.removed` | box, or detected on disk |
| `session.started`, `session.stopped` | box |
| `share.started`, `share.stopped` | box |
| `box.upgraded` | box |
| `agent.started`, `agent.finished`, `agent.waiting` | agent tool hooks |

`calport events` streams every event from the laptop and all its boxes.
