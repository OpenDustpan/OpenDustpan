---
version: 1
---

Issue: #2576.

## Problem

Every feature edits the hot composition root file, so merges conflict and review slows. Most wiring still sits in pkg/dustpan/dustpan.go.

## Evidence

- The composition root file exists on main. Proof: Pack path pkg/dustpan/dustpan.go exists true.
- The area recipe exists on main. Proof: Pack path docs/agents/dustpan-areas.md exists true.
- The authorizer chain test exists on main. Proof: Pack path pkg/dustpan/authchain_internal_test.go exists true.
- NewServer and newAdapters live in dustpan.go on main. Proof: Pack main lists pkg/dustpan/dustpan.go:774 (func NewServer) and pkg/dustpan/dustpan.go:1139 (func newAdapters).

## Fix boundary

- Move the wiring of each area from pkg/dustpan/dustpan.go into the NEW files pkg/dustpan/area_jobs.go, pkg/dustpan/area_backup.go, pkg/dustpan/area_signin.go, pkg/dustpan/area_notifications.go and pkg/dustpan/area_triggers.go with explicit arguments.
- Keep one explicit ordered call list in NewServer with unchanged order. No init and no area type. mountAPI stays as it is.
- One area per pull request, each with an architecture review. Move 2 waits for the review of move 1.
- After each move, TestNewServerAuthenticatorChain passes unchanged.

Out of scope:

- Split of storetest suites by area
- Move of e2e helpers
- Behavior change, new option or route change
- Sort of queries.sql

## Check

- Move jobs and runners wiring into the NEW file `pkg/dustpan/area_jobs.go` with explicit arguments and unchanged order Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- After the architecture review of move 1, move backups and restore wiring into the NEW file `pkg/dustpan/area_backup.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- Move sign-in and local-users wiring into the NEW file `pkg/dustpan/area_signin.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- Move notifications wiring into the NEW file `pkg/dustpan/area_notifications.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- Move triggers wiring into the NEW file `pkg/dustpan/area_triggers.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- Confirm the server still serves the same routes and the exported SDK API is unchanged, with the full test run and `make check`. Confirmed by: CI (make check). Check: `make check`

Depends on:

- `architecture-review-7-nov-composition-e2e-helpers-move` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main builds the server through NewServer. (`pkg/dustpan/dustpan.go:774 (func NewServer)`)
- Main builds HTTP adapters from services and settings through newAdapters. (`pkg/dustpan/dustpan.go:1139 (func newAdapters)`)
- Main enforces one authorizer chain with an internal test. (`pkg/dustpan/authchain_internal_test.go:129 (TestNewServerAuthenticatorChain)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 326: Split the hot files by area, in small steps** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current), 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The assembly stays plain sequential calls with explicit arguments in one function."
- Quote: "There is no `area` type and no `areas()` list."
- Quote: "Each pull request moves one area."
- Quote: "The first move gets a review before the next."
- Quote: "**Each split pull request gets an architecture review before it merges (owner).**"
- Effect: The change follows the smaller form: area wiring moves to area files with explicit arguments, NewServer keeps one ordered call list, no area type, one area per pull request with reviews.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_model_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh.

Questions considered and answered:

- Should area wiring use explicit arguments or a shared settings object? Explicit arguments. ADR 326 decision 5: plain sequential calls with explicit arguments, no area type.
- Should NewServer use init registration or an ordered list? An ordered list. ADR 326 decision 5 keeps the assembly as plain sequential calls in one function.
- Does this change alter routes, auth or options? No. The issue states no behavior change and no new option.
- What gates each area move? ADR 326 decision 5 (review of the first move) and decision 7 (architecture review of each split pull request).
- Does mountAPI change? No. ADR 326 decision 5 keeps mountAPI as it is.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
