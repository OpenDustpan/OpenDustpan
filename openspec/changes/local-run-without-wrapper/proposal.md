Roadmap: `docs/roadmap.md:665` (A local run without the wrapper).

## Why

**Persona: developer.** A developer applies from a laptop with plain tofu apply. The run has no associated claim, so the server must serialize it with a whole-unit lock, hold a runner job that starts meanwhile until the lock ends, and show the holder so the team sees why work waited.

The user path this change must make work:

1. **CLI.** Run plain tofu apply against a workspace from a laptop with an access token.
2. **API.** Server grants the whole-unit lock at the lock call and holds a runner job that starts meanwhile in the queue.
3. **Console.** Read the workspace and unit history to see the plain holder, the interval, the end cause, and the wait reason.
4. **Docs page.** Follow the guide for plain run lock behavior and the wrapper alternative.
5. **E2e.** Replay a plain apply during a running runner apply against real OpenTofu with a fake provider log.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Runner job starts while a plain run holds the lock | Server holds the runner job in the FIFO whole-unit lock queue behind the named plain holder, and the job starts after release, expiry, or force-unlock. | `tofu apply -lock-timeout=10m` |
| Second plain run tries to lock the same workspace | Server refuses with the TFE lock-held error naming the holder identity and lock ID and the wrapper as the claim path, and changes nothing. Wait with lock-timeout or retry after release. | `tofu apply -lock-timeout=10m` |
| Plain run tries to lock while a runner claim is active | Server refuses with the claim-conflict error naming the active claim and changes nothing. | `tofu plan -lock-timeout=10m` |

Evidence for the claims above:

- Exit test requires a plain tofu apply to take the whole-unit lock with a waiting runner job and holder in history. Proof: Roadmap step: A person runs plain `tofu apply` on the same workspace. Pass: it takes the whole-unit lock, and a runner job that starts meanwhile waits for it. The history shows the lock holder.
- Repository has claim gate e2e coverage. Proof: e2e/claim_gate_test.go
- Repository has runner protocol code. Proof: internal/api/runnerapi
- Repository has lock holder e2e coverage. Proof: e2e/fence_test.go
- Repository documents manual locks. Proof: docs/guides/manual-locks.md

## What Changes

- Plain TFE-compatible lock with no associated claim takes the whole-unit lock with reason local-without-wrapper.
- Runner job that starts during a plain holder waits in the FIFO whole-unit lock queue and starts after release, expiry, or force-unlock.
- Workspace row, wait record, history, and audit name the plain lock holder with interval, end cause, and reason.

Out of scope:

- Disjoint claim registration for wrapped local runs
- Merge of concurrent uploads one resource instance at a time
- Port of runner-only features to the local wrapper
- Pulumi CLI claim model

Depends on:

- two-disjoint-claims
- runner-job-overlapping-claim-refused
- strict-case-takes-turns
- `two-disjoint-claims` (order of the milestone review)
- `strict-case-takes-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Developer may be surprised that a plain laptop apply makes a team runner job wait; the job status must name the holder and the wait.
- Developer must learn the wrapper path from the lock message when repeated plain runs block others.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Server reports workspace.locked for a held lock. (`internal/errcode/catalog_workspace.go:15`)
- Server reports workspace.lock-mismatch for a holder mismatch. (`internal/errcode/catalog_workspace.go:12`)
- Server reports workspace.not-locked when no lock is held. (`internal/errcode/catalog_workspace.go:20`)
- Server reports workspace.changed-concurrently for a concurrent change. (`internal/errcode/catalog_workspace.go:22`)
- Runner claim test covers an operation started with its claim. (`pkg/durable/runner_claimed_test.go:16`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 3 (changed), 6 (extended).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "A new state version needs the lock of the caller."
- Quote: "The server mints a lock ID for each acquisition."
- Effect: Plain run keeps the caller lock check, the server-minted lock ID, and the holder check on unlock and upload.
- Conflict: none.

**ADR 44: A force-unlock fences the old holder, and overrides are opt-in** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 239 extends decisions 4 and 6; ADR 335 extends decision 5. Chain to the end: ADR 239, ADR 335.
- Current rule: this ADR, with ADR 239, 335.
- Quote: "Every later upload of the stale job carries the old lineage, so the server refuses it with the lineage error of ADR 26 (422)."
- Effect: A stale or fenced plain holder fails a later upload with the lineage error.
- Conflict: none.

**ADR 239: A force-unlock needs `unit:write-forced` on every OpenTofu route** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A client-encrypted state also needs the switch, as on `main`."
- Effect: The forced-write switch applies only to a lock release on a client-encrypted state; every force-unlock needs holder check and audit entry.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Work that the server cannot prove disjoint takes turns."
- Quote: "The `dustpan` CLI wraps the real `tofu` or `pulumi` CLI on the machine of the user, in the same way as the runner wrapper does."
- Effect: A plain local run without the wrapper keeps the whole-unit lock and takes turns with runner jobs; the wrapper path that takes part in claims comes from the claim model and the later wrapper protocol.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a plain local run take part in claims or take the whole-unit lock? ADR 302 decision 2 lists a local CLI run without the wrapper as one of the six cases that keep the whole-unit lock, so a plain run takes the whole-unit lock and never registers a claim.
- How does the server tell a plain run from a wrapper run? No handshake marker exists. ADR 302 decision 6 leaves the wrapper protocol to the later RFC. A plain run is a TFE-compatible lock with no associated claim record from the claim model; a run with an associated claim follows the claim path.
- When is the whole-unit lock acquired for a plain run? ADR 26 decisions 3 and 6 require the caller lock and a server-minted lock ID with holder check, so the plain lock is acquired at the OpenTofu lock call before plan or apply and released at unlock.
- What happens when a plain lock expires while the apply still runs? The plain lock reuses the existing workspace lock lifetime and expiry with no new TTL and no new renewal rule; an expired or fenced holder fails later uploads per ADR 26 decision 3 and ADR 44 decision 2.
- What releases the lock when a plain run crashes or never uploads? ADR 26 decision 6 needs unit:manage for force-unlock; ADR 239 decision 4 needs the forced-write switch only for a lock on a client-encrypted state. A crashed plain run releases only by existing expiry or by that force-unlock with fence version and audit entry.
- Who retries when a plain run and a runner job overlap? The plain holder and runner jobs take turns in the FIFO whole-unit lock queue built by strict-case-takes-turns; blocked stays only for a refused overlapping claim between claims.
- What holder facts does history show and where are they stored? Live holder and reason sit on the workspace lock record while held; holder principal, lock interval, end cause, and reason are written to history and audit when the lock ends for any cause; the queued runner job wait record names the holder.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
