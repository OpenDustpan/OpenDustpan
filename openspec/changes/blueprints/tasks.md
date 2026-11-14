## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Failing tests for bad input error that names the input Check: TestSelfServiceRequestValidation
- [ ] 1.2 Failing tests for quota refusal Check: TestSelfServiceQuotaRefused
- [ ] 1.3 Failing tests for deterministic render Check: TestSelfServiceRenderDeterministic
- [ ] 1.4 Failing tests for one revision in flight and retry Check: TestSelfServiceConcurrentSubmit
- [ ] 1.5 Failing tests for Idempotency-Key retry and conflict Check: TestSelfServiceIdempotencyKey
- [ ] 1.6 Failing authorization test Check: TestSelfServiceDeny

## 2. Build

- [ ] 2.1 Implement the core and quota reservation Check: `go test ./pkg/catalog/... ./pkg/quota/...` passes
- [ ] 2.2 Wire the NEW request routes with actions and audit Check: `POST /api/dustpan/v1/namespaces/{namespace}/self-service/requests` (NEW) makes a unit seen in `GET /api/dustpan/v1/namespaces/{namespace}/units`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Route test lists each route with its action Check: route test passes
- [ ] 4.2 e2e step with the real Pulumi CLI and OpenTofu: publish through POST /api/dustpan/v1/self-service/blueprints/{}/{}/versions, submit through the NEW POST /api/dustpan/v1/namespaces/{namespace}/self-service/requests with an access token, a runner applies Check: `make e2e` passes on SQLite and PostgreSQL
- [ ] 4.3 Docs page commands run as written Check: docs/guides/publish-a-blueprint.md commands run in a test
- [ ] 4.4 Usability check from the docs page alone Check: Each error names one command
- [ ] 4.5 Run make check Check: `make check` reports zero findings
