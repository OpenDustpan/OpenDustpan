## Context

Each notification row keys on the namespace name. A namespace that is deleted and made again under the same name inherits the sinks, subscriptions, notifications, cursors, and deliveries of the old namespace. A namespace ID is stable and a new namespace gets a new one, so a row keyed by the ID never reaches the new namespace.

The unit filter that matches a stack name is a separate bug and stays out. No namespace delete route exists, so no delete of the rows is defined here.

## Decisions

1. **One scope type keyed by the ID.** The NEW type `notify.Scope` holds a namespace ID and a display name, or the installation scope `@server`. Only `notify.NewNamespaceScope(id, name)` and `notify.ServerScope()` make a valid value; the zero value is invalid and fails validation. `Notification.Scope`, `Sink.Scope`, `Subscription.Scope`, `Delivery.Scope`, `CursorMove.Scope`, and `ClaimRequest.Scope` take this type. The ID is the key; the name is a label for people only.
2. **Compare the ID everywhere, show the name everywhere.** `ScopeActions`, `Route`, `SendsFor`, and `BeforeAttempt` compare the scope ID. The webhook payload and the chat message use the display name. The sink and subscription answers keep the `namespace` field as the display name, so the wire shape does not change. The sink secret item keeps the namespace name for its secret scope; the store resolves the name from the namespace row.
3. **Resolve the ID at the service boundary.** The subscriptions service resolves the namespace ID through `Namespaces.Get` or `WritableHere` and passes the ID to the port methods. The store reads, writes, and compares the ID in `inScopedTx` with the fence check in the same transaction. The notification queries read the display name from the namespace row. Backup rows carry the ID.

## Structure

`notify.Scope` with unexported fields and the two constructors of decision 1 is the structure that makes a name-keyed row impossible: a caller cannot build a scope without an ID, and the store never sees a bare name. The zero value fails validation, so a forgotten constructor call is refused.

No custom analyzer and no new tool. The rewrite of the call sites starts from the search in the proposal and moves each group in one commit: domain, service, API answers, store adapters, and backups.

## Risks

- A reader that joins the scope text to a namespace name finds no row. The conformance case with a deleted and remade namespace catches a name comparison.
- A backup from before the change holds names. The restore maps each name through the namespace row or refuses the row with a named error; it never writes a name into the ID column.

## Contract

The production `notify.Reader` (`pkg/dustpan/notify_reader.go`, NEW):

- `type notifyReader struct` (NEW, unexported fields: the principal service and the authorizer).
- `func newNotifyReader(principals *principal.Service, authz identity.Authorizer) (*notifyReader, error)` (NEW): returns an error when either argument is nil.
- `func (r *notifyReader) CanRead(ctx context.Context, id string, n notify.Notification) (bool, error)` (NEW): reads the principal by its stable ID; an unknown or a disabled principal gives `false, nil`; for each action of `notify.ScopeActions(n)` checks the object of the scope ID, plus the unit read action for a unit notification; an authorizer answer that wraps `identity.ErrDenied` gives `false, nil`; any other error gives `false, err`, so the caller sends nothing.

Stored schema: the `scope` column of each notification table (`notification_sinks`, `sinks`, `subscriptions`, `notifications`, `sequences`, `cursors`, `deliveries`) holds the namespace ID, or `@server` for the installation scope. The API answers, the webhook payload, and the chat message hold the namespace name. No new error code: unknown scope and missing row use the existing `not-found` shape.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Resolve the namespace name to its ID before any read or write; refuse an empty ID. | List the sinks of a remade namespace; the new namespace lists none. |
| domain | Compare scope IDs in routing, matching, and claim checks; a name match with a different ID does not match. | Build two scopes with the same name and different IDs; the subscription of the other ID does not match. |
| store | Write, read, and compare the ID column under the fence in the same transaction. | Store a row by name; the conformance case finds the old rows. |
| handler | Pass the path namespace through ID resolution; answer with the display name. | Read a sink of a remade namespace; the answer holds the name and the list holds no old row. |
| reader | Fail closed on authorizer error; deny on unknown or disabled principal. | Give a principal of the new namespace a notification of the old ID; the answer is deny. |
