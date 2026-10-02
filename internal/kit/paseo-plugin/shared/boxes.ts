import { defineRpc } from "@getpaseo/plugin";
import { z } from "zod";

const usage = z.object({ total: z.number(), used: z.number() });

export const boxStats = z.object({
  name: z.string(),
  state: z.string(),
  network: z.string().optional(),
  latencyMs: z.number().optional(),
  // Absent while a box is offline, or when its daemon did not answer in time:
  // a box being unreachable is ordinary, not an error worth hiding the rest of
  // the list for.
  hostname: z.string().optional(),
  uptimeSeconds: z.number().optional(),
  cpus: z.number().optional(),
  load: z.array(z.number()).optional(),
  memory: usage.optional(),
  swap: usage.optional(),
  disks: z.array(usage.extend({ mount: z.string() })).optional(),
  agents: z
    .array(
      z.object({
        tool: z.string(),
        state: z.string(),
        worktree: z.string().optional(),
      }),
    )
    .optional(),
  detail: z.string().optional(),
});

export const boxesStats = defineRpc({
  name: "boxes.stats",
  input: z.object({}),
  output: z.object({
    // calport is the agent on the machine this daemon runs on. A box's own
    // daemon has no calport, so the surface says so rather than looking broken.
    available: z.boolean(),
    detail: z.string().optional(),
    boxes: z.array(boxStats),
  }),
});
