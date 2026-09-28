// Preview mode: in a plain browser (outside the Tauri app) calport commands
// are answered from sample data, so the UI can be designed and checked with
// `pnpm dev`. It never runs anything. Add ?preview=new to start as a first-run
// user with no boxes.

const firstRun = new URLSearchParams(window.location.search).get("preview") === "new";
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();

const status = {
  boxes: firstRun
    ? []
    : [
        { name: "devl", address: "100.101.102.103:7443", network: "personal", fingerprint: "q7mzl2h4d8xk", state: "online", latency_ms: 48, since: ago(90) },
        { name: "dev-alex", address: "100.64.12.7:7443", fingerprint: "k3tqp9shd2mzab1c0x", state: "offline", error: "dial tcp 100.64.12.7:7443: i/o timeout", since: ago(12) },
      ],
  forwards: [{ id: "ca89419c", box: "devl", local: 23000, remote: 3000, state: "listening" }],
  routes: [{ pattern: "*.personal.cal.localhost", box: "devl", port: 18080 }],
  proxy: { port: 1355, url_port: 1355 },
};

function kitFixture() {
  const mode = new URLSearchParams(window.location.search).get("kit");
  if (mode === "none") return { installed: false, config: { host: "", root: "" } };
  return {
    installed: true,
    config: { host: "devl", root: "/home/alex/work/cal", orca: "/home/alex/.local/bin/orca" },
    pattern: "*.devl.cal.localhost",
    worktrees: [{ host: "fix-login-1bda8a.devl.cal.localhost", path: "/home/alex/orca/workspaces/cal/fix-login", port: 3010, active: true }],
  };
}

// Worktree tools the preview has "set up", so Set up changes what it shows.
const setUpTools = new Set<string>();

const previewTools = [
  { tool: "orca", name: "Orca", file: "orca.yaml", installed: true, detail: "Orca asks you to trust the scripts once." },
  { tool: "cursor", name: "Cursor", file: ".cursor/worktrees.json", installed: true, detail: "Cursor has no teardown hook; archive its worktrees with cal-archive." },
  { tool: "codex", name: "Codex", file: ".codex/environments/environment.toml", installed: true, detail: 'Select the "Cal.com worktree kit" environment once in the Codex app. Codex does not run setup over Remote SSH yet (openai/codex#23648).' },
  { tool: "superset", name: "Superset", file: ".superset/config.json", installed: false },
  { tool: "herdr", name: "Herdr", file: "/home/alex/.local/share/cal-worktrees/herdr-plugin", installed: true, opt_in: true, detail: "Opt-in: installs a Herdr plugin for your user on this box, which runs the hooks for Herdr worktrees of this checkout." },
];

function kitToolsFixture(written: string[] = []) {
  const tools = previewTools.map((t) => ({ ...t, state: setUpTools.has(t.tool) ? "configured" : "missing" }));
  return {
    root: "/home/alex/work/cal",
    written,
    tools: [
      ...tools,
      { tool: "conductor", name: "Conductor", state: "skipped", installed: false, detail: "Conductor makes worktrees on your Mac only, never on a box." },
      { tool: "claude", name: "Claude Code", state: "skipped", installed: false, detail: "Claude Code's WorktreeCreate hook replaces how it makes worktrees; run cal-setup in one instead." },
    ],
  };
}

function setUpKitToolsFixture(args: string[]) {
  const i = args.indexOf("--tool");
  const named = i >= 0 ? args[i + 1].split(",") : ["orca", "cursor", "codex", "superset"];
  const written = previewTools.filter((t) => named.includes(t.tool) && !setUpTools.has(t.tool)).map((t) => t.file);
  for (const t of named) setUpTools.add(t);
  return kitToolsFixture(written);
}

const fixtures: Record<string, unknown> = {
  status,
  networks: firstRun ? [] : [{ name: "personal", state: "Running", tailnet: "example.com", ips: ["100.64.0.10"] }],
  "agent status": { installed: false, running: true },
  locations: [
    {
      name: "cal",
      path: "/home/alex/work/cal",
      repo: true,
      worktrees: [
        { name: "cal", path: "/home/alex/work/cal", branch: "main", head: "5fdc8af8dd", main: true },
        { name: "feat-billing-dash", path: "/home/alex/work/cal-feat-billing-dash", branch: "billing/4-customer-credit", head: "62d41c1302" },
        { name: "fix-login", path: "/home/alex/orca/workspaces/cal/fix-login", branch: "alex/fix-login", head: "1bda8af513" },
      ],
      scripts: { setup: '"$HOME/.local/bin/cal-worktree" setup', archive: '"$HOME/.local/bin/cal-archive"', from: "orca" },
      cal: true,
    },
    { name: "scratch", path: "/home/alex/scratch", repo: false, scripts: {} },
  ],
  ports: [
    { port: 22, address: "0.0.0.0" },
    { port: 3000, address: "::", pid: 2211, process: "next-server (v1", command: "next-server (v16.3.6)" },
    { port: 3010, address: "::", pid: 2290, process: "next-server (v1", command: "next-server (v16.3.6)" },
    { port: 5432, address: "127.0.0.1", pid: 901, process: "postgres", command: "postgres -D /var/lib/postgresql/16/main" },
    { port: 6768, address: "0.0.0.0", pid: 1433, process: "orca", command: "orca serve --port 6768" },
  ],
  services: [
    { location: "cal", worktree: "cal", path: "/home/alex/work/cal", port: 3000, process: "next-server (v16.3.6)", main: true },
    { location: "cal", worktree: "fix-login", path: "/home/alex/orca/workspaces/cal/fix-login", port: 3010, process: "next-server (v16.3.6)" },
  ],
  // ?kit=none shows the kit offer; otherwise the box has the kit.
  kit: kitFixture(),
  discover: {
    user: "alex",
    machines: [
      { name: "devl", dns_name: "devl.example.ts.net", ip: "100.101.102.103", os: "linux", online: true, box: "devl" },
      { name: "dev-sam", dns_name: "dev-sam.example.ts.net", ip: "100.64.12.8", os: "linux", online: true },
      { name: "build-01", dns_name: "build-01.example.ts.net", ip: "100.64.12.9", os: "linux", online: true },
      { name: "studio", dns_name: "studio.example.ts.net", ip: "100.64.12.20", os: "macOS", online: true },
      { name: "old-staging", dns_name: "old-staging.example.ts.net", ip: "100.64.12.30", os: "linux", online: false },
    ],
  },
  stats: {
    hostname: "devl",
    uptime_s: 1_900_000,
    cpus: 8,
    load: [0.71, 1.2, 1.26],
    memory: { total: 67_200_000_000, used: 26_300_000_000 },
    swap: { total: 0, used: 0 },
    disks: [{ mount: "/", total: 468_000_000_000, used: 161_000_000_000 }],
    hooks: true,
    agents: [
      { tool: "claude", pid: 101, path: "/home/alex/orca/workspaces/cal/fix-login", location: "cal", worktree: "fix-login", state: "waiting", since: ago(3) },
      { tool: "claude", pid: 102, path: "/home/alex/work/cal", location: "cal", worktree: "cal", state: "running" },
      { tool: "codex", pid: 103, path: "/home/alex/work/cal", location: "cal", worktree: "cal", state: "finished", since: ago(12) },
      { tool: "claude", pid: 104, path: "/home/alex/work/cal-feat-billing-dash", location: "cal", worktree: "feat-billing-dash", state: "running" },
      { tool: "claude", pid: 105, path: "/home/alex/projects/notes", state: "running" },
    ],
  },
  info: { os: "linux", arch: "amd64", build: "33100c12520f", tools: ["orca", "herdr", "claude", "codex"] },
  doctor: [
    { area: "This computer", name: "starts at login", status: "ok", detail: "background agent installed" },
    { area: "This computer", name: "agent", status: "ok", detail: "running" },
    { area: "This computer", name: "local URLs", status: "ok", detail: "http://PORT.BOX.localhost:1355/" },
    { area: "This computer", name: "short URLs", status: "info", detail: "URLs include :1355", fix: "calport setup port80" },
    { area: "Boxes", name: "devl", status: "ok", detail: "online, 48ms" },
    { area: "Boxes", name: "dev-alex", status: "fail", detail: "not answering", fix: "calport doctor dev-alex" },
    { area: "Tools", name: "orca", status: "ok", detail: "/usr/local/bin/orca" },
  ],
  "doctor box": [
    { area: "calportd", name: "service", status: "ok", detail: "calportd.service, starts at boot" },
    { area: "calportd", name: "listening", status: "ok", detail: "100.101.102.103:7443 (tailnet only)" },
    { area: "Orca", name: "orca", status: "ok", detail: "/home/alex/.local/bin/orca" },
    { area: "Orca", name: "cal scripts", status: "ok", detail: "setup and archive from Orca" },
    { area: "Agents", name: "claude", status: "ok", detail: "/home/alex/.local/bin/claude" },
    { area: "Agents", name: "hooks", status: "warn", detail: "Claude Code does not report to calportd", fix: "calportd integrations install claude" },
  ],
  shares: [{ id: "e46f6100", port: 3010, url: "https://shelter-mileage-simply-ranges.trycloudflare.com", started: ago(7), state: "live" }],
};

function key(args: string[]): string {
  if (args[0] === "agent") return "agent status";
  if (args[0] === "doctor" && args[1] !== "--json") return "doctor box";
  return args[0];
}

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

export async function previewRun(args: string[]): Promise<string> {
  await delay(120);
  if (args[0] === "url") return `http://${args[2]}.${args[1]}.localhost:1355/\n`;
  if (args[0] === "share") return JSON.stringify({ id: "b71c02aa", port: Number(args[2]), url: "https://quiet-river-demo.trycloudflare.com", started: new Date().toISOString(), state: "live" });
  if (args[0] === "kit" && args[1] === "tools") return JSON.stringify(args.includes("--setup") ? setUpKitToolsFixture(args) : kitToolsFixture());
  if (args[0] === "kit" && args[1] === "reclaim")
    return JSON.stringify({ dry_run: args.includes("--dry-run"), reclaimed: [{ key: "a1", host: "old-a1b2c3.devl.cal.localhost", path: "/home/alex/orca/workspaces/cal/old", port: 3104, bytes: 1_300_000_000 }], templates: [{ name: "caltpl_x", bytes: 900_000_000 }], waiting: [], bytes: 2_200_000_000 });
  if (args[0] === "kit" && args[1] === "install") return JSON.stringify({ kit: { ...kitFixture(), installed: true, pattern: "*.devl.cal.localhost", notes: [] }, routed: true });
  const k = key(args);
  if (k in fixtures) return JSON.stringify(fixtures[k]);
  return "{}";
}

export async function previewStream(args: string[], onLine: (line: string) => void): Promise<void> {
  const lines =
    args[0] === "network"
      ? [`Sign in to the tailnet for "${args[2]}":`, "https://login.tailscale.com/a/preview", `Network ${args[2]} is connected to example.com.`]
      : [`Checking ${args[2]}…`, "Installing calportd-linux-amd64 (8 MB)…", "Installed calportd.service; serving on 100.101.102.103:7443.", `Paired with ${args[2]} at 100.101.102.103:7443. SSH is no longer needed for this box.`];
  for (const line of lines) {
    await delay(700);
    onLine(line);
  }
}
