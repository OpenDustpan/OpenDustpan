## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Moves

- [ ] 1.1 Move jobs and runners wiring into the NEW file `pkg/dustpan/area_jobs.go` with explicit arguments and unchanged order Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- [ ] 1.2 After the architecture review of move 1, move backups and restore wiring into the NEW file `pkg/dustpan/area_backup.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- [ ] 1.3 Move sign-in and local-users wiring into the NEW file `pkg/dustpan/area_signin.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- [ ] 1.4 Move notifications wiring into the NEW file `pkg/dustpan/area_notifications.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`
- [ ] 1.5 Move triggers wiring into the NEW file `pkg/dustpan/area_triggers.go` Check: `go test ./pkg/dustpan -run TestNewServerAuthenticatorChain`

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Confirm the server still serves the same routes and the exported SDK API is unchanged, with the full test run and `make check`. Confirmed by: CI (make check). Check: `make check`
