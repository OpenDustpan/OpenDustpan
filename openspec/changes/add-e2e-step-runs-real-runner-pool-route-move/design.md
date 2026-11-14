## Context

Runner management moves to resource paths under one namespace. The runner protocol under /runner/v1 does not move. This part carries no launcher and no live runner run.

## Decisions

1. **Move management to resource paths.** Serve pools and join tokens at GET, POST, PATCH and DELETE under /api/dustpan/v1/namespaces/{namespace}/runner-pools. Keep the actions namespace:read-runners and namespace:manage-runners and the audit events of ADR 138.
2. **No alias on old pool paths.** Return the old pool paths as not found with the error code of the unknown route. The server writes no redirect and no compatibility shim. The rule covers only the pool and join-token paths of this change.
3. **Stable IDs for pools and tokens.** Store each pool and each join token by its stable ID. A name is a label for people only. A pool removed and made again gets a new ID and no history of the old pool.

## Structure

Unchanged domain types in pkg/runner with a thin HTTP adapter over the runner service; no new tool and no custom analyzer.

## Risks

- Callers of the old pool paths get not found and must change to the new paths.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Each management route declares its action and object, and the policy check refuses a caller without the grant. | Call each management route with a token without the grant and check 403, plus a route test with no sign-in on the public list only. |
| service | The service checks the grant for the namespace or pool object at the point of use and writes the audit entry in the same transaction as the change. | Call the service with a denied grant and check that no pool, token, drain or retire change persists and no audit entry is stored. |
| port adapter | The store writes pool rows with the namespace fence in the same transaction and returns domain sentinels that the adapter maps. | Attempt a cross-namespace pool write and check refusal with no row change. |
