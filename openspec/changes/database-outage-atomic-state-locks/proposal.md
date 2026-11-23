---
version: 7
---

Issue: #3931.

Roadmap: `docs/roadmap.md:475` (Make state and lock commits atomic with fencing.).

## Why

**Persona: developer.** A Developer runs OpenTofu against the server. During a database outage a write can stop in the middle of a commit. The developer then faces a half-written state version or a workspace lock that nobody holds yet nobody can acquire. This change makes each commit atomic so a retry after the outage works without manual repair.

The user path this change must make work:

1. **CLI.** Run `tofu apply` against the server while the database stops mid-commit, then run `tofu plan` after the database returns.
2. **Docs page.** Read the recovery docs page for the retry step after a stored-write error.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| OpenTofu apply fails during the outage with a stored-write error. | The server rolls back the half-written state and reports a stored-write error in the TFE-compatible shape defined by part 1. Run the same command again after the database returns with no manual unlock. | `tofu apply` |

Evidence for the claims above:

- Main has separate TFE state version write and workspace lock paths. Proof: internal/api/tfe/states.go:602 (func Handler.writeStateVersion) and internal/api/tfe/locks.go:134 (func Handler.lockWorkspace)
- Main has a Postgres state version finalize path. Proof: internal/store/postgres/workspaces.go:690 (func Store.FinalizeStateVersion)
- Main has a TFE unlock path. Proof: internal/api/tfe/locks.go:266 (func Handler.unlockWorkspace)

## What Changes

- Wrap TFE state version commits in one fenced store transaction with full rollback on failure.
- Wrap workspace lock acquire and release in one fenced store transaction with holder identity.
- Keep a committed lock held until a release or a force-unlock. The lock has no expiry and no takeover, as on main.

Out of scope:

- 503 shape and Retry-After text from part 1
- Long and short outage e2e steps and CLI retry measurement
- Pulumi checkpoint and update atomicity
- Lock refresh behavior change

Depends on:

- database-outage-503-shape
- `database-outage-503-shape` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A failed apply during an outage still costs one retry after the database returns, with no manual unlock step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Handler writes a TFE state version. (`internal/api/tfe/states.go:602 (func Handler.writeStateVersion)`)
- Handler creates a TFE state version record. (`internal/api/tfe/state_write.go:264 (func Handler.writeStateVersionCreate)`)
- Postgres store finalizes a state version. (`internal/store/postgres/workspaces.go:690 (func Store.FinalizeStateVersion)`)
- Handler locks a workspace. (`internal/api/tfe/locks.go:134 (func Handler.lockWorkspace)`)
- Handler unlocks a workspace. (`internal/api/tfe/locks.go:266 (func Handler.unlockWorkspace)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the expiry time of a committed workspace lock before fenced takeover?
   - Options: 30 s lock expiry with takeover after expiry; 60 s lock expiry with takeover after expiry; No expiry with manual release only.
   - Provisional decision (PM, rule 88): No expiry with manual release only. The lock stays held until `tofu unlock` or force-unlock releases it.
   - Reason: Main has no lock expiry and no lock refresh path (the workspace port lock fields and `internal/api/tfe/locks.go:148-159`), and the OpenTofu CLI sends no heartbeat. A 30 s takeover would evict a live apply that runs longer than 30 s. Lock refresh is out of scope. Failover readiness keeps the lock of an OpenTofu workspace as it is. Benchmark: the TFE-compatible API holds a workspace lock until an explicit unlock or force-unlock; Google and AWS use expiry only with a heartbeat.
   - Persona impact: After the loss of a holder instance the Developer runs force-unlock once. The lock does not evict a live apply. Escape hatch: the existing force-unlock route.
2. How is the fence token formed and checked for state version and lock commits?
   - Options: Failover version plus lock identity checked in the same transaction; Separate fencing token column checked in the same transaction.
   - Provisional decision (owner review 14 Nov): Failover version plus lock identity checked in the same transaction
   - Reason: The fenced write cannot be built or tested without the exact fields that the store checks and the stale-fence error. Benchmark: Google: each fenced write carries a monotonic token that the store checks in the same transaction and rejects when stale.
   - Persona impact: No persona impact; the token never shows in user output. Escape hatch: A rejected stale write retries from a fresh read of the current fence.

Questions considered and answered:

- Does this change also change the 503 shape or retry behavior? No. The roadmap scope for this part keeps the 503 shape in part 1. This part makes commits atomic and leaves the error shape unchanged.
- Does this change add the long and short outage e2e steps? No. The roadmap scope states this part does not add the outage test steps or retry measurement. Those belong to the probe part.
- Which ADRs were checked for conflicts? The context pack lists no ADRs or RFCs. No ADR decision text was supplied to quote. The change follows the roadmap scope text and does not claim an ADR conflict check beyond the empty pack.
- Does this change cover Pulumi checkpoint atomicity? Pulumi checkpoint atomicity is out of scope for this part. The change covers only OpenTofu state versions and workspace locks. The Pulumi path was removed from this change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
