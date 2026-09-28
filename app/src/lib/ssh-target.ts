import type { Machine } from "@/lib/calport";

// sshTarget is what to pass to ssh for a discovered machine. On this
// computer's own tailnet its MagicDNS name resolves, and matches any ssh
// config entry for it; through a calport network only the address is dialled.
export function sshTarget(machine: Machine, user: string, network: string): string {
  const host = network ? machine.ip : machine.name;
  const login = user.trim();
  return login ? `${login}@${host}` : host;
}
