## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for bundle save checks, rotation, delete and ETag Check: go test ./pkg/trust/... -run TestBundleSaveRefusesBadPEM|TestBundleRotate|TestBundleDeleteActive fails on main
- [ ] 1.2 Add failing tests for rule create, update, delete, fence, reach and fresh session Check: go test ./pkg/trust/... -run TestRuleCreate|TestRuleUpdateNeedsETag|TestRuleDelete|TestTokenAloneRefused fails on main

## 2. Build

- [ ] 2.1 Build bundle store with rotation and fence Check: go test ./pkg/trust/... passes on SQLite and PostgreSQL
- [ ] 2.2 Wire routes with actions and audit Check: route action test passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Docs page for bundle and rules Check: docs commands run as written against a dev server
- [ ] 4.2 Full check and usability check Check: `make check` reports zero findings
