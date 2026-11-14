---
version: 1
---

Issue: #2708.

## Problem

As a Developer I run domain tests and read workspace and stack history, but gaps in API description coverage and key material in error text cost time and risk disclosure.

## Evidence

- API description regression tests exist on main Proof: pkg/dustpan/apidesc_internal_test.go, pkg/dustpan/apidesc_test.go, pkg/dustpan/apidesc_bodies_test.go
- Workspace streamed-write and state scope tests exist on main Proof: pkg/workspace/nil_recorder_test.go, pkg/workspace/write_state.go, pkg/workspace/service.go, pkg/workspace/state_scope_test.go
- Stack history and store ListUpdates exist on main Proof: pkg/stack/updates.go
- Body coverage test exists on main Proof: pkg/dustpan/apidesc_bodies_test.go

## Fix boundary

- Add regression test for undescribed routes through server assembly
- Discover concrete http.Handler fields with embedded traversal
- Check body declarations on both every-module and dev-module servers
- Assert streamed writes refuse before placeholder or attempt writes without a recorder
- Preserve ErrNotFound for history when the stack is deleted mid-read
- Remove blob keys from OpenStateBlob and unreferenced-blob cleanup errors

Out of scope:

- Fixes outside the six listed review items
- New routes, new API fields, or schema changes
- Unrelated Pulumi, TFE, runner, or console work

## Check

- Add regression test in pkg/dustpan/apidesc_test.go that mounts a test-only adapter with an extra undescribed route through server assembly Check: New test TestAssembledServerReportsUndescribedRoute in pkg/dustpan/apidesc_test.go fails on main and passes after, plus positive described route passes
- Add a concrete http.Handler field fixture in pkg/dustpan/apidesc_internal_test.go and extend the field discovery it uses with embedded traversal Check: New test for the concrete handler field in pkg/dustpan/apidesc_internal_test.go fails on main and passes after, existing interface-field checks still pass
- Run body-declaration assertions in pkg/dustpan/apidesc_bodies_test.go against both every-module and dev-module servers Check: TestEveryOperationMarksItsBodies in pkg/dustpan/apidesc_bodies_test.go passes for both server configs and fails on a dev-only fixture gap
- Instrument streamed-write test in pkg/workspace/nil_recorder_test.go for early recorder guard of WriteStreamed in pkg/workspace/write_state.go Check: Missing-recorder test in pkg/workspace/nil_recorder_test.go observes zero placeholder and zero attempt writes, and removal of only the early guard in pkg/workspace/write_state.go fails it
- Preserve ErrNotFound in pkg/stack/updates.go when the stack is deleted between existence check and ListUpdates Check: New History race test for pkg/stack/updates.go expects ErrNotFound and empty history stays empty
- Remove blob keys from OpenStateBlob and cleanup errors in pkg/workspace/service.go Check: NEW test in pkg/workspace/state_scope_test.go for four error paths asserts no key in errors with errors.Is matches over the full chain
- Run affected package tests with no new routes Check: `go test -race ./pkg/dustpan/... ./pkg/stack/... ./pkg/workspace/...` passes
- Run full checks and usability review of error text for key leakage. Confirmed by: CI (`make check`). Check: `make check` passes with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Body coverage test checks body declarations (`pkg/dustpan/apidesc_bodies_test.go:21`)
- Every-module server test checks adapter mounting (`pkg/dustpan/apidesc_test.go:250`)
- Stack updates expose ListUpdates (`pkg/stack/updates.go:87`)
- ErrNotFound sentinel exists (`pkg/access/access.go:53`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change need a new ADR or RFC No. The pack names only test and service files with no ADR or RFC. The work stays inside existing contracts and adds regression tests, plus it removes key material from error text.
- Does any item change user-visible behavior Yes for item 6. Error text that omits the supplied key keeps error matching with errors.Is. The other five items add only regression tests with no user-visible change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
