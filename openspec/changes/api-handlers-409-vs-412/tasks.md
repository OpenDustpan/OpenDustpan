## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing test TestApprovalIfMatchMismatchAnswers412 in NEW file `internal/api/server/approval_ifmatch_test.go` for PUT and DELETE of route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/approval-rules/{} with an access token Check: `go test ./internal/api/server -run TestApprovalIfMatchMismatchAnswers412` fails on main with 409
- [ ] 1.2 Write failing test TestNamedRouteIdempotencyReplay in `internal/api/idempotent/keys_test.go:176` for collection, lock, approval, preferences and context mutating routes with an access token Check: `go test ./internal/api/idempotent -run TestNamedRouteIdempotencyReplay` fails on main with the header ignored

## 2. Fix

- [ ] 2.1 Map stale approval version with If-Match present to 412 in the approval handlers in `internal/api/route` for route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/approval-rules/{} with an access token Check: TestApprovalIfMatchMismatchAnswers412 passes on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/approval-rules/{} with an access token
- [ ] 2.2 Wire Idempotency-Key store and replay in `pkg/store/internal/codec/idempotency.go:89 (func EncodeIdempotencyHeaders)` on the named routes after authentication and authorization Check: TestNamedRouteIdempotencyReplay passes on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/collections/{}/lock with an access token
- [ ] 2.3 Update the API description in internal/api/apidesc with the 412 response and idempotency header Check: `go test ./pkg/dustpan -run TestDescriptionSchemasAreConsistent` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 E2E step against the real server for the 412 path and one idempotency retry Check: `make e2e` passes for PUT /api/dustpan/v1/namespaces/{namespace}/approval-rules/{id} with an access token and stale If-Match expecting 412
- [ ] 4.2 Update the docs page for preconditions and Idempotency-Key scope Check: `make docs-lint` passes and each curl runs as written
- [ ] 4.3 Run the full checks Check: `make check` reports zero findings
