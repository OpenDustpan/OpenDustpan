## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write PolicyStore conformance cases for put, get, delete, effective order, restart of the pause state and delete of the object in pkg/drift/drifttest/policy.go. Check: `go test ./pkg/drift/drifttest/ -run TestPolicy -count=1` fails on main
- [ ] 1.2 Write Scheduled cases in the NEW file internal/driftsvc/scheduled_test.go: off starts nothing, on starts the source kind schedule, 5 failures pause and send unit.drift-schedule-paused, a lock refusal counts 0, a clean check resets, the hashed offset, the 100-start limit, one start for two claims of a slot. Check: `go test ./internal/driftsvc/ -run TestScheduled -count=1` fails on main
- [ ] 1.3 Write route cases in the NEW file internal/api/drift/schedule_test.go for 400, 403, 404, the 256-byte cron limit, a cron with a gap under 1 hour and the explicit off. They call the NEW routes with an access token. Check: `go test ./internal/api/drift/ -run TestSchedule -count=1` fails on main

## 2. Build

- [ ] 2.1 Add the SQL adapters in pkg/store/sqlite and pkg/store/postgres, edit the baseline migrations and queries.sql, and run drifttest through pkg/store/storetest/drift.go. Check: `go test ./pkg/store/... -run TestDrift -count=1 -timeout 60m` passes
- [ ] 2.2 Add the NEW routes in internal/api/drift/drift.go with the settings action of the object. Check: `go test ./internal/api/drift/ -run TestSchedule -count=1` passes
- [ ] 2.3 Add Scheduled in internal/driftsvc/driftsvc.go on the durable schedule claim, with the existing source kind schedule of pkg/job/source.go, and the pause notification of the NEW type unit.drift-schedule-paused. Check: `go test ./internal/driftsvc/ -run TestScheduled -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire in pkg/dustpan/drift.go. Add the e2e step in e2e/ with the real CLI. It sets the schedule with PUT on the NEW unit schedule route, then reads GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift, with an access token that holds the settings action. Add the docs page and run the usability check from the page alone. Check: `make e2e` shows a scheduled check for the unit with the setting and none for the other unit
- [ ] 4.2 Run all checks. Check: `make check` reports zero findings
