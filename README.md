# Calport

Connect your laptop to development boxes — any VPS or dev machine, on a tailnet
or not — and work on them as if they were local:

- **Private URLs for every service**: `http://3000.devl.localhost:1355/`, with no
  DNS, `/etc/hosts`, or port juggling. Nothing is public unless you share it.
- **Locations, worktrees, and agent sessions**: register a repo on a box,
  create worktrees (through git, Orca, or Herdr), start Claude Code or Codex in
  any of them, and read or attach to them from anywhere.
- **Integrations both ways**: agent tools drive calport through its CLI and a
  bundled skill; calport drives tools through hooks.
- **A desktop app** (Tauri) with onboarding, or the `calport` CLI.

See [docs/design.md](docs/design.md) for the architecture and security model,
and [docs/integrations.md](docs/integrations.md) for Orca, Herdr, Cursor, Claude
Code, and hooks.

## Install

On a box (Linux, amd64 or arm64), this downloads calportd from the latest
release, verifies it against `SHA256SUMS`, starts it at boot, and prints a
pairing link:

```sh
curl -fsSL https://raw.githubusercontent.com/sean-brydon/calport/main/install.sh | sh
```

On your laptop, paste that link into the app, or install the CLI and pair:

```sh
curl -fsSL https://raw.githubusercontent.com/sean-brydon/calport/main/install.sh | sh -s -- calport
calport pair '<link>'
```

`CALPORT_VERSION=v0.1.0` pins a release; `CALPORTD_LISTEN=<ip>:7443` sets the
address on a box that is not on a tailnet. If you can SSH to the box,
`calport add ssh <host>` does all of this for you instead.

## Quick start (from source)

```sh
make all                                  # calport, calportd, and Linux daemons in bin/
bin/calport add ssh my-box                # install on a box over SSH once, and pair
bin/calport ports my-box                  # what is running there
bin/calport url my-box 3000               # → http://3000.my-box.localhost:1355/
```

A box on a tailnet this laptop is not joined to (say a personal one, while
the Mac is on work):

```sh
bin/calport network login personal        # one-time browser sign-in
bin/calport add ssh devl --network personal
```

Without SSH access, run the install script on the box and paste the link it
prints into `calport pair '<link>'` or the app.

## Everyday commands

```sh
calport status                                   # boxes, forwards, proxy
calport locations devl                           # repos and their worktrees
calport worktree new devl/cal/fix-login --base main
calport kit install devl/cal                     # Cal.com: every worktree gets its own app and URL
calport kit tools devl/cal --setup               # …for worktrees Orca, Cursor, Codex and Superset make too
calport worktree open devl/cal/fix-login --tool orca --agent claude
calport services devl                            # dev servers by worktree
calport doctor devl                              # what the box sees
calport forward devl 3000                        # fixed local port
calport share devl 3000                          # public link, until unshare
calport upgrade devl                             # new daemon, no SSH, sessions kept
calport events                                   # everything, live
```

Every listing takes `--json`. `calport help` lists everything.

## The desktop app

```sh
make app-dev        # run it
make app-build      # build Calport.app
make release        # every install.sh asset + SHA256SUMS in dist/
```

It is a view over the bundled `calport` binary: every action runs it with
`--json`, so the app and the CLI never disagree. In a plain browser
(`cd app && pnpm dev`) it runs on sample data, for working on the UI; add
`?preview=new` to see first-run onboarding.

## Layout

| Path | What |
| --- | --- |
| `cmd/calport` | Laptop CLI and background agent |
| `cmd/calportd` | Box daemon |
| `internal/wire` | Pinned mutual TLS, pairing, HTTP/2 streams |
| `internal/agent` | Laptop agent: box health, forwards, proxy, events, local API |
| `internal/box` | Locations, worktrees, sessions, shares, ports, self-upgrade |
| `internal/network` | Embedded Tailscale nodes for other tailnets |
| `internal/proxy`, `internal/forward` | `*.localhost` proxy and TCP forwards |
| `internal/hooks`, `internal/integrations` | Hooks, tool adapters, the skill |
| `internal/terminal` | PTYs and raw-mode terminals, no dependencies |
| `app/` | Tauri + React desktop app (coss ui) |

## Development

```sh
make test           # go vet + go test -race
```

The Go module's only third-party dependency is `tailscale.com`, for reaching
boxes on other tailnets.
