---
name: calport
description: Use calport to work across development boxes — list and create worktrees at registered locations, start and read other agent sessions, find a service's private URL, forward ports, and announce events. Use when asked to spin up a worktree or agent on a box, check what another agent is doing, open a dev server running on a box, or hand work between machines.
---

# calport

calport connects a laptop to development boxes. Each box runs `calportd`; each
laptop runs `calport`. The same commands exist in both, with one difference:

- **On a laptop**, name the box first: `calport worktree new devl/cal/fix-login`.
- **On a box**, the box is implied: `calportd worktree new cal/fix-login`.

Check which you have with `command -v calport calportd`. Add `--json` to any
listing command for machine-readable output; prefer it when you will parse the
result.

## Concepts

- **Box**: a paired machine, e.g. `devl`. `calport boxes` lists them.
- **Location**: a named directory on a box, usually a git repository, e.g.
  `cal` → `~/work/cal`. Its worktrees are addressed as `cal/<worktree>`.
- **Session**: a long-running program (usually a coding agent) started at a
  location. It keeps running when nobody is attached.

## Find your way around

```sh
calport boxes --json                 # which boxes are online
calport locations devl --json        # locations and their worktrees
calport sessions devl --json         # running agent sessions
calport ports devl --json            # what is listening on the box
```

## Create a worktree and start an agent in it

```sh
calport worktree new devl/cal/fix-login --base main
calport session new devl/cal/fix-login --name fix-login -- claude
```

`--provider orca` or `--provider herdr` creates the worktree through that tool
instead of plain git, so it also appears there. With Orca, `--agent claude
--prompt "..."` starts the agent for you.

## See what another agent is doing

```sh
calport session screen devl/fix-login --history 200
```

This prints the session's terminal. Do not attach (`calport attach`) from an
agent: it is interactive and meant for humans.

## Reach a dev server

Every port on a box has a private URL on the laptop:

```sh
calport url devl 3000        # → http://3000.devl.localhost:1355/
```

For a fixed local port instead: `calport forward devl 3000`.

## Share publicly — only when a human asks

`calport share devl 3000` makes a port reachable **by anyone on the
internet** until `calport unshare devl <id>`. Never share on your own
initiative, and never share anything with real data. Confirm with the user
first and tell them the URL and how to stop it.

## Announce what you did

```sh
calport emit agent.finished path="$PWD" --origin=claude     # on a laptop
calportd emit agent.finished path="$PWD" --origin claude    # on a box
```

Hooks configured by the user react to these events (notifications, starting
the next agent). Event types look like `area.action`. Put identifiers and
paths in events, never prompts, secrets, or personal data.

## When something fails

- `no paired box named X`: run `calport boxes`; the name may differ.
- `box no longer trusts this laptop`: the box revoked access; a human must pair again.
- `calportd serve is not running`: on a box, `calportd install` starts it.
