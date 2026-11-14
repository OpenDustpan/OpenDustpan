Roadmap: `docs/roadmap.md:717` (Gate apply starts on change-order dependencies after RFC 115.).

## Why

**Persona: developer.** A Developer runs terragrunt run-all apply over many workspaces from a runner and needs dependent workspaces to apply in Terragrunt order. Without a gate, apply jobs start at once and a later workspace applies before its dependency, which breaks infrastructure.

The user path this change must make work:

1. **Docs page.** Read the NEW file `docs/guides/terragrunt-run-all.md` for the runner fan-out flow.
2. **API.** Start terragrunt run-all apply on a runner; the shim uses only the NEW change-order API to make one plan job and one apply job per workspace.
3. **Console.** Watch apply jobs start in Terragrunt order and blocked jobs name their failed dependency.
4. **E2e.** Run the e2e flow with real Terragrunt and real OpenTofu over 3 workspaces and check the provider call record through `ANY /runner/v1` with a runner token.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply of one workspace cannot start because a workspace it depends on failed on the NEW change-order apply-start route. | Server marks the dependent apply blocked with the NEW error code `change-order.dependency-blocked` and starts no apply. Check state with existing route `GET /api/dustpan/v1/namespaces/{}/units` with an access token. | `curl GET /api/dustpan/v1/namespaces/{}/units` |
| Apply of one workspace cannot start because a dependency did not finish on the NEW change-order apply-start route. | Server keeps the apply queued behind its dependencies and starts no apply. Check state with existing route `GET /api/dustpan/v1/namespaces/{}/units` with an access token. | `curl GET /api/dustpan/v1/namespaces/{}/units` |
| Discovery reports a dependency cycle in the Terragrunt units on the NEW change-order edge-write route. | Server stores no edges and returns the NEW error code `change-order.cycle-rejected`. Discovery must fix the Terragrunt order with `terragrunt graph-dependencies`. | `terragrunt graph-dependencies` |

Evidence for the claims above:

- RFC 115 is a draft stub and leaves dependency order open. Proof: RFC excerpt in the pack: Status: draft. A stub. and open question Dependency order. Does the shim give ordered apply jobs, or does it use a change order that does not order its apply jobs (ADR 190)?
- The roadmap scope for this part gates apply starts, keeps isolated applies with retry, and leaves discovery and fan-out to earlier parts. Proof: Roadmap in the pack: This part gates each apply start on the change-order API route in depends_on. It enforces Terragrunt unit order and keeps isolated applies with idempotent retry. It leaves discovery and basic fan-out to the earlier parts.

## What Changes

- Gate each run-all apply start on stored Terragrunt dependency edges through the NEW change-order apply-start route.
- Enforce Terragrunt unit order: a dependent apply starts only after each dependency apply succeeded.
- Block dependent apply jobs with no apply call when a dependency failed, with the NEW error code `change-order.dependency-blocked`, and name the blocking job.
- Reject a cyclic edge write with the NEW error code `change-order.cycle-rejected` and store no edges.
- Keep isolated applies with idempotent retry per workspace on the same Idempotency-Key.
- Add the NEW file `docs/guides/terragrunt-run-all.md` with the runner fan-out flow.

Out of scope:

- Discovery of Terragrunt units and mapping to workspaces
- Basic fan-out of one plan job and one apply job per unit
- Local run-all and run-all in the CI of a team
- The record that names the cause and the review hold
- A failure in one unit beyond blocked apply starts

Depends on:

- run-all-on-runner-discovery
- run-all-on-runner-fanout-plan-apply-jobs
- `run-all-on-runner-fanout-plan-apply-jobs` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The Developer waits for dependency chains with no progress text when many workspaces queue behind one slow apply.
- The blocked state surprises the Developer when one failed workspace stops the rest of the chain without a clear next step in the console.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs one claimed durable operation on one runner claim, which this gate keeps for isolated per-workspace applies. (`pkg/durable/runner_claimed_test.go:16 (func TestRunnerRunsAnOperationStartedWithItsClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

1. Does each apply job wait for each Terragrunt dependency apply job to succeed before it starts? Decides: PM.
   - Options: Strict Terragrunt order: each apply job waits for each dependency apply job to succeed; Start each apply job when its own plan succeeds, with no order.
   - Recommended: Strict Terragrunt order: each apply job waits for each dependency apply job to succeed. Reason: RFC 115 in the pack is a draft stub and its open question on dependency order has no outcome yet. The pack proves no decision.

Not spec-ready: a code check fails: design decision "Dependency gate": a limit with no number; give the number, the unit and the scope.

Questions considered and answered:

- Does this change add discovery of Terragrunt units? No. The roadmap scope in the pack says this part gates each apply start, enforces Terragrunt unit order, and keeps isolated applies with idempotent retry, and leaves discovery and basic fan-out to the earlier parts in depends_on.
- Does any accepted ADR in the pack decide dependency order? No ADR decision text is in the pack. The pack lists no ADRs and only an RFC 115 draft stub excerpt, so no ADR quote can be given and no conflict can be checked beyond the RFC note.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
