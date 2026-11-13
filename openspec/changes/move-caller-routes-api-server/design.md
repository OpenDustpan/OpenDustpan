## Context

The caller routes serve the signed-in principal only. The move changes paths, paging names, and idempotency coverage with no other behavior change.

## Decisions

1. **Move paths, keep service.** Keep the existing caller service and authorizer checks unchanged and change only the HTTP route table and API description labels. Paging uses the shared page token helper so cursor text never reaches the store. Each mutating token route uses the shared idempotency store keyed by principal, method, path, and key, with one atomic database transaction per state change.
2. **Auth before replay, no stored secrets.** Check authentication before the idempotency lookup on each /me route and fail closed. A replay that presents an ended or missing token gets 401 and never reaches the stored answer. The stored record never holds a token secret. The first create or rotate answer carries the secret value once. A replay with a valid token returns the stored status with token metadata and no secret value.
3. **Caller-scoped authorization.** Declare one authorization action with object caller principal on each /me route. The check passes only for the authenticated caller and, for item routes, only when the item belongs to that caller. No stored grant is checked because the object is the caller itself.
4. **Keep metrics label.** Keep the metrics adapter label server for /api/dustpan/v1/me/ routes. The label is not a path and the path rule forbids the word server only in paths.

## Structure

No new unexported type. The existing mountV1 constructor in pkg/dustpan registers handler paths under /api/dustpan/v1; the route test plus the not-moved list check fail when an old path stays served or a new path is missing.

## Risks

- Console or script still calls an old path and gets 404.
- Paging rename breaks a console list that still sends cursor and limit.
- A client that loses the first create answer holds a token it cannot read again.
