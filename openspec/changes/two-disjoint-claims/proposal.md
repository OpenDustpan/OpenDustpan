Roadmap: `docs/roadmap.md:634` (Two disjoint claims).

## Why

**Persona: developer.** Developer on two teams changes different resources of one workspace at the same time. Each team runs a runner job with a saved plan. The developer needs both claims accepted before anything runs, so parallel work starts from a clear record.

The user path this change must make work:

1. **API.** Runner job A plans the workspace and registers its non-replace claim with its address set through the new claims route.
2. **API.** Runner job B plans and registers its disjoint non-replace claim through the same route.
3. **API.** Developer lists claims of the workspace and sees two records in state accepted with start time and principal.
4. **E2e.** The e2e suite runs two runner jobs with real OpenTofu against a fake cloud provider and checks two accepted claim records.
5. **Docs page.** A guide describes the claim record fields and the list call for two disjoint claims.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Claim base is touched by a later version on an address of the claim. | Server answers 422 with the touching version and names the current state version. Plan again from the current state version, then register a new claim. | `tofu plan` |
| Claim overlaps an accepted claim. | Server answers 409 with the blocking claim ID and stores no claim. Wait for the blocking claim to end, plan again, then register a new claim. | `GET /api/dustpan/v1/workspaces/{id}/claims` |
| Claim holds a replace action. | Server answers 422 and changes nothing. Use the workspace lock path for this change. | `tofu plan` |
| Principal lacks unit:claim-register on the workspace. | Server answers 403 and names the missing action on the workspace. Ask a namespace administrator for the grant, then register again. | `GET /api/server/principals/{}/grants` |
| Claim holds an empty resource set. | Server answers 422 and changes nothing. Plan with at least one non-replace resource change, then register again. | `tofu plan` |
| Claim names a missing, deleted or locked workspace or an unknown base. | Server answers 404 for a missing workspace, 410 for a deleted workspace and 422 for a locked workspace or an unknown base version. Use the current workspace and base, then register again. | `GET /api/dustpan/v1/workspaces/{id}/claims` |

Evidence for the claims above:

- Runner jobs run through the runner protocol and the runner management API. Proof: Pack code lists internal/api/runnerapi and pkg/runner.
- Claim gate behavior has e2e coverage today. Proof: Pack e2e list names e2e/claim_gate_test.go.
- Job plan and jobs API paths have e2e coverage today. Proof: Pack e2e list names e2e/jobs_plan_test.go and e2e/jobs_api_test.go.

## What Changes

- New route POST /api/dustpan/v1/workspaces/{id}/claims: a runner job or local wrapper registers a non-replace claim with base state version, address set and principal.
- New route GET /api/dustpan/v1/workspaces/{id}/claims: lists each claim with state, base, address set, principal, job ID, start time and end time.
- Server accepts a second non-replace claim when its address set is disjoint from each accepted claim.
- Server refuses an overlapping non-replace claim with 409 and a replace claim with 422.
- Each accepted claim writes one audit entry, and each claim marks ended with an end time at job end or lease expiry.

Out of scope:

- Concurrent applies and per-resource merge of uploads.
- Blocked job status mapping of a refused claim.
- Strict cases that keep the whole-unit lock.
- Local CLI wrapper retry of refused claims.
- Pulumi CLI claim model and client-encrypted state.
- Serial assignment under a claim, parent pointer and claim fence.

Depends on:

- C2 RFC accepted as an ADR by the owner

## Usability

Where the path still costs the person time or surprises them:

- Developer reads two claim IDs with state accepted, start time and principal to confirm both jobs proceed.
- A replace claim gets a plain refusal that names the workspace lock path.
- No local run retry path exists in this step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Disjoint behavior has a conformance test in the workspace package. (`pkg/workspace/reveal_conformance_test.go:1463`)
- A workspace locked error code exists for whole-unit lock cases. (`internal/errcode/catalog_workspace.go:15`)
- Runner claimed-operation behavior has a test in the durable package. (`pkg/durable/runner_claimed_test.go:16`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 3 (changed), 6 (extended).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "A new state version needs the lock of the caller."
- Quote: "The server mints a lock ID for each acquisition."
- Effect: Claim registration grants no lock ID and publishes no state version. The lock, serial and lineage checks stay unchanged.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A runner job registers its saved plan as a claim before the apply."
- Quote: "OpenTofu is first."
- Quote: "The code of milestone 8 comes after the project owner accepts that RFC as an ADR."
- Effect: This change is a draft spec only. It blocks implementation until the owner accepts the C2 RFC as an ADR. Then it records two disjoint runner-job claims and refuses overlapping ones, with no merge and no serial assignment.
- Conflict: owner_blocks.

## Open questions

1. What is the claim ID format, the per-job principal on the TFE-compatible path and the claim expiry rule, which the C2 RFC decides? Decides: owner.
   - Options: Follow the accepted RFC when it lands; Fix them in this change now.
   - Recommended: Follow the accepted RFC when it lands. Reason: ADR 302 decision 6 gives these points to the RFC.

Not spec-ready: the change conflicts with an owner-tagged decision of ADR 302.

Questions considered and answered:

- Where does claim registration happen in the runner path? ADR 302 decision 2: a runner job registers its saved plan as a claim before the apply.
- Does this change assign serials or merge state under claims? No. ADR 302 decision 6 leaves them to the RFC.
- Which tool does this step cover? ADR 302 decision 3: OpenTofu first.
- Can code start before the RFC is accepted? No. ADR 302 decision 6 orders the accepted RFC before the code. The change waits and implementation does not start.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
