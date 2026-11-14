## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing store conformance tests for outbox append, list, and claim in both dialects. Check: TestOutboxAppendInSameTransaction fails on main, passes after the change.
- [ ] 1.2 Write failing tests for match, duplicate create, oversize append, invalid UTF-8, lease claim, stored match set, idempotent reclaim, key, retry, dead-letter, manual retry guards, retention, and 404 and 403 on routes. Check: TestSubscriptionsMatchAndDeliveriesWrite fails on main, passes after the change.

## 2. Build

- [ ] 2.1 Implement outbox, subscriptions, and deliveries stores for SQLite and PostgreSQL with lease, stored match set, key, and retention purge. Check: `go test -race ./pkg/notify/... ./internal/subscriptionsvc/...` passes.
- [ ] 2.2 Implement the dispatcher and the delivery operation with per-subscription retry. Check: `go test -race ./internal/notifydispatch/...` passes. The test calls the existing route POST /api/dustpan/v1/namespaces/{namespace}/deliveries/{id}/redeliver with a personal access token. The send and retry paths need no route.
- [ ] 2.3 Wire the removal alert service as the first emitting domain. Check: `go test -race ./internal/removalalertsvc/...` passes with one appended server-scope removal alert per recorded alert.

## 3. Definition of done

- [ ] 3.1 Wire routes with actions, docs page, e2e step against the real server, usability check, and make check with zero findings. Check: The e2e step calls the existing routes GET and POST /api/dustpan/v1/namespaces/{namespace}/subscriptions and GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries with a personal access token and gets 200; `make check` reports zero findings.
