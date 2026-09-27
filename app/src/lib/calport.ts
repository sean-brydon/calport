import { Command } from "@tauri-apps/plugin-shell";

// Every type mirrors the JSON the calport CLI prints with --json.

export type BoxState = "connecting" | "online" | "offline" | "untrusted";

export interface BoxStatus {
  name: string;
  address: string;
  network?: string;
  fingerprint: string;
  state: BoxState;
  error?: string;
  latency_ms?: number;
  since: string;
}

export interface ForwardStatus {
  id: string;
  box: string;
  local: number;
  remote: number;
  state: "listening" | "failed";
  error?: string;
}

export interface Status {
  boxes: BoxStatus[];
  forwards: ForwardStatus[];
  routes: Route[];
  proxy: { port: number; url_port: number; error?: string };
}

export interface Worktree {
  name: string;
  path: string;
  branch?: string;
  head?: string;
  main?: boolean;
}

export interface Scripts {
  setup?: string;
  archive?: string;
  from?: "orca" | "calport";
}

export interface Location {
  name: string;
  path: string;
  repo: boolean;
  cal?: boolean;
  worktrees?: Worktree[];
  scripts: Scripts;
}

export interface KitWorktree {
  host: string;
  path: string;
  port: number;
  active: boolean;
}

export interface KitStatus {
  installed: boolean;
  config: { host: string; root: string; orca?: string };
  pattern?: string;
  worktrees?: KitWorktree[];
}

export interface KitInstall {
  kit: KitStatus & { notes?: string[]; hooks_from?: string };
  routed: boolean;
}

// The hooks Orca runs for a Cal.com repository once the kit is installed.
export const KIT_SETUP_HOOK = '"$HOME/.local/bin/cal-worktree" setup';
export const KIT_ARCHIVE_HOOK = '"$HOME/.local/bin/cal-archive"';

export interface Service {
  location: string;
  worktree: string;
  path: string;
  port: number;
  process?: string;
  main?: boolean;
}

export interface BoxInfo {
  os: string;
  arch: string;
  build: string;
  tools: string[];
}

export interface Route {
  pattern: string;
  box: string;
  port: number;
}

export interface Check {
  area: string;
  name: string;
  status: "ok" | "warn" | "fail" | "info";
  detail?: string;
  fix?: string;
}

export interface Share {
  id: string;
  port: number;
  url: string;
  started: string;
  state: string;
}

export interface Port {
  port: number;
  address: string;
  pid?: number;
  process?: string;
  command?: string;
}

export interface Network {
  name: string;
  state: string;
  tailnet?: string;
  ips?: string[];
}

export interface CalportEvent {
  type: string;
  time: string;
  box?: string;
  origin?: string;
  error?: string;
  data?: Record<string, unknown>;
}

// Lifecycle is what the event stream last said about a worktree's setup or archive.
export interface Lifecycle {
  kind: "setup" | "archive";
  state: "started" | "finished" | "failed";
  log?: string;
  error?: string;
}

export class CalportError extends Error {}

const SIDECAR = "binaries/calport";

// Outside the Tauri app there is no sidecar; answer from sample data instead.
const inApp = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;

// run executes the bundled calport with args and returns its stdout. Errors
// carry calport's own message, which is written for people.
export async function run(args: string[]): Promise<string> {
  if (!inApp) return (await import("@/lib/preview")).previewRun(args);
  const output = await Command.sidecar(SIDECAR, args).execute();
  if (output.code !== 0) {
    const message = output.stderr.trim().replace(/^calport: /, "");
    throw new CalportError(message || `calport ${args[0]} failed`);
  }
  return output.stdout;
}

// json runs a command that prints JSON with --json.
export async function json<T>(args: string[]): Promise<T> {
  const [cmd, ...rest] = args;
  return JSON.parse(await run([cmd, ...rest, "--json"])) as T;
}

// watchEvents streams the agent's events until the returned stop function is
// called. The agent relays every box's events, so this is the whole picture.
export async function watchEvents(onEvent: (e: CalportEvent) => void): Promise<() => void> {
  if (!inApp) return () => {};
  const command = Command.sidecar(SIDECAR, ["events", "--json"]);
  let buffer = "";
  command.stdout.on("data", (chunk: string) => {
    buffer += chunk;
    let newline = buffer.indexOf("\n");
    while (newline >= 0) {
      const line = buffer.slice(0, newline).trim();
      buffer = buffer.slice(newline + 1);
      if (line) {
        try {
          onEvent(JSON.parse(line) as CalportEvent);
        } catch {
          // A partial or foreign line is not an event.
        }
      }
      newline = buffer.indexOf("\n");
    }
  });
  const child = await command.spawn();
  return () => {
    child.kill();
  };
}

// stream runs a long command and reports each line it prints as it goes, so
// multi-step work (installing a box, signing in to a tailnet) can show progress.
export async function stream(args: string[], onLine: (line: string) => void): Promise<void> {
  if (!inApp) return (await import("@/lib/preview")).previewStream(args, onLine);
  const command = Command.sidecar(SIDECAR, args);
  let stderr = "";
  const lines = (chunk: string) => chunk.split("\n").map((l) => l.trim()).filter(Boolean);
  command.stdout.on("data", (chunk: string) => lines(chunk).forEach(onLine));
  command.stderr.on("data", (chunk: string) => {
    stderr += chunk;
  });
  const done = new Promise<number | null>((resolve) => command.on("close", (data) => resolve(data.code)));
  await command.spawn();
  const code = await done;
  if (code !== 0) {
    throw new CalportError(stderr.trim().replace(/^calport: /, "") || `calport ${args[0]} failed`);
  }
}

export interface AgentStatus {
  installed: boolean;
  running: boolean;
}

// AUTO_AGENT is "0" once someone turns start at login off, so launching the
// app does not turn it back on.
export const AUTO_AGENT = "calport.agent.auto";

export const calport = {
  agentStatus: async () => JSON.parse(await run(["agent", "status", "--json"])) as AgentStatus,
  installAgent: () => run(["agent", "install"]),
  uninstallAgent: () => run(["agent", "uninstall"]),
  installIntegration: (tool: "claude" | "cursor" | "codex") => run(["integrations", "install", tool]),
  networkLogin: (name: string, onLine: (line: string) => void) => stream(["network", "login", name], onLine),
  addSSHStreaming: (host: string, onLine: (line: string) => void, name?: string, network?: string) =>
    stream(["add", "ssh", host, ...(name ? ["--name", name] : []), ...(network ? ["--network", network] : [])], onLine),

  status: () => json<Status>(["status"]),
  networks: () => json<Network[]>(["networks"]),
  locations: (box: string) => json<Location[]>(["locations", box]),
  ports: (box: string) => json<Port[]>(["ports", box]),
  shares: (box: string) => json<Share[]>(["shares", box]),

  pair: (link: string, name?: string, network?: string) =>
    json<unknown>(["pair", link, ...(name ? ["--name", name] : []), ...(network ? ["--network", network] : [])]),
  addSSH: (host: string, name?: string, network?: string) =>
    run(["add", "ssh", host, ...(name ? ["--name", name] : []), ...(network ? ["--network", network] : [])]),
  forget: (box: string) => run(["forget", box]),
  upgrade: (box: string) => run(["upgrade", box]),
  setupPort80: () => run(["setup", "port80"]),
  removePort80: () => run(["setup", "port80", "--remove"]),
  doctor: (box?: string) => json<Check[]>(box ? ["doctor", box] : ["doctor"]),
  services: (box: string) => json<Service[]>(["services", box]),
  kit: (box: string) => json<KitStatus>(["kit", box]),
  installKit: (box: string, location: string) => json<KitInstall>(["kit", "install", `${box}/${location}`]),
  info: async (box: string) => JSON.parse(await run(["info", box])) as BoxInfo,
  importOrca: (box: string) => run(["location", "import", box, "orca"]),
  openWorktree: (box: string, location: string, worktree: string, tool: "orca" | "herdr", agent?: string) =>
    run(["worktree", "open", `${box}/${location}/${worktree}`, "--tool", tool, ...(agent ? ["--agent", agent] : [])]),
  setScripts: (box: string, location: string, setup: string, archive: string) =>
    run(["location", "scripts", `${box}/${location}`, ...(setup || archive ? ["--setup", setup, "--archive", archive] : ["--clear"])]),
  addRoute: (pattern: string, box: string, port: number) => run(["route", "add", pattern, box, String(port)]),
  removeRoute: (pattern: string) => run(["route", "rm", pattern]),

  forward: (box: string, local: number, remote: number) => json<unknown>(["forward", box, `${local}:${remote}`]),
  unforward: (id: string) => run(["unforward", id]),
  url: async (box: string, target: string | number) => (await run(["url", box, String(target)])).trim(),

  addLocation: (box: string, name: string, path: string) => json<Location>(["location", "add", `${box}/${name}`, path]),
  removeLocation: (box: string, name: string) => run(["location", "rm", `${box}/${name}`]),
  newWorktree: (box: string, location: string, name: string, opts: { base?: string; provider?: string; agent?: string; prompt?: string }) =>
    json<Worktree>([
      "worktree",
      "new",
      `${box}/${location}/${name}`,
      ...(opts.base ? ["--base", opts.base] : []),
      ...(opts.provider ? ["--provider", opts.provider] : []),
      ...(opts.agent ? ["--agent", opts.agent] : []),
      ...(opts.prompt ? ["--prompt", opts.prompt] : []),
    ]),
  removeWorktree: (box: string, location: string, name: string) => run(["worktree", "rm", `${box}/${location}/${name}`]),

  share: (box: string, port: number) => json<Share>(["share", box, String(port)]),
  unshare: (box: string, id: string) => run(["unshare", box, id]),
};

// serviceURL mirrors calport's own: no port once the port 80 redirect is in.
export function serviceURL(service: string | number, box: string, urlPort: number): string {
  return urlPort === 80 ? `http://${service}.${box}.localhost/` : `http://${service}.${box}.localhost:${urlPort}/`;
}

// kitURL is a Cal.com kit worktree's address, which its app redirects to.
export function kitURL(host: string, urlPort: number): string {
  return urlPort === 80 ? `http://${host}/` : `http://${host}:${urlPort}/`;
}

// worktreeURL is a worktree's own address: its dev server, named for it.
export function worktreeURL(worktree: string, location: string, box: string, urlPort: number, main = false): string {
  const host = main ? `${location}.${box}` : `${worktree}.${location}.${box}`;
  return urlPort === 80 ? `http://${host}.localhost/` : `http://${host}.localhost:${urlPort}/`;
}
