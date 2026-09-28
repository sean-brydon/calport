import type { Machine } from "@/lib/calport";

export interface SSHTarget {
  // host is what ssh connects to: the machine's name, so an ~/.ssh/config
  // entry for it (its user, key, or 1Password agent) applies.
  host: string;
  // options reach the machine by address through a calport network, whose
  // tailnet names this computer cannot resolve.
  options: string[];
}

export function sshTarget(machine: Machine, user: string, network: string): SSHTarget {
  const login = user.trim();
  return {
    host: login ? `${login}@${machine.name}` : machine.name,
    options: network ? ["-o", `HostName=${machine.ip}`] : [],
  };
}
