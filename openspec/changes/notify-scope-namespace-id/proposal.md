Issue: #2991.

## Problem

A namespace administrator deletes a namespace and creates another with the same
name. The new namespace lists the notification sinks and subscriptions of the
old one, because each notification row keys on the namespace name. The new
namespace also gets the old notifications, cursors and deliveries. A namespace
ID is stable and a new namespace gets a new one, so a row keyed by the ID never
reaches the new namespace. The unit filter that matches a stack name is a
separate bug; this change covers only the namespace-ID half.

## Evidence

- `pkg/notify/notify.go:254` — `Scope      namespace.Name` — a notification scope is a namespace name.
- `pkg/notify/route.go:89` — `Scope      namespace.Name` (sink); `route.go:671` — `Scope     namespace.Name` (subscription); `pkg/notify/ports.go:71` — `Scope    namespace.Name` (cursor move); `ports.go:253` — `Scope        namespace.Name` (delivery); `ports.go:434` — `Scope namespace.Name` (claim request).
- `pkg/notify/route.go:532` — `type Filter struct {`; `route.go:534` — `Objects     []string`; `route.go:561` — `return matchList(f.Types, string(n.Type), ".*") && matchList(f.Objects, n.Object, "/*")` — the filter matches the object name text. This is the half that stays out.
- `pkg/store/sqlite/migrations/0168_baseline.sql:1183-1192` — `CREATE TABLE notification_sinks (` with `scope      TEXT    NOT NULL,` and no foreign key. The same `scope` column is at lines 1195, 1204, 1210, 1230, 1235, 1243 and 1256.
- `pkg/namespace/namespace.go:97` — `// namespace gets a new ID, so rows that hold the old ID never reach the new` — the notification rows do not follow this rule.
- `pkg/notify/route.go:749` — `CanRead(ctx context.Context, id string, n Notification) (bool, error)` — the port's only input about the scope is `n.Scope`, a `namespace.Name`.
- `pkg/notify/notify_test.go:355` — `func (r reader) CanRead(_ context.Context, id string, _ notify.Notification) (bool, error) {` — the only implementation in the checkout is this test fake. No production adapter exists, so the claimed read leak after a recreated name cannot be verified here. The fix makes the namespace ID the only scope value, so the implementation can decide by ID.
- `pkg/notify/seal.go:325` — `scope, err := secret.NewScope(s.Scope, s.ID)` — the sink secret uses the sink scope as its secret item scope. A secret item is keyed by the namespace name, so the fix keeps the name for this one call and changes only the row key. The store resolves the name from the namespace row.
- `pkg/notify/webhook/encode.go:59` — `Scope:         n.Scope.String(),` and `pkg/notify/chat/sender.go:380` — `add("Namespace", n.Scope.String())` — the webhook payload and the chat message show the scope as text.
- `internal/api/notifications/handlers.go:106` — `ID: s.ID, Namespace: s.Scope.String(), Kind: string(s.Kind), Platform: s.Platform, Name: s.Name,` — the sink answer shows the scope as its `namespace` field.
- No production delete of a namespace exists. The only delete is the test hook `pkg/store/sqlite/inventory_changes_internal_test.go:44` (and the PostgreSQL counterpart). It deletes the namespace row and keeps the rows that name it; `pkg/store/storetest/lifecycle.go:1158` states that a purge does this. The notification rows therefore stay, and with the ID they reach no new namespace. A later delete of a namespace must remove them.

## Fix boundary

**IN**

- `pkg/notify/notify.go`, `route.go`, `ports.go`: add `notify.Scope`, a namespace ID and a display name, or the installation scope `@server`. Use it for `Notification.Scope`, `Sink.Scope`, `Subscription.Scope`, `Delivery.Scope`, `CursorMove.Scope` and `ClaimRequest.Scope`. The ID is the key; the name is a label.
- `pkg/notify/route.go`: `ScopeActions`, `Route`, `SendsFor` and `BeforeAttempt` compare the scope ID.
- `pkg/notify/webhook/encode.go` and `pkg/notify/chat/sender.go`: the payload and the chat message use the display name.
- `internal/api/notifications/handlers.go` and `subscriptions.go`: the `namespace` field of the answers stays the display name.
- `pkg/notify/ports.go`: the `Outbox`, `Subscriptions` and `Deliveries` methods that name a scope take the ID.
- `internal/subscriptionsvc/subscriptionsvc.go`: `Create`, `List`, `Get`, `History`, `Redeliver` and `Caller` resolve the namespace ID through `Namespaces.Get` or `WritableHere` and pass it.
- `pkg/store/sqlite/notifications.go` and `pkg/store/postgres/notifications.go`: `inScopedTx`, `checkNotifyFence`, `commonScope`, `Append`, `After`, `Scopes`, `MoveCursor` read, write and compare the ID.
- `pkg/store/sqlite/sinks.go` and `pkg/store/postgres/sinks.go`: sink reads and writes. The sink row scope is the namespace ID; the sink secret item keeps the namespace name for its secret scope.
- `pkg/notify/seal.go`: `SinkSealer.sealValue` and `openValue` use the namespace name of the sink, not the row scope ID, for the secret item.
- `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql`: the notification queries read the display name from the namespace row; regenerate `internal/db` with `make generate`.
- `pkg/store/sqlite/backup_notifications.go`, `backup_notification_sinks.go` and the PostgreSQL counterparts: the backup rows carry the ID.
- `pkg/notify/notifytest/notifytest.go`: the conformance suite.

**OUT**

- The form of the unit filter: an exact unit or collection as an ID, and a pattern in its own field. It needs its own proposal.
- A foreign key from the notification tables to `namespaces`. The namespace delete and purge order is not defined.
- The delete of the rows of a deleted namespace. No namespace delete route exists.

## Check

- **Guard: the scope is the ID.** Test `TestNotificationConformance`, case "a namespace made again gets no notification rows", in `pkg/store/sqlite` and `pkg/store/postgres`. The case creates a namespace, a sink and a subscription, deletes the namespace row with the test hook, and creates a namespace with the same name. It lists the sinks and subscriptions of the new namespace. Mutation: store the namespace name in `scope`; the case finds the old rows.
- **Guard: the route compares the ID.** Test `TestRouteComparesTheScopeID` in `pkg/notify`. It builds two scopes with the same namespace name and different IDs. Mutation: compare names; the subscription of the other ID matches.
- **Guard: the reader receives the ID.** Test `TestCanReadUsesTheScopeID` with the first production `notify.Reader` implementation. It gives a principal of the new namespace a notification of the old ID. Mutation: resolve the scope by name; the answer is true. No implementation exists today, so this test lands with it.
- **Guard: the API resolves the ID.** Test `TestRecreatedNamespaceListsNoSinks` in `internal/api/notifications`. Mutation: pass the path namespace name to the service; the new namespace lists the old sinks.
- **Guard: the answers keep the name.** Test `TestSinkDocumentKeepsTheNamespaceName` in `internal/api/notifications` and `TestWebhookUsesTheNamespaceName` in `pkg/notify/webhook`. Mutation: use the ID in the answer or the payload; the tests fail.

## ADR check

**ADR 145 decisions 1, 9, 12, 14 and 15 (not owner-tagged).** Decision 1: "a scope (a namespace or `@server`)". Decision 9: "each scope has its own sequence." Decision 12: "Each write carries a fence of the scope of its row." Decision 14: "A subscription routes the notifications of its scope that match its filter." Decision 15: the creator check through `notify.Reader`. Changed by ADR 248 (decision 9), ADR 146 (decision 5), ADR 149 (decisions 15 and 24), ADR 197 (decision 5), ADR 203 (decision 5), ADR 204 (decision 5), ADR 210, ADR 234, ADR 241, ADR 262, ADR 315 and ADR 334. The current rule is this ADR with those. The fix keeps the model and changes the stored key from the name to the ID. No conflict.

**ADR 362 decision 1 (owner).** "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only." Changed by ADR 363 (decision 2 only). The fix follows this decision for the notification rows. No conflict.

**ADR 220 decision 1 (owner, title).** "Each row that belongs to a namespace holds its namespace ID." Changed by ADR 273 (decision 12), ADR 286 (words) and ADR 363, which extends decisions 1, 2 and 3. The current rule is this ADR with ADR 273, 286 and 363. The fix implements this decision for the notification tables. It conflicts with the order of ADR 363 decision 1: "RFC 103 step 8 comes before the work of ADR 220 decisions 1, 2 and 3. The design of that work can start before the move ends. Its implementation starts after the move." This fix is part of that work and starts before the move today. The conflict is in "Open questions".

**ADR 42 decision 1 (not owner-tagged).** "The SQL part of a backup is in the `portable` format of this ADR. The server writes no other format, and it has no setting to select one." Changed by ADR 248 and ADR 253 (decision 4). The backup reads the notification rows. The value of `scope` changes; the format and the column list stay. No conflict.

## Open questions

none

Decided by the PM on 2026-11-09 from ADR 363 decision 1 (owner): the work of
ADR 220 decisions 1, 2 and 3 starts after RFC 103 step 8. This change is part
of that work, so it waits for the store move (`openspec/changes/store-move/`,
PR B). It is not implemented before PR B merges.
