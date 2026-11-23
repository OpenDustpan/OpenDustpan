---
version: 3
---

## Why

**Persona: namespace administrator.** Namespace administrator wants to define which cloud accounts a sweep may read, then start, cancel and list sweeps, without giving the server a cloud credential. The sweep runs on a runner pool of the namespace that has the sweeps purpose.

The user path this change must make work:

1. **API.** Create a sweep target with the NEW route `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets`, with a pool of the namespace, scopes and a command, using an access token.
2. **API.** Start, list and cancel sweeps with the NEW routes under `/api/dustpan/v1/namespaces/{namespace}/sweeps`.
3. **CLI.** Run the same calls with the generated CLI of the API description.
4. **Docs page.** Read the target guide: command rules, read-only identity note and the error list.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 404 unmanaged.target-not-found | Target not found in this namespace. List the targets. | `GET /api/dustpan/v1/namespaces/{namespace}/sweep-targets` (NEW route) |
| 409 unmanaged.scan-busy | The target has a sweep that did not end. Cancel it or wait. | `GET /api/dustpan/v1/namespaces/{namespace}/sweeps?target={id}` (NEW route) |
| 422 unmanaged.pool-not-sweeps | The pool has no sweeps purpose, or it is a pool of another namespace. Use a pool of the namespace with the sweeps purpose. | `GET /api/dustpan/v1/namespaces/{namespace}/sweep-targets` (NEW route) |
| 422 unmanaged.command-invalid | The command breaks the rules: 1 to 256 arguments, 131072 bytes, no NUL byte, bare or clean absolute program. The error repeats nothing of the input. | `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets` (NEW route) |
| 409 unmanaged.target-disabled | The target is disabled. Enable it, then start again. | `GET /api/dustpan/v1/namespaces/{namespace}/sweep-targets` (NEW route) |

Evidence for the claims above:

- The lifecycle states and the busy refusal are decided. Proof: ADR 164 decisions 14 and 15 in the pack.
- A pool delete fails while a target names the pool. Proof: ADR 130 decision 10 in the pack.
- The pool purpose never changes. Proof: ADR 130 decision 3 (owner) in the pack.
- No target or sweep route exists on main. Proof: The main list of the pack holds no sweep-target route and no sweep route.

## What Changes

- NEW SQL tables for sweep targets and sweeps in the baseline of each dialect, with the ScanStore methods and the fence
- NEW routes `/api/dustpan/v1/namespaces/{namespace}/sweep-targets`, `/sweep-targets/{id}`, `/sweeps`, `/sweeps/{id}` and `/sweeps/{id}/cancel`
- NEW actions `namespace:manage-sweeps` and `namespace:start-sweep`
- NEW audit entries for target create, change and delete, with the command
- NEW errors `unmanaged.target-not-found`, `unmanaged.scan-busy`, `unmanaged.pool-not-sweeps`, `unmanaged.command-invalid`, `unmanaged.target-disabled`, `unmanaged.scan-not-cancelable`
- add the new package internal/unmanagedsvc and the new package internal/api/unmanaged
- Docs page for targets

Out of scope:

- Context entries, graph and focus view
- Claim, heartbeat and the states after queued (runner protocol part)
- Report intake and findings
- Schedule and state check
- Choice of sweeper program

Depends on:

- 130-138-runner-protocol-claims
- `160-165-context-graph-focus` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The administrator learns the command rules before the first save. The error names the rules and repeats nothing.
- A busy error shows the open sweep in the list by target.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A helper for the state check offset exists. No target or sweep store exists. (`internal/unmanaged/scan.go:50 (func StateCheckOffset)`)
- The focus view exists in pkg/graph and belongs to the first part. (`pkg/graph/focus.go:48 (type FocusView)`)
- Context error codes exist. They are not changed here. (`internal/errcode/catalog_context.go:7 (func context.field-from-code)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 23 Nov 2026 (ADR 396, replaces the 13 Nov 2026 answer P14)**: sweeps run on the user's own runners. The owner answered: "Sweeps must run on the user's own runners as we most recently re-designed it." A sweep runs on a runner pool of the namespace that has the sweep purpose. The host identity of the pool has no write access. No installation sweep pool exists. The pool API accepts the sweep purpose on a namespace pool with the same rules as for any other purpose.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current), 7 (current), 8 (current), 14 (changed), 15 (changed), 18 (current), 19 (changed), 20 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "A sweep is not a job. A job belongs to one unit, and the one-change rule, the phases and the gates of ADR 100 serve changes to a unit."
- Quote: "The store refuses a second sweep of the same target that did not end (`ErrScanBusy`)."
- Effect: Start stores the sweep and its durable operation in one transaction. A second open sweep is refused. Fence on each write.
- Conflict: none.

**ADR 130: Runner pools, runners and coordinators** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed), 10 (current).
- Changed by: ADR 164 changes decision 5; ADR 300 changes; ADR 343 changes decision 3. Chain to the end: ADR 164, ADR 300, ADR 343, ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 164, 300, 343.
- Quote: "A runner of a pool for sweeps claims only `unmanaged.sweep.<pool ID>`."
- Quote: "A delete of a pool fails with `runner.ErrPoolInUse` while a job that is not terminal or a sweep target names it."
- Effect: A target accepts only a pool of the namespace with the sweeps purpose. The purpose is never changed. A pool in use is not deleted.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: No cloud code in the server.
- Conflict: none.

**ADR 396: Sweeps run on the team's own runners, and the CLI does not audit its own usage errors** (owner-tagged: decision 1).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR, with ADR 164, 343.
- Quote: "A sweep runs on a runner pool of the team that has the sweep purpose. The host identity of the pool has no write access."
- Effect: A sweep target names a pool of the namespace with the sweep purpose. The server refuses a pool without that purpose. No installation sweep pool exists.
- Conflict: none. ADR 396 changes ADR 164 decision 5. It extends ADR 343 decision 3.

## Open questions

none

Questions considered and answered:

- Which pools may a target name? Owner decision, 23 Nov 2026 (ADR 396): a pool of the namespace with the sweeps purpose. The server refuses a pool without it with 422 unmanaged.pool-not-sweeps.
- Does a sweep use the job model? No. ADR 164 decision 2.
- Can the pool purpose change under a target? No. ADR 130 decision 3 (owner) forbids it. ADR 130 decision 10 refuses a pool delete.
- Final route paths? ADR 210 puts tool-neutral routes under /api/dustpan/v1, so the NEW paths above are used with no alias, because no tag shipped the proposal paths of ADR 164.
- Restore, purge, failover? Targets are data people wrote, so they are backed up. Sweeps are rebuildable run data and a restore leaves them out, as ADR 166 decisions 4 and 5 do for classes. A purge deletes rows of the namespace ID, as ADR 166 decision 11. Writes carry the fence.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
