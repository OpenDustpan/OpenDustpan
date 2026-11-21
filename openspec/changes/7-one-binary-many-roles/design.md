## Context

An operator runs one server process on a laptop and replicas of one server process in production, with no extra processes to learn.

This coarse change records that one binary with shared storage per installation stays until a runner need forces a new role. It makes no code change now and changes no server code, no route, and no schema.

## Decisions

1. **No code now.** One binary with shared storage per installation stays. Runners and remote runs (RFC 6) are the known second role that needs its own process when it lands.
2. **Optional modules and the capabilities document stay out.** They refine on their own path; this change fixes the process shape only.
3. **Runner hosting and runner protocol work stay out.** They ship with runners, not here.
4. **Kubernetes operator packaging stays out.** It follows the operator changes, not the binary shape.

## Structure

No new structure now. The bug class is a second process that shares storage without a role boundary; one binary per installation with a named second role only when a runner need forces it is the structure that removes it. When runners land, the role is an explicit process mode set at start, not a flag read at each call site.

No custom analyzer and no new tool.

## Risks

- A runner need forces a second role before the design exists. The change names the trigger (a runner need), so the split starts from this record instead of from scratch.
- Replicas diverge in behavior. Replicas run the same binary against shared storage, so behavior stays in one codebase.
