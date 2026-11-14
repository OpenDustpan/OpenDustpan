## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service tests for the hourly cap, version cap, file cap, release cap and settings override Check: TestRegistryUploadRateLimitRefuses61st in pkg/registry fails on main
- [ ] 1.2 Add failing tests that refused uploads do not count, yanked versions count, blob failure releases the count, and streaming abort deletes partial blobs Check: TestRegistryLimitCountingScope in pkg/registry fails on main

## 2. Implementation

- [ ] 2.1 Enforce caps in NEW file pkg/registry/limits.go Check: `go test ./pkg/registry/...` passes
- [ ] 2.2 Add NEW codes registry.upload-rate-limited and registry.version-limit to internal/errcode/catalog_registry.go Check: `go test ./internal/errcode/...` passes
- [ ] 2.3 Edit the baseline migration in place for the hourly counter table, in the NEW path pkg/store/sqlite/migrations/ baseline file and the same under pkg/store/postgres/migrations/, with no numbered migration Check: `go test ./pkg/store/...` passes
- [ ] 2.4 Document the four caps, keys, bounds, Retry-After and codes in docs/reference/registry-api.md Check: `grep -E 'registry.upload-rate-limited|registry.version-limit' docs/reference/registry-api.md` finds the table

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 E2e upload burst against the real API with an access token Check: POST /api/dustpan/v1/namespaces/{}/registry/uploads with an access token holding registry:publish shows 429 with Retry-After on the 61st upload
- [ ] 4.2 Settings wiring through the installation settings resource Check: PATCH /api/dustpan/v1/settings with an access token updates registry.uploadsPerHour and GET /api/dustpan/v1/settings shows it
- [ ] 4.3 Wiring, route with registry:publish, docs page, usability check Check: `make check` with zero findings
