## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service test for set with probe pass and probe fail in NEW file pkg/keysvc/key_test.go. Check: NEW file pkg/keysvc/key_test.go: TestKeySetStoresOnlyAfterProbePasses fails on main.
- [ ] 1.2 Add failing route test for NEW actions namespace:manage-key and namespace:read-key in NEW file internal/api/key/key_test.go. Check: NEW file internal/api/key/key_test.go: TestKeyProbeDeniesWrongNamespace fails on main.
- [ ] 1.3 Add failing start check test for wrapper ID mismatch in NEW file pkg/keysvc/start_test.go. Check: NEW file pkg/keysvc/start_test.go: TestKeyStartRefusesWrapperMismatch fails on main.

## 2. Build

- [ ] 2.1 Build NEW key set, change, clear, read, and probe routes with fence and audit entries in NEW file internal/api/key/routes.go. Check: NEW file internal/api/key/routes.go: NEW POST /api/dustpan/v1/namespaces/{}/key/probe returns pass or failed check with access token.
- [ ] 2.2 Build key cache, start check, and outage answers with Retry-After in NEW file pkg/keysvc/cache.go. Check: NEW file pkg/keysvc/cache.go: cache drops entries per keys.namespaceKeyCacheSeconds and on revoke.
- [ ] 2.3 Write customer keys docs page in NEW file docs/guides/customer-keys.md with set, rotate, probe, revoke, and outage steps. Check: NEW file docs/guides/customer-keys.md passes docs lint.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run e2e step for set, probe, read, and clear on a dev server with an access token against NEW key routes; BLOCKED by NEW key routes until they land. Check: BLOCKED by NEW key routes: GET and PUT against NEW /api/dustpan/v1/namespaces/{}/key from the docs page alone with an access token, plus Pulumi CLI read during KMS outage shows 503 shape.
- [ ] 4.2 Run usability check, route action check with pkg/access/access_test.go:466, and full check. Check: `make check` reports zero findings.
