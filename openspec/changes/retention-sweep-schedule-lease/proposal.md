---
version: 2
---

## Why

**Persona: operator.** An operator runs an installation with several server processes. Decision records of the state.write gate pile up unless a sweep prunes the expired ones. The pruning function exists, but nothing calls it on a schedule. With several server processes, two sweeps could run together and do the same work twice. The operator needs one sweep at a time, on a known schedule, with a record of each run.

The user path this change must make work:

1. **Server start.** The operator starts the server. The sweep schedule starts with the worker component and uses the retention setting that the server already checks.
2. **API.** The operator reads the effective retention and schedule values and the time of the last sweep through the settings resource at GET /api/dustpan/v1/settings.
3. **Docs page.** The operator reads the page for retention of decision records and sees how to read the last sweep.
4. **E2e.** A step starts the real binary with two server processes on one database and checks that one sweep runs at a time and that expired records are gone.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The retention value is too short. | The server refuses to start and names the minimum value. | `GET /api/dustpan/v1/settings` |
| The sweep lease is held by another server process. | The process skips the run and logs the holder. No API call fails. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The paths for the sweep home exist on main. Proof: The pack lists internal/cron and pkg/dustpan/area_retention.go with exists true.
- The server refuses a short retention on main. Proof: The pack names the test TestNewServerRefusesShortRetention at pkg/dustpan/dustpan_test.go:832.
- A shared lease between processes exists for another area. Proof: The pack names TestTwoShippersShareOneSinkLease at pkg/audit/ship_test.go:937.
- The pack does not show a scheduled caller of pruneExpired. Proof: The roadmap line of the item names the sweep that calls pruneExpired as the follow-up to build. The pack has no keyword evidence for a caller, so the first task confirms it.

## What Changes

- Add the sweep in pkg/dustpan/area_retention.go (on main) that calls pruneExpired.
- Register its trigger in internal/cron (on main).
- Take a store lease on the lease primitive of main, so that one sweep runs at a time. The first task names its exact file and line from the code.
- Write an audit record of each sweep with the count of pruned records and the reason.
- Show the effective schedule and the last sweep in the settings resource.

Out of scope:

- The retention volume and the decision record content (the parent change retention-volume-state-write-decision).
- A new lease primitive.
- A change of the sweep of deleted units or of token policy.
- A runner pool with the sweep purpose: this sweep runs in the server worker only.

Depends on:

- retention-volume-state-write-decision
- `retention-volume-state-write-decision` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator sees the schedule and the last sweep in one API read.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The server refuses a short retention. (`pkg/dustpan/dustpan_test.go:832`)
- A shared lease test exists for audit shippers. (`pkg/audit/ship_test.go:937`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the change conflict with an ADR? The pack lists no ADR, so there is no decision text to quote. No conflict is shown.
- Does the lease need a new primitive? No. The item says to build on the lease primitive of main.
- Does the sweep run on user runners? No. Owner decision, 13 Nov 2026 (queue P14): a sweep purpose is only for an operator pool, never a namespace pool. This sweep runs in the server worker, so it stays inside that rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
