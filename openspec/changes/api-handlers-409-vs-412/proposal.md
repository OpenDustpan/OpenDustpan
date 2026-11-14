---
version: 1
---

Issue: #1286.

## Problem

A program drives approval and lock routes through the tool-neutral API and retries timed-out writes. A stale write must read as 412 and a retry must replay one stored answer, or the program cannot tell a stale write from a conflict and it applies a write twice.

## Evidence

- The store returns gate.ErrConflict for version conflicts on gate objects Proof: pkg/store/internal/gatestore/objects.go:286: return fmt.Errorf("%w: %s is at version %d, not %d", gate.ErrConflict, what, stored, prev)
- The issue reports approval 409 for If-Match mismatch and ignored Idempotency-Key as existing behavior Proof: Issue 1286: both are existing behavior, and the handlers must match ADR 210.

## Fix boundary

- Approval-rule and approval-default writes with a stale If-Match answer 412 instead of 409
- A create conflict with no If-Match keeps 409 with missing object as 404 before the precondition check
- Named inventory collection and lock, approval, preferences and context mutating routes store and replay Idempotency-Key answers

Out of scope:

- Change of ADR 210 words
- Console or generated-client changes
- Replay scope beyond the named routes

## Check

- Write failing test TestApprovalIfMatchMismatchAnswers412 in NEW file `internal/api/server/approval_ifmatch_test.go` for PUT and DELETE of route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/approval-rules/{} with an access token Check: `go test ./internal/api/server -run TestApprovalIfMatchMismatchAnswers412` fails on main with 409
- Write failing test TestNamedRouteIdempotencyReplay in `internal/api/idempotent/keys_test.go:176` for collection, lock, approval, preferences and context mutating routes with an access token Check: `go test ./internal/api/idempotent -run TestNamedRouteIdempotencyReplay` fails on main with the header ignored
- Map stale approval version with If-Match present to 412 in the approval handlers in `internal/api/route` for route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/approval-rules/{} with an access token Check: TestApprovalIfMatchMismatchAnswers412 passes on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/approval-rules/{} with an access token
- Wire Idempotency-Key store and replay in `pkg/store/internal/codec/idempotency.go:89 (func EncodeIdempotencyHeaders)` on the named routes after authentication and authorization Check: TestNamedRouteIdempotencyReplay passes on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/collections/{}/lock with an access token
- Update the API description in internal/api/apidesc with the 412 response and idempotency header Check: `go test ./pkg/dustpan -run TestDescriptionSchemasAreConsistent` passes
- E2E step against the real server for the 412 path and one idempotency retry Check: `make e2e` passes for PUT /api/dustpan/v1/namespaces/{namespace}/approval-rules/{id} with an access token and stale If-Match expecting 412
- Update the docs page for preconditions and Idempotency-Key scope Check: `make docs-lint` passes and each curl runs as written
- Run the full checks Check: `make check` reports zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The store defines gate.ErrConflict for gate version conflicts (`pkg/gate/gate.go:87 (var gate.ErrConflict)`)
- A shared header test helper checks ignored If-Match (`internal/api/apidesc/headertest/headertest.go:169 (func checkIfMatchIgnored)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).**"
- Quote: "One JSON style guide. Each tool-neutral route follows these rules."
- Effect: Decision 6 requires Idempotency-Key on every mutating route, so this change adds replay support to the named routes instead of rejecting the header. Decision 10 gives one JSON style; 412 for a stale If-Match follows HTTP precondition semantics and keeps the description exact. No decision words change.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Should the named routes gain Idempotency-Key replay or answer 400? Add support. ADR 210 decision 6 is owner-tagged and says every mutating route accepts the header, so rejection with 400 would conflict with an owner decision.
- Does ADR 210 govern these routes? Yes for the routes named in the issue. The fix changes handler mapping and idempotency wiring only, under ADR 210 decisions 6 and 10.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
