---
version: 1
---

## Problem

Duplicate generated error shapes hide each other in the API description, so the build must refuse the duplicate name with a clear error.

## Evidence

- Main has checkedErrorVariants in the API description package. Proof: internal/api/apidesc/errorvariants.go:79 (func checkedErrorVariants)
- Main has WithErrorVariants in the same file. Proof: internal/api/apidesc/errorvariants.go:52 (func WithErrorVariants)
- Main has errorVariantSchemas in the same file. Proof: internal/api/apidesc/errorvariants.go:162 (func errorVariantSchemas)
- Main tests that error variants match the catalog. Proof: pkg/dustpan/apidesc_test.go:1038 (TestAPIDescriptionErrorVariantsMatchTheCatalog)

## Fix boundary

- checkedErrorVariants refuses a duplicate generated error-variant name.

Out of scope:

- Route guard address check and authorize path
- Audit old values, settings helpers, purge verification

## Check

- Add failing unit test for duplicate generated error-variant name in the NEW file `internal/api/apidesc/errorvariants_duplicate_test.go`. Check: The NEW test file fails on main and passes after the fix.
- Refuse duplicate generated name in checkedErrorVariants in `internal/api/apidesc/errorvariants.go:79`. Check: `go test ./internal/api/apidesc -count=1` passes.
- Confirm wiring in `internal/api/apidesc/errorvariants.go:52`: duplicate error from checkedErrorVariants fails API description build at server start. Check: `go test ./pkg/dustpan -run TestAPIDescriptionErrorVariantsMatchTheCatalog -count=1` passes.
- Run usability check from the server-start error alone and run `make check` in `internal/api/apidesc/errorvariants.go:79`. Confirmed by: CI (`make check`). Check: `make check` passes with zero findings.

Depends on:

- `small-hardening-26-oct-architecture-audit-chain` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main builds error-variant schemas through WithErrorVariants and errorVariantSchemas. (`internal/api/apidesc/errorvariants.go:52 (func WithErrorVariants)`)
- Main checks error variants through checkedErrorVariants. (`internal/api/apidesc/errorvariants.go:79 (func checkedErrorVariants)`)
- Main tests that error variants match the catalog. (`pkg/dustpan/apidesc_test.go:1038 (TestAPIDescriptionErrorVariantsMatchTheCatalog)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR govern generated error-variant names? No decision applies. The pack adrs list is empty, so there is no ADR text to quote and no owner tag to follow.
- What counts as a duplicate generated name? Exact match on the generated string, case-sensitive, checked on the generated names before schema build. A collision caused by normalization still counts as a duplicate because the generated strings are equal.
- How does the refusal reach server startup? checkedErrorVariants returns the error to its caller WithErrorVariants, which fails API description build and therefore server start. Covered by a scenario and a wiring task.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
