# Orca runtime tunneling

Give calport what tailmux's `tailmux orca serve|connect|status|exec` gave: a
box's Orca runtime reachable from the laptop, so the **local** Orca app lists
and drives environments that live on a box. calport keeps its existing
box-side Orca provider; the two paths coexist.

## Why this is not just a port

tailmux and calport point in opposite directions today.

- tailmux tunnels a box's Orca runtime back to the laptop. The local Orca app
  does the work. State lives in `orca.json`: `environment_id`, `runtime_id`,
  `local_port`, `remote_port`, `ready_file` per host.
- calport runs Orca **on the box** through calportd
  (`internal/box/providers.go`). The laptop never speaks to a runtime.

So the work is a new capability, not a translation. Three things get better
purely because calport has a paired daemon where tailmux had only SSH:

1. **No SSH.** tailmux SSHed in to `tail -c 1048576` the runtime's ready log.
   calportd reads the record locally and returns it over the already
   authenticated channel.
2. **Fewer moving parts to reach it.** `internal/wire/server.go`'s
   `dialLoopback` dials `127.0.0.1` then `::1` on the box, so no laptop-side
   SSH tunnel is needed to reach the runtime's port.

   This is *not* a security improvement, and an earlier draft of this document
   wrongly claimed one. `orca serve` has no flag to choose a bind address — the
   live runtime on devl listens on `0.0.0.0:6768` — so **tailmux's host-firewall
   consideration remains exactly as it was**. calport reaches the port over
   loopback, but anything else on the box's network can still reach it. Treat
   restricting non-loopback access to the runtime port as a box-hardening task
   that this work neither solves nor worsens.
3. **No manual service setup.** tailmux required the operator to preconfigure
   `tailmux-orca.service` before `orca serve` worked. calportd installs the
   unit through `internal/service`, so serving is a command.

## Layering

The transport and lifecycle are generic; the handshake is not. The seam sits
where Orca's protocol starts.

Generic — "run a long-lived thing on a box, reach it from a fixed local port":
install, start, stop and report a managed user unit, plus a *pinned* forward
whose local port is stable across restarts. Same shape whatever is served, and
already what calportd does for itself.

Orca-specific — the handshake: the `orca_server_ready` schema, the rule that
the advertised endpoint is baked into the pairing code (which is *why* the port
must be pinned), `orca environment add|list`, and runtime-identity
verification. A set of one; generalising it would invent an abstraction over a
single case.

Herdr is **not** a second consumer. calport drives Herdr box-side only
(`providers.go:251`), with no remote-runtime pairing model. The split is
justified by the pinned-forward primitive being useful on its own and by
keeping the security-sensitive parse in one reviewable file, not by a future
consumer.

### Naming

`GET /v1/services` already means *detected* dev servers and their worktrees
(`internal/box/services.go`), and `internal/service` is the launchd/systemd
renderer. Both names are taken, so the generic noun is **unit** — it matches
systemd and collides with nothing here.

### Where code goes

`internal/box` is already the box-side capability package (sessions, shares,
services, kit, ports as siblings). The generic half follows that shape instead
of adding a top-level package.

- `internal/box/units.go` — box side. Install, start, stop and report a
  managed user unit via `internal/service`. Peer of `sessions.go`.
- `internal/service` — one small fix: **honour `LogPath` on Linux**. Today only
  the macOS branch writes it (`StandardOutPath`); the `case "linux"` branch
  ignores `LogPath` entirely, so a unit's output goes to the journal. Add
  `StandardOutput=append:<LogPath>` and `StandardError=append:<LogPath>`
  (systemd 240+). This removes a macOS/Linux asymmetry that would otherwise
  bite any future unit, and it is what makes the next point possible.
- `internal/agent` — laptop side. `Forward` gains one optional field,
  `Pin string`, so a forward is findable by a stable key instead of a random
  ID. Empty for every existing forward, so `forwards.json` stays
  backward-compatible.
- `internal/orca/` — the only new package. Orca's protocol and nothing else.

Orca adds **no** box route. It calls `POST /v1/units` with
`{name: "calport-orca", program: "orca", args: ["serve", "--advertise", ...]}`.
That is the test of whether the seam is in the right place.

### Where the ready record lives

calportd owns the path rather than discovering it: it installs the unit with
`LogPath` set to `<calportd state>/units/<name>.log` and reads the record from
that file. The record therefore never enters the journal.

This is deliberate on two counts. First, it replaces tailmux's `ready_file`
configuration — nothing needs to be told where the log is, because calportd
chose it. Second, the journal is readable by the box user and persists, so
keeping a file that contains a pairing credential out of it narrows exposure to
one file calportd created.

The file is treated as append-only and read with a bounded read (tailmux read
the last 1 MiB; the same bound applies), so a long-lived runtime cannot grow
the read unboundedly.

## Wire routes

Registered in `box.Mount`'s existing `route(...)` table, authenticated like
every other route:

```
GET    /v1/units            list managed units and their state
POST   /v1/units            install + start  {name, program, args, env, advertise?}
DELETE /v1/units/{name}     stop + uninstall
GET    /v1/units/{name}     one unit's state, and its ready record if it wrote one
```

## Command surface

Generic verbs, one per route:

```
calport units BOX [--json]                    List managed units and their state
calport unit add BOX/NAME -- COMMAND...       Install and start a unit
calport unit get BOX/NAME [--json]            One unit's state
calport unit rm BOX/NAME                      Stop and uninstall
```

Orca verbs, matching tailmux's set one-for-one so existing habits carry over:

```
calport orca serve BOX          Install and start the runtime unit
calport orca connect BOX        Serve if needed, then tunnel and pair locally
calport orca status BOX         Restore the tunnel if absent, verify, report
calport orca exec BOX -- ARGS   Run an orca command against that box's runtime
```

`connect` is the one command that does the whole flow; `serve` exists for the
case where the runtime should run without the laptop pairing to it.

## State

Two files, each owned by one layer.

- `forwards.json` (existing, agent-owned) gains `"pin": "orca/<box>"` on the
  tunnel. `startSavedForwards` (`agent.go:502`) already runs saved forwards at
  agent start, and `run()` records failures that are retried on every health
  check, so tunnel persistence needs no new code.
- `orca.json` (new, `internal/orca`-owned, under `CALPORT_HOME`) holds per box
  `{environment_id, runtime_id, local_port, remote_port}` — tailmux's shape
  minus `ready_file`, which is unnecessary now calportd reads the record
  locally.

**Port allocation.** First free from base `16768`, persisted on first connect
and never recomputed: the pairing code embeds the port, so a moved port means
re-pairing. Reusing tailmux's base keeps existing environments on their ports
when connecting in the same order.

### How the runtime is invoked

Verified against the live runtime on devl and `orca serve --help` on both the
laptop and the box:

```
orca serve --json --pairing-address ws://127.0.0.1:<pinned>
```

`--pairing-address` sets only the client-advertised address, which is what the
record reports as `advertisedEndpoint` and what the pairing code embeds. No
`--port` is passed: the runtime uses its default and the record reports the
result as `boundEndpoint`, which is authoritative. If something already holds
that port — a tailmux-era runtime during migration — the unit fails to start
with a clear error, which is the correct outcome while replacing tailmux.

The box's `~/.local/bin/orca` accepts this same form; the `orca-ide --serve
--serve-port --serve-pairing-address` form visible in a running process is the
Electron binary underneath the wrapper and is not what calport calls.

## Flow: `calport orca connect <box>`

1. Read or allocate the box's pinned local port.
2. Ask calportd to serve, advertising `ws://127.0.0.1:<pinned>`.
3. calportd installs the `calport-orca` unit via `internal/service`, bound to
   box loopback, starts it, waits for the ready record, returns it.
4. Validate the record (below).
5. Ask the agent for a forward `<pinned> → boundPort`, pinned `orca/<box>`.
6. `orca environment list`; reuse the environment named `<box>` if its endpoint
   matches, else `orca environment add --name <box> --pairing-code <url>`.
7. Verify `orca status --environment <id>` reports `Reachable` and a
   `runtimeId` equal to the record's. Mismatch aborts.
8. Persist the route to `orca.json`.

`status` and `exec` restore the tunnel if absent and re-verify identity before
acting, not only at connect time.

## Validation and security

The record is the security boundary. Its `advertisedEndpoint` decides what the
**local** Orca app connects to, and its `pairing.url` is a **credential**. The
box is paired and trusted, but a misconfigured or compromised box must not be
able to aim the local Orca at an arbitrary endpoint. The record is therefore
validated as untrusted input despite arriving over an authenticated channel.

Ported from tailmux's `parseOrcaReady`, which already got these right:

- Keep the **last** line with `type == "orca_server_ready"`. A restarted
  runtime appends; earliest-wins would pin stale data.
- Require `schemaVersion == 1`, non-empty `runtimeId`, `pairing.available`,
  non-empty `pairing.url`.
- `advertisedEndpoint`: scheme `ws`; hostname loopback; **no userinfo, no
  query, no fragment**; path empty or `/`. Userinfo would smuggle credentials
  into a URL that looks local.
- `boundEndpoint`: scheme `ws` and a valid port. **Its host is deliberately not
  constrained**: `orca serve` binds `0.0.0.0`, so requiring loopback here would
  reject every real record.
- Both ports in 1–65535.
- **`pairing.endpoint` must equal `advertisedEndpoint`** exactly — the check
  that stops a record advertising one endpoint while pairing against another.

One rule calport adds, enabled by the paired daemon:

**Advertised port must equal the pinned port.** tailmux *derived* the local port
from the record because it only tailed a log. calport *dictates* it via
`--pairing-address`, so it verifies the runtime honoured it. A mismatch is an
error, not something to adapt to.

**Credential containment.** The pairing URL must never reach a log, an error
message, an event, `calport doctor`, or `--json`. tailmux enforced this by
having `orcaQuery` discard argv and raw output on error; that rule holds and
extends to the box side — calportd returns the record and must not log it. The
record is passed by value to the pairing step and never handed to `a.publish`
or `cfg.Log`. Errors name which check failed, never the value.

This is also why **failure reporting never tails the journal**: `orca serve`
writes its pairing URL into its own output, so a journal tail would leak the
credential into an error message. Failures report unit state and exit status
only.

**Rollback.** Connect creates up to two things, a forward and a local
environment. Any later failure undoes both, and undoes *only* what this call
created — a reused environment must never be deleted. tailmux's
`success`/`created`/`newEnvironment` defer pattern handles exactly this and is
mirrored.

## Errors

| Failure | Behaviour |
|---|---|
| `orca` missing on the box | Fail before installing anything, wording matching `doctor.ToolCheck` |
| `orca` missing on the laptop | Fail up front |
| Unit installed but will not start | Report unit state and exit status, never raw journal output |
| No ready record within 60s | Bounded wait, then point at `calport unit get <box> calport-orca` |
| Pinned local port taken | Allocate next free from 16768 and persist; `forwards.add` already rejects duplicate local ports |
| Environment named `<box>` exists with a different endpoint | Refuse; never silently re-pair |
| Runtime identity mismatch | Refuse; say to re-connect |

## Tests

Written first, failing, before implementation. Fakes follow the pattern in
`providers_test.go`: a shell script named `orca` in a temp dir, prepended to
`PATH` with `t.Setenv`.

- **Parse rejection table** — tailmux's existing cases (non-loopback host,
  port 0, port 70000, trailing path, userinfo, `http` scheme,
  `pairing.endpoint != advertised`) plus advertised port ≠ pinned, and
  `boundEndpoint` on `0.0.0.0`.
- **Last-record-wins** — a stale record followed by a current one pins the
  current runtime.
- **Rollback** — fake `orca` succeeding on `environment add` then failing on
  `status`: the environment is removed and the forward is gone. Inverse: a
  **reused** environment survives a later failure.
- **Credential containment** — a sentinel pairing URL driven through every
  failure path appears in no error string and no captured log output.
- **`internal/box/units.go`** — install/start/status against redirected
  `internal/service` paths.
- **`internal/service`** — the rendered Linux unit contains
  `StandardOutput=append:` for a spec with `LogPath`, and is unchanged for one
  without. Guards the asymmetry from returning.
- **Record reading** — a log larger than the read bound still yields the last
  record.
- **Agent** — `Pin` round-trips through `forwards.json`; an existing forwards
  file with no `pin` key still loads.

## In scope because this work needs it

`doctor.Tool()` misses `/opt/homebrew/bin` and `/usr/local/bin`, so
Homebrew-installed tools read as absent when checks run from the app's minimal
PATH (`internal/doctor/doctor.go:36`). `internal/orca` resolves the local Orca
CLI through `Tool`, so pairing would fail from Calport.app until this is fixed.
It lands first.

## Out of scope

Separate small changes, not part of this work:

- No box rename; parity fixes need forget + re-pair.
- The release CLI ships without `calportd-linux-*` beside it, so
  `calport add ssh` fails from `~/.local/bin`.
- `add ssh`'s 1Password fallback did not authenticate where `ssh` with
  `SSH_AUTH_SOCK` set to 1Password's socket succeeds.
