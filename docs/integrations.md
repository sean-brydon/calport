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

### The Orca runtime on a box

`calport orca connect BOX` lets this computer's own Orca app reach an Orca
runtime running on a box:

```sh
calport orca connect devl
```

It installs and starts the runtime as a managed unit on the box if it is not
already running, tunnels it over the same paired connection calport already
uses to reach that box, and pairs the local Orca app with it. calport itself
always reaches the runtime through that existing tunnel over calportd, so
nothing needs forwarding or opening for calport's own use. But `orca serve`
has no flag to restrict its bind address, so the runtime listens on every
interface on the box, not just loopback — anything else on the box's network
can still reach its port directly. calport does not close that off, and does
not open it either: restricting the runtime's port, if that matters for a
given box's network, remains a box-hardening concern that calport neither
solves nor worsens.

Orca is the one that holds the pairing credential, once it has paired;
calport itself only ever stores the route (the runtime's identity and the
local port it tunnels to) in `orca.json` under its state directory. That
credential is never printed by any `calport orca` command and never belongs
in a command, a config file, or a chat message — pairing happens once, inside
Orca.

A box's local port is fixed the first time it pairs, because the pairing
code the local Orca app stores embeds that port. Reconnecting (`calport orca
connect BOX` again) restores the tunnel on the same port if it was lost; it
does not move to a new one. `calport orca status BOX` only reports whether
the runtime this computer already paired with is reachable — it neither
installs nor pairs anything. `calport orca exec BOX -- ARGS` runs an `orca`
command against that box's paired runtime, and `calport orca serve BOX`
starts the runtime without pairing to it.

### The Cal.com kit

On a box with a Cal.com checkout, `calport kit install BOX/LOCATION` (or **Set
up** on the location in the app) installs the Cal.com worktree kit. Each new
worktree then gets its own port, a copy of the dev database, its own `.env`,
and a URL like `http://fix-login-a1b2c3.devl.cal.localhost`; archiving stops it.

What it installs, as your user, with nothing needing root:

- the scripts in `~/.local/share/cal-worktrees`, embedded in calportd, and
  `cal-worktree`, `cal-archive` and `cal-setup` links in `~/.local/bin` (a
  script of the same name already there is kept as `NAME.calport-backup`);
- `cal-worktree-proxy.service`, which is `calportd kit router`: it routes each
  worktree hostname to its dev server on `127.0.0.1:18080`, and serves
  `/__worktree/logs` and Prisma Studio next to it;
- with Orca, `cal-worktree-lifecycle.service`, which stops worktrees Orca
  archives and sets up ones it restores;
- on the laptop, a route for the kit's hostnames to that router.

Worktrees calport creates run the hooks itself. So worktrees other tools make
run them too, `calport kit tools BOX/LOCATION --setup` (or **Set up all** under
**Worktree tools** in the app) writes each tool's per-repository config into
the checkout and lists it in that clone's `.git/info/exclude`, so it is never
committed and never shows up in a PR:

| Tool | What calport does |
| --- | --- |
| Orca | `orca.yaml` with `scripts.setup`, `scripts.archive` and `setupAgentStartupPolicy: wait-for-setup`; Orca asks you to trust the scripts once. Hooks set in Orca's own settings take precedence, so when it has some calport writes nothing. |
| Cursor | `.cursor/worktrees.json` (`setup-worktree`). Cursor has no teardown hook. |
| Codex app | `.codex/environments/environment.toml` with setup and cleanup. Select the "Cal.com worktree kit" environment once in the app; Codex does not run setup over Remote SSH yet ([openai/codex#23648](https://github.com/openai/codex/issues/23648)). |
| Superset | `.superset/config.json` with setup and teardown. |
| Herdr | Opt-in (`--tool herdr`): Herdr has no repository file, so calport installs a plugin for your user in `~/.local/share/cal-worktrees/herdr-plugin` and links it with `herdr plugin link`. Its `worktree.created` and `worktree.removed` hooks run the kit's hooks for worktrees of the kit's checkout. |
| Conductor | Skipped: it makes worktrees on your Mac only. |
| Claude Code | Skipped: its `WorktreeCreate` hook replaces how it makes worktrees. Run `cal-setup` in one instead. |

A file the repository commits, or one you wrote that runs other scripts, is
left alone. The hooks read the main checkout and worktree from whichever tool
runs them (`ORCA_*`, `CONDUCTOR_*`, `SUPERSET_*`, `CODEX_*`, Cursor's
`ROOT_WORKTREE_PATH`), falling back to the current directory. `calport doctor
BOX` says when a tool on the box is not set up. Reinstalling keeps a box's URL
label, so URLs in use keep working.

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
