## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service tests for Patch refusal, Get locked flag, and namespace scope in `pkg/setting/service.go`. Check: `go test ./pkg/setting -run TestStaticOffLock -count=1` covers `pkg/setting/service.go`
- [ ] 1.2 Add failing API tests for capabilities off, the NEW error code `module.off` refusal, and divergent digest 503 in `internal/api/server/settings_test.go:468`. Check: `go test ./internal/api/server -run TestStaticOffLockCapabilities -count=1` covers `internal/api/server/settings_test.go:468`

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the stored-snapshot lock through Get, Patch, and Sync in `pkg/setting/service.go` on route `ANY/GET/PATCH /api/dustpan/v1/settings` with action installation:manage-settings. Check: `PATCH /api/dustpan/v1/settings` on a locked switch answers 409 settings.locked with an access token
- [ ] 3.2 Wire the WARN line, @server entry, per-namespace first-apply entry, and admin report row for a security switch lock in `internal/api/server/settings_test.go:468`. Check: `go test ./internal/api/server -run TestStaticOffLockReports -count=1` covers `internal/api/server/settings_test.go:468`
- [ ] 3.3 Write the docs page section on the static off lock in `docs/reference/settings-api.md` and run the usability check. Check: Operator completes the path from `docs/reference/settings-api.md` alone; refusal names the one file fix
- [ ] 3.4 Run the full gate and confirm wiring, routes, e2e step, docs page, and usability in `docs/reference/settings-api.md`. Check: `make check` reports zero findings after `GET /api/dustpan/v1/settings` with an access token passes on route `ANY/GET/PATCH /api/dustpan/v1/settings`
