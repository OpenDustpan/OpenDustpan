## 1. Hostile tests first

- [ ] 1.1 SECURITY. Add `TestRedeliverTooSoonRecorded` (NEW) in `internal/api/notifications/subscriptions_test.go`: the route POST /api/dustpan/v1/namespaces/{namespace}/deliveries/{delivery_id}/redeliver with an access token, sent too soon, answers 429 with `sink.delivery-too-soon` (a NEW code that #2899 adds) and `Retry-After`, changes no delivery, and the audit list of the namespace then holds one denied entry with that reason. Check: `go test ./internal/api/notifications/ -run TestRedeliverTooSoonRecorded -count=1` fails on main.

## 2. Build

- [ ] 2.1 SECURITY. In `Handler.redeliver` (`internal/api/notifications/subscriptions.go:505`), when the service answers the too-soon refusal, record it through the refusal recorder of the adapter (the field guard of Handler, which holds the shared denial limiter) with the action of the route, the delivery object and the reason `sink.delivery-too-soon` (a NEW code that #2899 adds), before it writes the answer. The service writes no entry for it. Check: `go test ./internal/api/notifications/ -run TestRedeliverTooSoonRecorded -count=1` passes.
- [ ] 2.2 SECURITY. Prove the guard: run the test of 1.1 once with the record of 2.1 removed in `internal/api/notifications/subscriptions.go`, and paste the failing output into the pull request body. Check: the pull request body holds the failing run.

## 3. Definition of done

- [ ] 3.1 In `docs/guides/notification-destinations.md`, say that a refused redeliver writes a denied audit entry, and that repeated refusals in one minute write one entry with a count. Check: `make docs-lint` passes.
- [ ] 3.2 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
