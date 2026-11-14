---
version: 1
---

Issue: #917, #2161.

## Problem

The problem: the TFE-compatible workspace PATCH does not write the execution mode or the runner pool. A team that manages workspaces through the OpenTofu TFE provider cannot set where plans run.

## Evidence

- TFE workspace routes exist on main. Proof: ANY /api/v2/workspaces/{} (route table of the pack)
- The workspace domain package exists on main. Proof: pkg/workspace
- The runner domain package exists on main. Proof: pkg/runner
- The issue says the mapping of mode values needs a short ADR. Proof: Issue 917, section Design status

## Fix boundary

- PATCH of execution-mode and the agent-pool relation on the TFE workspace
- Mapping to the pool of the unit with the tool-neutral authorization
- JSON:API errors and a docs page

Out of scope:

- Remote apply and saved plans
- TFE agent-pool management routes
- Runner labels beyond the pool
- Speculative plans through the TFE run routes (the slice 1 change)

## Check

- Write failing tests for the PATCH of mode and pool, the refusals and the JSON:API errors, through PATCH ANY /api/v2/workspaces/{} with an access token, in `NEW internal/api/tfe/workspace_execution_test.go`. Check: TestTFEWorkspacePatchExecutionMode fails on main
- Write a failing test in `NEW internal/api/tfe/workspace_execution_test.go` that a read through the tool-neutral API shows the same pool, with an access token. Check: TestTFEWorkspacePatchPoolMatchesToolNeutral fails on main
- Wire the PATCH through the authorizer and pkg/workspace to the unit pool fields. Check: The tests above pass
- Add the docs page in `NEW docs/guides/tfe-execution-mode.md`. Check: `make docs-lint` passes
- Run the e2e step with the real OpenTofu CLI on PATCH ANY /api/v2/workspaces/{} with an access token. Check: `make e2e` passes
- Check the wiring, the route authorization action, the usability check from the docs page alone and the PATCH latency. Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- E2E drives the real OpenTofu CLI. (`e2e/tool.go:110 (func OpenTofu)`)
- Runner pool rows exist in the store codec. (`pkg/store/internal/codec/runner.go:17 (type RunnerPoolRow)`)
- The workspace service implements the use cases behind the cloud block. (`pkg/workspace/service.go:345 (type Service)`)
- A pool reader port exists for the executor. (`pkg/runner/executor.go:13 (type PoolReader)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 254: Remote execution for OpenTofu starts with speculative plans on runners** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 300 changes decision 6; ADR 310 extends decision 4; ADR 310 extends decision 5; ADR 350 changes decision 4. Chain to the end: ADR 300, ADR 310, ADR 350.
- Current rule: this ADR, with ADR 300, 310, 350.
- Quote: "Opt in for each workspace. The entitlement `operations` is `true` for a namespace only when the namespace has a runner pool."
- Quote: "Each workspace keeps `execution-mode` `local` until a person with the action to manage the workspace sets it to `agent` through the API (ADR 17)."
- Effect: The PATCH is the opt-in path. Local stays the default and agent needs the manage action.
- Conflict: none.

**ADR 350: OpenDustpan runners replace HCP Terraform agents, and a gap in security is a documented gap** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The agent pool routes are not decided (owner, 2026-11-07). The pool management routes (level 1a) stay undecided. A later RFC covers them."
- Quote: "Until the RFC is accepted, ADR 254 decision 4 holds: the server does not serve the TFE agent pool API."
- Effect: The change serves no pool management route. It only stores the stable ID of a runner pool.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The docs page ships in the same pull request.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Run the e2e step with the real OpenTofu CLI on PATCH ANY /ap" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Check the wiring, the route authorization action, the usabil" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: and 2 more.

Questions considered and answered:

- Who may change the execution mode? ADR 254 decision 2: a person with the action to manage the workspace, through the API.
- Does this change serve the TFE agent-pool API? No. ADR 350 decision 4 (owner) keeps pool management routes unserved; only the workspace relation is stored.
- What are the mode values and the pool relation shape? Local and agent are accepted and remote is refused. The relation ID is the stable ID of the runner pool, null clears it, agent needs a pool of the same namespace, and a dead pool ID refuses the run.
- How are concurrent PATCH writes handled? One store transaction with last-writer-wins on the row version. No partial write.
- What audit record does a change leave? A change writes one audit entry. A denial writes no change entry. Reads show effective local by default.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
