## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing test that cmd/opendustpan opens its SQLite store through dustpan.WithSQLite. Check: TestCmdUsesBuiltinSQLiteConstructor in NEW file cmd/opendustpan/builtin_wiring_test.go. No route: the test builds the server options only.
- [ ] 1.2 Add a failing test that cmd/opendustpan opens its PostgreSQL store through dustpan.WithPostgreSQL. Check: TestCmdUsesBuiltinPostgreSQLConstructor in NEW file cmd/opendustpan/builtin_wiring_test.go. No route: the test builds the server options only.

## 2. Wiring

- [ ] 2.1 Switch cmd/opendustpan to dustpan.WithSQLite and dustpan.WithPostgreSQL. Check: The two new tests pass. No route: the tests build the server options only.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Update the SDK docs page with both constructors. Check: `make docs-lint` passes. No route: the check reads files only.
- [ ] 4.2 Run a usability check of the constructor names from the docs page alone. Check: Persona follows the docs page and starts the server with each store kind. No route: the check walks docs and server start only. A person's step.
- [ ] 4.3 Run the full check. Check: `make check` with zero findings. No route: the check runs the full suite only. Confirmed by: the driver's gates.
