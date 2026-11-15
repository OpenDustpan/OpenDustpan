## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Write failing tests for bundle save checks, rotation and active delete in pkg/trustrule. The task adds the NEW top-level tests `TestBundleSaveRefusesBadPEM`, `TestBundleRotate`, `TestBundleDeleteActive` in a `_test.go` file of `pkg/trustrule`, so that the Check runs them. Check: go test ./pkg/trustrule/... -run TestBundleSaveRefusesBadPEM|TestBundleRotate|TestBundleDeleteActive fails on main
- [x] 1.2 Write failing tests for rule create, update, delete, reach and fresh session in internal/trustrulesvc. The task adds the NEW top-level tests `TestRuleCreate`, `TestRuleUpdateNeedsETag`, `TestRuleDelete`, `TestTokenAloneRefused` in a `_test.go` file of `internal/trustrulesvc`, so that the Check runs them. Check: go test ./internal/trustrulesvc/... -run TestRuleCreate|TestRuleUpdateNeedsETag|TestRuleDelete|TestTokenAloneRefused fails on main

## 2. Build

- [x] 2.1 Extend pkg/trustrule with bundle versions and rule fields, and add `Update` to the `trustrule.Store` port. Add the conformance tests of the new method to pkg/trustrule/trustruletest first. Check: go test ./pkg/trustrule/... passes
- [x] 2.2 Implement `Update` and the bundle methods on both SQL adapters, each of which asserts `var _ trustrule.Store`: pkg/store/sqlite/trustrules.go and pkg/store/postgres/trustrules.go. Put the shared checks in pkg/store/internal/trustrulestore/trustrulestore.go. Add the new queries in name order to pkg/store/sqlite/queries.sql and pkg/store/postgres/queries.sql, then run `make generate` for internal/db. Add the tables `trust_bundles` and `trust_bundle_versions` in name order to pkg/store/sqlite/migrations/0168_baseline.sql and pkg/store/postgres/migrations/0168_baseline.sql. If the store move has changed these paths on main, use the new paths. Check: go build ./... and go test ./pkg/store/... -run TestTrustRule pass on SQLite and PostgreSQL
- [x] 2.3 Extend internal/trustrulesvc and internal/api/trustrules with bundle routes and rule update, with actions and audit Check: route action test passes

## 3. Side effects (computed from main)

- [x] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [x] 4.1 Docs page for bundle and rules Check: a test runs the docs commands against a dev server. Confirmed by: CI (an e2e step in the NEW file e2e/mtls_trust_bundle_rules_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.2 Usability check from the docs page alone Check: each error names one command. A person's step.
- [ ] 4.3 Full check Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
