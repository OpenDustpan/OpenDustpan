## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Side effects (computed from main)

- [ ] 1.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 1.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 2. Docs and checks

- [ ] 2.1 Add the user docs page that lists each fail-closed identity default and its effective value in `NEW docs/guides/identity-defaults.md`. Check: `make docs-lint` passes on `NEW docs/guides/identity-defaults.md`.
- [ ] 2.2 Run a usability check of `NEW docs/guides/identity-defaults.md` from the docs alone. A person's step. Check: Read `NEW docs/guides/identity-defaults.md` alone and state each default and its denial. A person's step.
