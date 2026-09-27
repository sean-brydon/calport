# Calport design

Calport connects a laptop to remote development boxes — any VPS, on a tailnet or
not — and makes the services, worktrees, and agent sessions on them usable as
if they were local. Nothing is public unless someone explicitly shares it.

## Principles

- **Private by default.** Services stay on the box. They are reachable only
  through an authenticated connection from a paired laptop. calportd itself
  listens on the box's tailnet address, never the internet, unless told to.
- **The box serves, the laptop connects.** The box owns its services, worktrees,
  sessions, and public shares; the laptop holds connections and local URLs.
  Nothing a teammate depends on runs only on someone's laptop.
- **No runtime dependencies.** calportd is one static binary; the app bundles
  everything. No SSH agent, DNS zone, or `/etc/hosts` edit is needed, and SSH
  is used once at most, to install.
- **Survive the real world.** Laptops sleep, networks change, daemons upgrade.
  Connections reconnect indefinitely, sessions outlive the daemon, and state
  files are never silently emptied.

## Components

```
laptop                                                   box (any VPS)
┌──────────────┐  local API   ┌────────────────┐  TLS 1.3 + HTTP/2   ┌──────────────┐
│ desktop app  │─────────────▶│ calport agent  │════════════════════▶│   calportd   │
│ calport CLI  │ (Unix socket)│  box health    │  pinned keys, one   │ locations    │
│ hooks, skill │              │  forwards      │  connection per box │ sessions     │
└──────────────┘              │  *.localhost   │                     │ shares       │
                              │  tailnets      │                     │ events, hooks│
                              └────────────────┘                     └──────────────┘
```

- **calportd** runs on each box as a systemd user service (launchd on macOS). It
  holds the box identity, the trust store of paired laptops, and the box's
  locations, sessions, shares, and hooks. Box-local tools reach it through a
  private Unix socket with the same API.
- **The calport agent** runs on the laptop under launchd. It keeps a connection
  to every box, runs forwards and the `*.localhost` proxy, relays every box's
  events, runs laptop hooks, and owns the embedded tailnet nodes. The CLI and
  the app talk to it over a private Unix socket, so closing the app drops
  nothing.
- **The desktop app** is a Tauri shell. Every action runs the bundled `calport`
  binary with `--json`; the app holds no logic of its own.

## Identity and transport

Every installation has an Ed25519 key; its identity is the SHA-256 of the key's
SubjectPublicKeyInfo. Certificates are regenerated on every start and carry no
meaning beyond the key.

Connections are TLS 1.3 with mutual certificates. Each side accepts only the
other's pinned key, never a certificate authority. On top, HTTP/2 multiplexes
pings, API calls, TCP streams, and terminals over one connection per box, with
health pings to notice a connection that died silently.

An unpaired client can complete a handshake but only attempt pairing, which is
rate limited. Every other request is refused with the same answer, whatever the
reason. A trust store that cannot be read authorizes nobody.

## Pairing

1. On the box, `calportd pair` stores a random 256-bit single-use code (10
   minute expiry) and prints `calport://HOST:PORT?code=…&fp=<box fingerprint>`.
2. The laptop connects to `HOST:PORT` and pins `fp`.
3. The laptop proves it holds the code without sending it: an HMAC keyed by the
   code over TLS exported keying material (RFC 5705) and the laptop's own
   fingerprint, so the proof is bound to this session and this key.
4. The box consumes the code under a file lock and pins the laptop's key.

`calport add ssh HOST` does all of this over one SSH session: it detects the
box's platform, uploads the matching calportd, installs the service, and pairs.

## Where calportd listens

By default calportd listens on the box's tailnet address only and refuses to
start if there is none. Listening on the internet takes an explicit
`--listen 0.0.0.0:7443`. `calportd pair` advertises the address it actually
listens on.

## Reaching other tailnets

A laptop's system Tailscale can be on only one tailnet. For boxes on another
(a personal tailnet while the Mac is on a work one), the agent runs an embedded
Tailscale node per named network (`calport network login personal`, a one-time
browser sign-in). Each paired box records the network it is reached through;
pairing, the agent's connections, and `add ssh` all dial through it. calport's
own pinned TLS still runs on top: the tailnet only provides the route.

## Local URLs and forwards

Every port on a box is reachable at `http://<port>.<box>.localhost:1355/`. All
operating systems resolve `*.localhost` to loopback, so no DNS is involved. The
proxy presents requests to the app as `localhost:<port>` and rewrites the app's
own redirects back, so dev servers need no configuration. Unknown hosts are
refused, which blocks DNS rebinding. Fixed-port forwards listen on both
loopback addresses and refuse a port another program already holds on `::1`,
where `localhost` resolves first on macOS.

## Locations, worktrees, sessions

A **location** is a named directory on a box, usually a git repository. Its
worktrees are read from `git worktree list` and addressed as `location/name`.
Worktrees are created by git (next to the repo, as `<repo>-<name>`), Orca, or
Herdr. calportd watches locations, so worktrees made by any tool appear and
produce events.

A **session** runs a program, usually a coding agent, at a location, in
calport's own tmux server with `remain-on-exit`. Sessions keep running when
nobody is attached and when calportd restarts or upgrades (`KillMode=process`).
Laptops attach through a framed terminal stream (keystrokes and resizes in,
screen out) or read the screen without attaching.

## Public shares

Sharing is opt-in per port and confirmed in the app. The **box** runs a
Cloudflare quick tunnel, so a link survives the laptop sleeping. Shares end when
revoked or when calportd stops; none outlive the daemon that manages them.

## Upgrades

`calport upgrade BOX` uploads the matching daemon over calport's own connection.
The box runs the upload once and requires it to report the box's own
fingerprint, swaps it in, and re-executes in place, keeping its PID so the
supervisor sees no restart and sessions are untouched.

## Events, hooks, and integrations

One event model on both sides, each event recording the tool it came from.
Hooks run commands for matching events; a hook that names the tool it drives
never reacts to that tool's own events, which prevents loops between
integrations. Agent tool hooks forward only identifiers and paths, never
prompts. See [integrations.md](integrations.md).

## Status

Done and exercised against a real box (devl), over the internet and across
tailnets:

- identity, pairing, pinned mutual TLS, HTTP/2 streams, rate limiting
- laptop agent: health, sleep detection, forwards, `*.localhost` proxy, events
- locations, worktrees (git, Orca, Herdr), change detection
- sessions with attach and screen reading, surviving daemon restarts
- public shares, `add ssh`, self-upgrade, other tailnets
- hooks, tool adapters, the agent skill
- desktop app with onboarding

## Open questions

- An independent review of the pairing protocol and the pre-authentication
  surface before teammates rely on it.
- Handshake flooding: pairing is rate limited, TLS handshakes are not.
- Team sharing: one box used by several people, and how access is granted and
  revoked beyond per-laptop pairing.
- Named services (`web.devl.localhost`) from a per-repository recipe; the proxy
  supports them, but nothing defines them yet.
- Windows, and dropping the proxy port with a privileged helper.
