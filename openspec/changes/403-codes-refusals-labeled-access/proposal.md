---
version: 1
---

Issue: #1373.

## Problem

Refusals labeled access.denied tell the caller to seek a grant, even where a grant does not help.

## Evidence

- The item touches pkg/dustpan/jobservice.go, pkg/dustpan/stateguard.go, pkg/gate/service.go, and pkg/job/service_start.go. Proof: paths list gives pkg/dustpan/jobservice.go, pkg/dustpan/stateguard.go, pkg/gate/service.go, pkg/job/service_start.go as exists true
- Main documents that a person who gets access.denied needs a grant. Proof: rg access.denied: internal/errcode/errcode.go:50-55: access.denied. Use it for a refusal that is not a missing permission, such as a refused sign-in: a person who gets access.denied needs a grant
- Main pins forbidden ordering in the errcode test and pins the access.denied entry in the catalog test. Proof: main lists TestForbiddenOrder at internal/errcode/errcode_test.go:26 and the access.denied code at internal/errcode/catalog_test.go:98
- No ADR is listed for this item in the pack. Proof: pack field adrs is [] and no ADR text is given, so there is no quoted decision line to check

## Fix boundary

- Map only true missing-permission denials to access.denied; give job token, unusable connection, and state guard refusals the NEW codes job.token-refused, connection.unusable, and state.subject-refused with status 403
- Make gate CheckAccess fail closed on store errors with the NEW code gate.store-unavailable and status 503 plus an audit denial entry that records the store error
- Remove the unreachable csrf-before-other-site order pin in TestForbiddenOrder and fix the forbidden-error-codes docs page wrap at lines 53-54

Out of scope:

- New authorization rules or new grants
- Other ErrDenied wraps outside the four call sites named in the issue

## Check

- Add failing test for job token refusal mapping at pkg/job/service_start.go Check: NEW TestJobTokenRefusalCode in pkg/job/service_start_test.go fails on main with access.denied
- Add failing tests for connection and state guard refusal mappings at pkg/dustpan/jobservice.go and pkg/dustpan/stateguard.go Check: NEW TestConnectionRefusalCode in pkg/dustpan/jobservice_test.go and NEW TestStateGuardRefusalCode in pkg/dustpan/stateguard_test.go fail on main with access.denied
- Add failing tests for gate store-error code, mapper order, and forbidden order at internal/errcode/errcode_test.go:26 Check: NEW TestGateStoreErrorCode and NEW TestForbiddenOrderWithoutCsrfPin in internal/errcode/errcode_test.go fail on main
- Replace the ErrDenied wraps at the four call sites with distinct NEW sentinels that wrap ErrDenied Check: go test ./pkg/job/... ./pkg/dustpan/... ./pkg/gate/... passes
- Add the NEW codes job.token-refused, connection.unusable, state.subject-refused, gate.store-unavailable to the adapter-side errcode mapping after errcode-catalog-imports-domain-packages and fix TestForbiddenOrder and the forbidden-error-codes page wrap Check: go test ./internal/errcode/... passes with the NEW catalog entries and catalog test
- Call the existing route ANY /api/server/caller/access with an access token in an e2e step against the real CLI and check the code member keeps HTTP 403 for tool refusals and 503 for the gate store fault Check: API description error list names the NEW codes; `make e2e` passes with tool refusals on 403 and gate store fault on 503. Confirmed by: CI (make e2e).
- Wire the NEW codes, update the forbidden-error-codes docs page with entries for the NEW codes, and run the usability check Check: `make check` passes with zero findings. Confirmed by: CI (make check).

Depends on:

- errcode-catalog-imports-domain-packages
- `errcode-catalog-imports-domain-packages` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Gate service evaluates access limits through CheckAccess. (`pkg/gate/service.go:297 (func Service.CheckAccess)`)
- The catalog test pins the access.denied code entry. (`internal/errcode/catalog_test.go:98 (error code)`)
- Main pins forbidden ordering in the errcode test. (`internal/errcode/errcode_test.go:26 (test TestForbiddenOrder)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- When shall the server use access.denied? Pack lists no ADR. The needs-a-grant rule comes from the errcode comment on main: a person who gets access.denied needs a grant. Only a true missing permission keeps that code.
- Shall gate CheckAccess fail open on store errors to avoid a wrong label? Keep denying. The issue states the gate wrap fails closed. The fix keeps the denial and answers 503 with the NEW gate.store-unavailable code plus an audit denial entry.
- Do the NEW codes change the Pulumi or OpenTofu wire contract? No for the three relabeled refusals. Tool endpoints keep HTTP 403 with the NEW code only in the code member. The gate store case moves to 503 like other unavailable answers.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
