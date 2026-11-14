## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Edit pkg/notify/seal.go:217 (func SinkSealer.OpenDestination) to add failing hostile tests for plain, percent-encoded, uppercase-scheme and whitespace forms. Check: NEW test TestSinkSealerRefusesUserinfoAndScheme fails on main, passes after the fix.
- [ ] 1.2 Edit pkg/store/storetest/backup.go:1585 (func mustListRestores) to add migration tests with a realistic keyed sink, export and stage, on both dialects. Check: NEW test TestMigratedSinkExportStage fails on main and passes after the fix.

## 2. Fix

- [ ] 2.1 Edit pkg/notify/seal.go:217 (func SinkSealer.OpenDestination) to refuse scheme and userinfo in checkDestination and fix the OpenDestination doc wrap. Check: POST /api/dustpan/v1/namespaces/{}/sinks with an access token and a userinfo URL answers sink.invalid.
- [ ] 2.2 Edit pkg/notify/seal.go:217 (func SinkSealer.OpenDestination) and internal/api/server/restores.go:296 (func Handler.listRestores) to wire the empty-target refusal into list and RestoreService.Apply. Check: GET /api/dustpan/v1/namespaces/{}/sinks with an access token omits the row; ANY /api/server/restores with an access token fails with ErrCorrupt.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Update the webhook sink docs page with the http and https rule. Check: Docs page states that userinfo is never stored. Confirmed by: CI (make docs-lint).
- [ ] 4.2 Run the definition of done: wiring, route, docs, usability check, make check. Check: make check passes with zero findings. Confirmed by: CI (make check).
