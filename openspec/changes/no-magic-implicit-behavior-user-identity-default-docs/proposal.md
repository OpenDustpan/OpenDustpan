---
version: 1
---

## Problem

Identity checks fail closed, but no single docs page states the defaults, so administrators cannot predict or explain denials.

## Evidence

- Main has a fail-closed write check in the store conformance suite. Proof: pkg/store/storetest/maintenance.go:422 (func RunWritesFailClosed)
- Main has a fail-closed shared-limit check. Proof: pkg/store/storetest/shared_limits.go:347 (func testSharedFailClosed)

## Fix boundary

- Add a user docs page that documents the fail-closed identity defaults and their effective values with no behavior change.

Out of scope:

- No refusal text change
- No authorization rule change
- No job, trigger, or token behavior change

## Check

- Add the user docs page that lists each fail-closed identity default and its effective value in `NEW docs/guides/identity-defaults.md`. Check: `make docs-lint` passes on `NEW docs/guides/identity-defaults.md`.
- Run a usability check of `NEW docs/guides/identity-defaults.md` from the docs alone. A person's step. Check: Read `NEW docs/guides/identity-defaults.md` alone and state each default and its denial. A person's step.

Depends on:

- `no-magic-implicit-behavior-user-token-disable-record` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs fail-closed write checks in the store conformance suite. (`pkg/store/storetest/maintenance.go:422 (func RunWritesFailClosed)`)
- Main runs fail-closed shared-limit checks. (`pkg/store/storetest/shared_limits.go:347 (func testSharedFailClosed)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change alter any refusal or authorization rule? No. This part documents existing fail-closed identity defaults only. It makes no refusal change and no authorization rule change.
- Does this change alter jobs, triggers, or tokens? No. Jobs, triggers, and tokens keep current behavior. This part documents identity defaults only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
