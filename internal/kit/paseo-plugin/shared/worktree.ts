import { defineRpc } from "@getpaseo/plugin";
import { z } from "zod";

// state is what the panel has to tell the user, and each value means something
// different to them: "absent" is "this is not a Cal.com worktree, nothing to
// see"; "pending" is "wait, the port and database are still being made";
// "failed" is "work here if you like, but the dev server will not come up".
export const worktreeState = z.enum(["absent", "pending", "ready", "failed"]);

// The database password is deliberately absent. The panel needs to name the
// database, not hand out credentials to anyone the screen is shared with.
export const worktreeInfo = defineRpc({
  name: "worktree.info",
  input: z.object({ directory: z.string() }),
  output: z.object({
    state: worktreeState,
    detail: z.string().optional(),
    path: z.string().optional(),
    port: z.string().optional(),
    url: z.string().optional(),
    database: z.string().optional(),
    databaseHost: z.string().optional(),
    // serving is whether something answers on the port now, which is the
    // difference between "set up" and "running".
    serving: z.boolean().optional(),
  }),
});
