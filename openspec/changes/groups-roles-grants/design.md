## Context

Groups, roles and grants are stored grants checked at the point of use with no cached allow. Principals and groups live in the home that the installation setting identity.principalHome sets. The change adds no new grant shape and no new role. This change ships the shared unexported reach function and the refusal codes unit.forbidden and secret.forbidden for reuse by later changes.

## Decisions

1. **Check stored grants on each request.** Reuse the stored grant check at the service for each tool route, so revocation and membership change take effect on the next request with no cached allow.
2. **One shared reach function.** Evaluate every grant, membership and act-as cover through one unexported reach function shipped here, so later changes reuse it and cannot diverge.
3. **Export reads stored grants.** Build the access-review export as a read over stored grants with holder, role, object, creator, time, last use with Never, and history end time, in snapshot and history modes under its own read-access action.

## Structure

The check lives in an unexported granter with constructor newGranter that loads only stored grants by stable ID and calls the shared unexported reach function reachOf; names stay labels for people.

## Risks

- A cached allow lets a revoked grant or ended membership write until the cache ends.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service checks the stored grant for the caller, the action and the object before any state read or write. | Call unit write with a writer grant on another collection and expect 403. |
| port adapter | Store transaction reads the live grant and membership rows at the time of the request with no cached allow. | Revoke the grant or end the membership then retry the same token and expect refusal on the next request. |
| HTTP handler | HTTP adapter maps a refused check to 403 with error code, missing action and object, and writes a refusal audit entry. | Send viewer up and apply calls and export calls without the export action and expect 403 with audit entries. |
