## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for bundle save checks, rotation and active delete in pkg/trustrule Check: go test ./pkg/trustrule/... -run TestBundleSaveRefusesBadPEM|TestBundleRotate|TestBundleDeleteActive fails on main
- [ ] 1.2 Write failing tests for rule create, update, delete, reach and fresh session in internal/trustrulesvc Check: go test ./internal/trustrulesvc/... -run TestRuleCreate|TestRuleUpdateNeedsETag|TestRuleDelete|TestTokenAloneRefused fails on main

## 2. Build

- [ ] 2.1 Extend pkg/trustrule and the store with bundle versions and rule fields; add the two tables in name order to each baseline Check: go test ./pkg/trustrule/... passes on SQLite and PostgreSQL
- [ ] 2.2 Extend internal/trustrulesvc and internal/api/trustrules with bundle routes and rule update, with actions and audit Check: route action test passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Docs page for bundle and rules Check: a test runs the docs commands against a dev server. Confirmed by: CI (an e2e step in the NEW file e2e/mtls_trust_bundle_rules_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.2 Usability check from the docs page alone Check: each error names one command. A person's step.
- [ ] 4.3 Full check Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
