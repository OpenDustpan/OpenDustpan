---
version: 1
---

Issue: #3003.

## Problem

Embedder wires the built-in stores in two places and can drift from the server defaults. Operator starts cmd/opendustpan with the same flags and defaults before and after this change.

## Evidence

- cmd/opendustpan builds the server with NewServer. Proof: cmd/opendustpan/main.go:950:	srv, err := dustpan.NewServer(opts...)
- Main holds the SQLite constructor. Proof: pkg/dustpan/builtin_stores.go:161 (func WithSQLite)
- Main holds the PostgreSQL constructor. Proof: pkg/dustpan/builtin_stores.go:174 (func WithPostgreSQL)
- Main holds the helper that sets the store options. Proof: pkg/dustpan/builtin_stores.go:325 (func settings.setStoreOptions)
- No ADR is named in the pack. Proof: adrs: []

## Fix boundary

- cmd/opendustpan opens its built-in store through dustpan.WithSQLite or dustpan.WithPostgreSQL.
- The existing With...Store options stay.

Out of scope:

- New store kinds beyond SQLite and PostgreSQL.
- Removal of the existing With...Store options.
- Ordering, error, and close behavior, which main already holds.

## Check

- Add a failing test that cmd/opendustpan opens its SQLite store through dustpan.WithSQLite. Check: TestCmdUsesBuiltinSQLiteConstructor in NEW file cmd/opendustpan/builtin_wiring_test.go. No route: the test builds the server options only.
- Add a failing test that cmd/opendustpan opens its PostgreSQL store through dustpan.WithPostgreSQL. Check: TestCmdUsesBuiltinPostgreSQLConstructor in NEW file cmd/opendustpan/builtin_wiring_test.go. No route: the test builds the server options only.
- Switch cmd/opendustpan to dustpan.WithSQLite and dustpan.WithPostgreSQL. Check: The two new tests pass. No route: the tests build the server options only.
- Update the SDK docs page with both constructors. Check: `make docs-lint` passes. No route: the check reads files only.
- Run a usability check of the constructor names from the docs page alone. Check: Persona follows the docs page and starts the server with each store kind. No route: the check walks docs and server start only. A person's step.
- Run the full check. Check: `make check` with zero findings. No route: the check runs the full suite only. Confirmed by: the driver's gates.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- NewServer builds a server from options. (`pkg/dustpan/dustpan.go:784 (func NewServer)`)
- WithSQLite exists in pkg/dustpan. (`pkg/dustpan/builtin_stores.go:161 (func WithSQLite)`)
- WithPostgreSQL exists in pkg/dustpan. (`pkg/dustpan/builtin_stores.go:174 (func WithPostgreSQL)`)
- One helper applies the store options. (`pkg/dustpan/builtin_stores.go:325 (func settings.setStoreOptions)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_model_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh.

Questions considered and answered:

- Does a single wiring point conflict with an ADR? No ADR is named in the pack (adrs: []). The existing With...Store options stay, so no decision changes.
- Does main already hold the constructors? Yes. pkg/dustpan/builtin_stores.go:161 (func WithSQLite), :174 (func WithPostgreSQL), and :325 (func settings.setStoreOptions) exist on main. Only the cmd/opendustpan switch is missing.
- Are ordering, error, and close behaviors new work? They exist on main in pkg/dustpan and stay unchanged. They are out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
