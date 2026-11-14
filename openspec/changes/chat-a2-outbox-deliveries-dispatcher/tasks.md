## 1. Failing tests first

- [ ] 1.1 Write failing store conformance tests for outbox append, list, and claim in both dialects. Check: TestOutboxAppendInSameTransaction fails on main, passes after the change.
- [ ] 1.2 Write failing tests for subscription match, duplicate create, oversize append, invalid UTF-8, lease claim, claim snapshot, idempotent reclaim, idempotency key, retry, dead-letter, manual retry guards, retention with dead-letter kept, and read-route 404 and 403. Check: TestSubscriptionsMatchAndDeliveriesWrite fails on main, passes after the change.

## 2. Build

- [ ] 2.1 Implement outbox, subscriptions, and deliveries stores for SQLite and PostgreSQL with lease, idempotency key, and retention purge. Check: `go test -race ./pkg/notify/... ./internal/subscriptionsvc/...` passes.
- [ ] 2.2 Implement the dispatcher and the delivery operation with per-subscription retry. Check: `go test -race ./internal/notifydispatch/...` passes, including per-sink isolation, retry, and dead-letter cases.
- [ ] 2.3 Wire the removal alert service as the first emitting domain to append a notification in its write transaction. Check: `go test -race ./internal/removalalertsvc/...` passes with one appended server-scope removal alert per recorded alert.

## 3. Definition of done

- [ ] 3.1 Wire routes with authorization actions, docs page, e2e step against the real server, usability check, and make check with zero findings. Check: POST then GET /api/dustpan/v1/namespaces/acme/subscriptions returns 200 with the declared actions, and GET outbox and deliveries reads return 200.
