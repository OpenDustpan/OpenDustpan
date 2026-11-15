## 1. Hostile tests first

- [ ] 1.1 SECURITY. No route: the test calls the subscription service only. Add `TestRedeliverTooSoonAudited` (NEW) in `internal/subscriptionsvc/redeliver_test.go`: a table of too-soon redelivers; each returns the too-soon refusal, changes no delivery row, and appends exactly one entry with outcome denied, kind change, and reason `sink.delivery-too-soon` (a NEW code that #2899 adds); a row with an audit store that fails returns the refusal joined with the append error. Check: `go test ./internal/subscriptionsvc/ -run TestRedeliverTooSoonAudited -count=1` fails on main.
- [ ] 1.2 SECURITY. Add the case "a too-soon redeliver writes a denied entry" to `internal/api/notifications/subscriptions_test.go`: the route POST /api/dustpan/v1/namespaces/{namespace}/deliveries/{delivery_id}/redeliver with an access token answers 429 with `sink.delivery-too-soon` (a NEW code that #2899 adds) and `Retry-After`, and the audit list of the namespace then holds one denied entry for it. Check: `go test ./internal/api/notifications/ -run Redeliver -count=1` passes.

## 2. Build

- [ ] 2.1 SECURITY. In `Service.Redeliver` (`internal/subscriptionsvc/subscriptionsvc.go:313`), on the too-soon refusal of the store, append one denied entry in the namespace of the fence with the reason `sink.delivery-too-soon` (a NEW code that #2899 adds), as the service does for the refusal at the limit, and join an append error with the refusal. Check: `go test ./internal/subscriptionsvc/ -run TestRedeliverTooSoonAudited -count=1` passes.
- [ ] 2.2 SECURITY. Prove the guard: run the test of 1.1 once with the append of 2.1 removed in `internal/subscriptionsvc/subscriptionsvc.go`, and paste the failing output into the pull request body. Check: the pull request body holds the failing run.

## 3. Definition of done

- [ ] 3.1 In `docs/guides/notification-destinations.md`, say that a refused redeliver, too soon or at the limit, writes a denied audit entry. Check: `make docs-lint` passes.
- [ ] 3.2 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
