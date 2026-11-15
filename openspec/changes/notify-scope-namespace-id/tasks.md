## 1. The scope type

- [ ] 1.1 In `pkg/notify/notify.go`, `pkg/notify/route.go` and `pkg/notify/ports.go`, add `notify.Scope` (NEW) with a namespace ID, a display name, or `@server`. Change the `Scope` fields of `Notification`, `Sink`, `Subscription`, `Delivery`, `CursorMove` and `ClaimRequest` to it. Check: `go build ./pkg/notify/...`.
- [ ] 1.2 Make `ScopeActions`, `Route`, `SendsFor` and `BeforeAttempt` compare the ID. Check: `TestRouteComparesTheScopeID` in `pkg/notify`.
- [ ] 1.3 Use the display name in `pkg/notify/webhook/encode.go`, `pkg/notify/chat/sender.go`, `internal/api/notifications/handlers.go` and `subscriptions.go`. Check: `TestWebhookUsesTheNamespaceName` and `TestSinkDocumentKeepsTheNamespaceName`.

## 2. Storage and generated code

- [ ] 2.1 Change `inScopedTx`, `checkNotifyFence`, `commonScope`, `Append`, `After`, `Scopes` and `MoveCursor` in `pkg/store/sqlite/notifications.go` and `pkg/store/postgres/notifications.go` to read, write and compare the ID. Check: `TestNotificationConformance` case "a namespace made again gets no notification rows" in both dialect packages.
- [ ] 2.2 Change the sink reads and writes in `pkg/store/sqlite/sinks.go` and `pkg/store/postgres/sinks.go`. Keep the namespace name for the sink secret scope, and change `pkg/notify/seal.go` to use it. Check: the sink cases of the same suite pass.
- [ ] 2.3 Change the notification queries in `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql` and run `make generate`. Check: `sqlc diff` is clean. Confirmed by: the driver's gates.
- [ ] 2.4 Change the notification rows of `pkg/store/sqlite/backup_notifications.go` and `pkg/store/sqlite/backup_notification_sinks.go`, and the PostgreSQL counterparts in `pkg/store/postgres`. Check: the backup and restore cases of the suite keep the ID.

## 3. Services and routes

- [ ] 3.1 Resolve the namespace ID in `internal/subscriptionsvc/subscriptionsvc.go` and pass it to every port. Check: the subscriptions API tests pass.
- [ ] 3.2 Add `TestRecreatedNamespaceListsNoSinks` (NEW) in the NEW file `internal/api/notifications/recreated_test.go`. Check: the new namespace lists no sink and no subscription.
- [ ] 3.3 Add the first production `notify.Reader` (pkg/notify/route.go:737) in the NEW file pkg/dustpan/notify_reader.go, the wiring layer, which may import pkg/identity, pkg/principal and pkg/notify (a domain package may not, by depguard). It is the type and constructor of the section Contract of proposal.md. Add `TestCanReadUsesTheScopeID` (NEW) and a hostile table (unknown principal, disabled principal, an authorizer error, @server without server:observe, a unit of another collection) in the NEW file pkg/dustpan/notify_reader_internal_test.go. Check: a principal of the new namespace cannot read a notification of the old ID; `go test ./pkg/dustpan/ -run 'TestCanRead' -count=1` passes.

## 4. Documentation and verification

- [ ] 4.1 Add one sentence to `docs/guides/notification-destinations.md`: a new namespace with the name of a deleted namespace gets no sinks, subscriptions, notifications or deliveries of the old one. Check: `make docs-lint` exits 0.
- [ ] 4.2 In `Makefile`, run `go test ./pkg/notify/... ./internal/subscriptionsvc/... ./internal/api/notifications/...` and the store packages with `-timeout 60m`. Check: the packages pass.
- [ ] 4.3 In `Makefile`, run `make check`. Check: zero findings. Confirmed by: CI (the agent runs the fast checks only).
