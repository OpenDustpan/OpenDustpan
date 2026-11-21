## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Failing tests for bad input error that names the input, in pkg/catalog/request_test.go (NEW). Check: TestSelfServiceRequestValidation
- [ ] 1.2 Failing tests for quota refusal, in pkg/catalog/quota_test.go (NEW). Check: TestSelfServiceQuotaRefused
- [ ] 1.3 Failing tests for deterministic render, in pkg/catalog/render_test.go (NEW). Check: TestSelfServiceRenderDeterministic
- [ ] 1.4 Failing tests for one revision in flight and retry, in pkg/catalog/concurrent_test.go (NEW). Check: TestSelfServiceConcurrentSubmit
- [ ] 1.5 Failing tests for Idempotency-Key retry and conflict, in pkg/catalog/idempotency_test.go (NEW). Check: TestSelfServiceIdempotencyKey
- [ ] 1.6 Failing authorization test, in pkg/catalog/authz_test.go (NEW). Check: TestSelfServiceDeny

## 2. Build

- [ ] 2.1 Implement the core and quota reservation in pkg/catalog/engine.go (NEW). Check: `go test ./pkg/catalog/... ./pkg/quota/...` passes
- [ ] 2.2 Wire the NEW request routes with actions and audit, declared in the route table of its adapter package (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand) and adapted in internal/api/selfservice/requests.go (NEW, in the existing package internal/api/selfservice). Check: `POST /api/dustpan/v1/namespaces/{namespace}/self-service/requests` (NEW) makes a unit seen in `GET /api/dustpan/v1/namespaces/{namespace}/units`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Route test lists each route with its action Check: route test passes
- [ ] 4.2 e2e step with the real Pulumi CLI and OpenTofu: publish through POST /api/dustpan/v1/self-service/blueprints/{}/{}/versions, submit through the NEW POST /api/dustpan/v1/namespaces/{namespace}/self-service/requests with an access token, a runner applies Check: `make e2e` passes on SQLite and PostgreSQL.
- [ ] 4.3 Docs page commands run as written Check: docs/guides/publish-a-blueprint.md commands run in a test.go runs each command of the docs page against the e2e test server).
- [ ] 4.4 Usability check from the docs page alone Check: Each error names one command.
- [ ] 4.5 Run make check Check: `make check` reports zero findings.
