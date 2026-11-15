## 1. Hostile tests first

- [ ] 1.1 SECURITY. Add `TestRedeliverRefusalsThroughDenialLimiter` (NEW) in `internal/api/notifications/subscriptions_test.go`, a table on the route POST /api/dustpan/v1/namespaces/{namespace}/deliveries/{delivery_id}/redeliver with an access token: N identical refusals at the limit in one window give one denied entry and, with the next entry of the key, a count of N-1; N too-soon refusals give the same; a refusal by another person, and a refusal in another namespace, each give their own entry; each refusal answers its status (409 at the limit, 429 too soon) and changes no delivery. Check: `go test ./internal/api/notifications/ -run TestRedeliverRefusalsThroughDenialLimiter -count=1` fails on main.

## 2. Build

- [ ] 2.1 SECURITY. In `Handler.redeliver` (`internal/api/notifications/subscriptions.go:505`), record the refusal at the limit through the refusal recorder of the adapter (the field guard of Handler) with the action of the route, the delivery object and the reason `sink.delivery-redeliver-limit` (a NEW code that #2899 adds), as for the too-soon refusal. Remove the own audit append for that refusal from `Service.Redeliver` (`internal/subscriptionsvc/subscriptionsvc.go`), with its test row. Check: `go test ./internal/api/notifications/ ./internal/subscriptionsvc/ -count=1` passes.
- [ ] 2.3 After 2.1, `Config.Audit` of `internal/subscriptionsvc` has no reader. Remove the field, its nil check in `NewService` and the `audit` field of the service. Remove `Audit:` from every literal of `subscriptionsvc.Config`: `pkg/dustpan/sinks.go` (the call in the sink wiring), `e2e/coverage_sink.go`, `internal/api/notifications/fixture_test.go`, `internal/subscriptionsvc/redeliver_test.go` and `internal/subscriptionsvc/history_test.go`; remove the test fakes that only fed that field. Check: `go build ./... && go vet ./pkg/dustpan/ ./e2e/ ./internal/subscriptionsvc/ ./internal/api/notifications/` passes and `rg 'Audit:' internal/subscriptionsvc pkg/dustpan/sinks.go e2e/coverage_sink.go` finds no `subscriptionsvc.Config` literal.
- [ ] 2.2 SECURITY. Prove the guard: run the test of 1.1 once with the record of 2.1 removed in `internal/api/notifications/subscriptions.go`, and paste the failing output into the pull request body. Check: the pull request body holds the failing run.

## 3. Definition of done

- [ ] 3.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
