## Why

**Persona: runner operator.** Runner operator runs cloud sweeps on runners of the installation. The operator needs a sweep poll to claim only sweep work, a report upload to stay bounded and encrypted, and pool setup to refuse a namespace pool with the sweep purpose at once.

The user path this change must make work:

1. **API.** Create an operator pool with the sweep purpose, join a runner, and poll for sweep work under /runner/v1.
2. **API.** Upload the sweep report and call finish from the launcher session under /runner/v1.
3. **Docs page.** Read the sweep entries in docs/reference/runner-api.md.
4. **E2e.** Drive a real runner poll, report upload and finish against the TLS server.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller creates or updates a namespace pool with the sweep purpose. | 422 {"code": "invalid", "message": "only an installation pool can have the sweep purpose"}; no pool is stored. | Create the pool again with purpose `jobs` through the runner pool routes of the namespace. |
| The report is above the bound, or a line is above 64 KiB. | 413 {"code": "too-large", "message": "sweep report above the bound"}; the sweep ends failed and the staged report is dropped. | Upload a smaller report to the new sweep report route under /runner/v1. |
| The launcher calls finish with no stored report. | 422 {"code": "invalid", "message": "no complete sweep report"}; the sweep ends failed. | Upload the report to the new sweep report route, then call the new sweep finish route under /runner/v1. |

Evidence for the claims above:

- The runner API reference page exists and is the docs target of this change. Proof: docs/reference/runner-api.md

## What Changes

- Add the sweep claimer on durable operations of kind runner.SweepKind(pool), chosen by the stored purpose of the pool.
- Add two new routes under /runner/v1: the sweep report route `POST /runner/v1/session/sweep/report` and the sweep finish route `POST /runner/v1/session/sweep/finish`. They stream a bounded report and close the sweep.
- The existing poll route under /runner/v1 calls the sweep claimer for a pool with the sweep purpose.
- The pool service refuses a namespace pool with the sweep purpose at create and at update, and accepts it only for an installation pool.
- Add the sweep entries to docs/reference/runner-api.md.

Out of scope:

- Sweep target CRUD, sweep start and match
- Launcher run modes and helper modes
- Job claim path for unit jobs
- Console sweep views
- Converter commands of a sweep target

Depends on:

- add-job-session-paths-runner-unmanaged-sweep-store
- `add-job-session-paths-runner-unmanaged-sweep-store` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The runner operator learns the purpose refusal at pool create, and the error names the allowed purpose for a namespace pool.
- A failed report leaves no partial blob, so a retry starts clean with no manual cleanup.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (not owner-tagged).

- Decisions that apply: 4 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Each path calls one port. The HTTP adapter of the protocol is a thin layer over the ports of the job model and of this design: - The poll calls `job.Claimer.Claim` for a pool for jobs, and `runner.SweepClaimer.Claim` for a pool for sweeps."
- Quote: "Limits. A JSON body has at most 4 MiB. A log chunk has at most 1 MiB, a part of a plan at most 256 MiB, and a sweep report at most the limit of ADR 164."
- Effect: The poll calls the port runner.SweepClaimer for a sweep pool. The report upload keeps the bound of ADR 164.
- Conflict: none.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 3 (current), 5 (changed), 12 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "The durable operation. The start of a sweep starts one durable operation of the kind `unmanaged.sweep.<pool ID>`, with the start key `<sweep ID>`, in the transaction that stores the sweep."
- Quote: "A sweep pool runs only sweeps. A runner pool gets a **purpose**: `jobs` or `sweeps`. A sweep target must name a pool of the purpose `sweeps`, and a job must name a pool of the purpose `jobs`."
- Effect: The claimer claims the durable sweep operation by purpose. The purpose rule is read as changed by ADR 343.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Sweeps stay on runners. Only an installation pool can have the sweep purpose (Owner decision, 13 Nov 2026 (queue P14)).
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The sweep session routes stay under /runner/v1.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The spec and proposal refuse namespace pools with sweep purpose, but the owner decision P14 says a sweep pool is an operator (installation) pool only. The spec never requires the pool to be an installation pool, and the refusal rule in specs/runner-sweep-claimer/spec.md covers only namespace pools, so an installation check is missing in the pool create and update path. Resolution: answered: The spec now requires the installation pool for the sweep purpose and refuses a namespace pool, with an accept scenario for an installation pool.
- R1.2 (adr conflict): ADR 343 changes ADR 164 decision 5 (sweep pool no longer runs only sweeps) and extends decision 9 (converters run on a pool with sweep purpose). The change's claim-by-purpose and refusal rules ignore the converter case, so a jobs-purpose or sweep-purpose pool that runs converters is not handled. Resolution: answered: Claim is by operation kind, so converters run on the sweep pool; converter commands are out of scope and considered under ADR 343.
- R1.3 (open question): The Open questions section is not 'none'. Open question 1 (where the refusal happens) is still open, so the change is not spec-ready and cannot be implemented under the gate. Resolution: answered: The question is answered by the owner decision P14 and the open questions list is empty.
- R1.4 (missing scenario): The claim-by-purpose requirement has no scenario for a pool whose purpose changes while a sweep operation is queued or claimed. The design says the claim re-reads purpose in the transaction, but no scenario or test covers it. Resolution: answered: Added the scenario for a purpose that changes during a claim.
- R1.5 (failure mode): The report bound scenario covers over-cap lines and totals but not a report that is cut off mid-stream by a dropped connection or a runner crash, nor a second upload for the same sweep session. The partial blob cleanup on those paths is not specified. Resolution: answered: Added scenarios for a cut stream and a second upload, with staged blob cleanup.
- R1.6 (missing scenario): The spec does not cover the header scope-subset check (ADR 164 decision 13), which the design names as a stream check during upload. A report whose scopes fall outside the target has no scenario. Resolution: answered: Added the scope outside the target scenario.
- R1.7 (wrong claim): The ADR check cites ADR 131 decision 4 as current, but the quoted text names the runner.SweepClaimer port while the spec calls it sweepClaimer over a store, pools and blob ports; the naming and port mapping is not checked against the ADR. Resolution: answered: The ADR check now names the port runner.SweepClaimer; the unexported type is an implementation detail behind it.
- R1.8 (failure mode): The finish route with a stored report but a missing or expired claim, or a finish called twice, has no scenario. Idempotency of finish is not specified. Resolution: answered: Added scenarios for finish twice and finish with a lost claim, and an idempotent finish decision.
- R1.9 (scope): The tasks name the session routes and the pool refusal, but task 2.2 asks for an encrypted blob write, and no scenario or task covers the namespace data-key rotation case for an already stored report. Resolution: answered: Key rotation is considered: the report is encrypted at write time, and this change adds no rewrite.

## Open questions

none

Questions considered and answered:

- Which pools may have the sweep purpose? Owner decision, 13 Nov 2026 (queue P14): only an operator (installation) pool. The pool service refuses a namespace pool with the sweep purpose at create and update.
- How do converters fit the purpose rule? ADR 343 extends ADR 164 decision 9: converters run on a pool with the sweep purpose. Claim is by operation kind, so no new claim path.
- Where does the pool service refuse the purpose? At create and at update, so a bad pool is never stored.
- What bound applies to the report? ADR 131 decision 14 and ADR 164 decision 11: 1 GiB, 5,000,000 records, 64 KiB a line.
- What happens to the stored report when the namespace key changes? The report is encrypted at write time with the data key of the namespace. This change adds no rewrite.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
