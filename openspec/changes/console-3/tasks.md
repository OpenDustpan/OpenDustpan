## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing browser test in console repo that opens the catalog and lists published blueprints Check: TestCatalogListsBlueprints fails on main before the fix
- [ ] 1.2 Add failing browser test in console repo that fills a bad input and sees the error that names the input Check: TestCreateFormBadInput fails on main before the fix
- [ ] 1.3 Add failing browser test in console repo that sends a valid request and sees the new unit Check: TestCreateFormMakesUnit fails on main before the fix
- [ ] 1.4 Add failing browser test in console repo for a blueprint removed before submit Check: TestCreateFormGoneBlueprint fails on main before the fix
- [ ] 1.5 Add failing browser test in console repo for timeout retry with the same Idempotency-Key Check: TestCreateFormIdempotentRetry fails on main before the fix
- [ ] 1.6 Add failing browser test in console repo that submits script text and oversized values with no script run Check: TestCreateFormHostileText fails on main before the fix

## 2. Build

- [ ] 2.1 Build catalog page in console repo wired to the REST API of the blueprints change Check: Catalog lists blueprints with read grant and shows empty, paged, denied, and not-found states
- [ ] 2.2 Build create form in console repo that sends the request with Idempotency-Key and maps field errors by input name Check: Bad input shows each server error unchanged as plain text next to the named input with raw JSON mode
- [ ] 2.3 Order and filter inputs by blueprint version in console repo Check: Inputs render in server order and inapplicable inputs stay hidden with stale-version message

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run console browser gate in this repo against the real server Check: Browser suite in console repo runs against the real binary over TLS from the server of the suite
- [ ] 4.2 Usability check of the full create path Check: Developer completes catalog to unit path from the docs page alone
- [ ] 4.3 Run full checks Check: `make check` with zero findings
