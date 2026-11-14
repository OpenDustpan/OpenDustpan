## 1. Failing tests first

- [ ] 1.1 Add failing browser test that opens the catalog and lists published blueprints Check: TestCatalogListsBlueprints fails on main before the fix
- [ ] 1.2 Add failing browser test that fills a bad input and sees the error that names the input Check: TestCreateFormBadInput fails on main before the fix
- [ ] 1.3 Add failing browser test that sends a valid request and sees the new unit Check: TestCreateFormMakesUnit fails on main before the fix
- [ ] 1.4 Add failing browser test for a blueprint removed before submit Check: TestCreateFormGoneBlueprint fails on main before the fix

## 2. Build

- [ ] 2.1 Wire catalog list to the REST API of the blueprints change Check: Catalog lists blueprints with read grant and shows empty and not-found states
- [ ] 2.2 Wire create form to send the request with Idempotency-Key and map field errors by input name Check: Bad input shows each server error unchanged as plain text next to the named input with raw JSON mode
- [ ] 2.3 Order and filter inputs by blueprint version Check: Inputs render in server order and inapplicable inputs stay hidden
- [ ] 2.4 Add hostile input test for the create form Check: Oversized value and script text run no script in the console
- [ ] 2.5 Write docs page for catalog create flow in the same pull request Check: Docs page commands run as written in a test

## 3. Definition of done

- [ ] 3.1 Run console browser tests against the server of the suite Check: Browser suite runs against the real binary over TLS
- [ ] 3.2 Usability check of the full create path Check: Developer completes catalog to unit path from the docs page alone
- [ ] 3.3 Run full checks Check: `make check` with zero findings
