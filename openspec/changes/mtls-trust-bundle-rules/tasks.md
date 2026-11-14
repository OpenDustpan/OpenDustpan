## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for bundle save checks, rotation and active delete in pkg/trustrule Check: go test ./pkg/trustrule/... -run TestBundleSaveRefusesBadPEM|TestBundleRotate|TestBundleDeleteActive fails on main
- [ ] 1.2 Write failing tests for rule create, update, delete, reach and fresh session in internal/trustrulesvc Check: go test ./internal/trustrulesvc/... -run TestRuleCreate|TestRuleUpdateNeedsETag|TestRuleDelete|TestTokenAloneRefused fails on main

## 2. Build

- [ ] 2.1 Extend pkg/trustrule and the store with bundle versions and rule fields; add the two tables in name order to each baseline Check: go test ./pkg/trustrule/... passes on SQLite and PostgreSQL
- [ ] 2.2 Extend internal/trustrulesvc and internal/api/trustrules with bundle routes and rule update, with actions and audit Check: route action test passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Docs page for bundle and rules Check: a test runs the docs commands against a dev server
- [ ] 4.2 Usability check from the docs page alone Check: each error names one command
- [ ] 4.3 Full check Check: `make check` reports zero findings
