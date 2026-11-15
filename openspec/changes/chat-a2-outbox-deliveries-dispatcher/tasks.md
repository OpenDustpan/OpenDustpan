## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add store conformance tests for the Outbox, Subscriptions and Deliveries ports in the NEW file pkg/notify/notifytest/dispatch_suite.go, run by the SQL adapters. Check: TestOutboxCursorDispatch fails on main
- [ ] 1.2 Add dispatcher tests in the NEW file internal/notifydispatch/dispatcher_test.go for match, isolation, key, mark sent, retry, dead-letter and no match. They read the existing route GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries with an access token of a principal. Add the route test in the NEW file internal/api/notifications/deliveries_read_test.go for that route with its action. Check: TestDispatcherFansOutInIsolation fails on main

## 2. Build

- [ ] 2.1 Implement the stores in the NEW files pkg/store/sqlite/notify_dispatch.go and pkg/store/postgres/notify_dispatch.go with queries in each queries.sql and the baseline migration. Check: go test -race ./pkg/store/... -timeout 60m passes
- [ ] 2.2 Implement the dispatcher and the delivery operation in the NEW file internal/notifydispatch/dispatcher.go. It marks a delivery sent after the sink accepts it. The acceptance check reads GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries with an access token. Check: go test -race ./internal/notifydispatch/... passes
- [ ] 2.3 Wire the removal alert service at internal/removalalertsvc/removalalertsvc.go:256 and the dispatcher in pkg/dustpan (NEW file pkg/dustpan/notifydispatch.go). Declare the delivery read route and its action in the NEW file internal/api/notifications/deliveries_routes.go and serve it in the NEW file internal/api/notifications/deliveries_handler.go. The acceptance check calls GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries with an access token. Check: TestRemovalAlertIsDelivered passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Check the routes of internal/api/notifications with their actions: POST /api/dustpan/v1/namespaces/{namespace}/deliveries/{id}/redeliver with an access token. Add the docs page NEW docs/guides/chat-notifications.md and an e2e step in the NEW file e2e/chat_delivery_test.go. Check: make e2e passes and make check reports zero findings. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only). `make docs-lint` passes on each edited guide.
