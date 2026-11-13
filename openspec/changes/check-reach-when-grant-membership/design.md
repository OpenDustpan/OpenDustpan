## Context

Grant, membership and token issue are act-as paths. The reach check and the write run in one transaction. The approval of the granter at grant time bounds the edge (queue P18). A later widening is flagged, not refused.

## Decisions

1. **Cover check at each new act-as edge.** Compute reach from stored grants with the shared reach function. Compare cover before a grant, a member add or a token issue, in the write transaction. Store the approved reach on the edge.
2. **Transaction rule.** The write transaction uses serializable isolation on PostgreSQL. SQLite serializes writers. On a serialization failure the service retries up to three times, then returns an error that asks the caller to retry.
3. **Flag instead of refuse growth.** Owner decision, 13 Nov 2026 (queue P18). When a group or a principal grows beyond the approved reach, the server keeps the change and sets a flag on the edge by stable ID. The access-review export lists the flag. A covering person acknowledges or revokes it. Each action writes an audit entry.
4. **Flag life cycle.** The flag record holds the stable ID of the edge. When the grant, the principal or the group ends, the same transaction ends the flag and writes an audit entry.
5. **Protected group by write access.** Owner decision, 13 Nov 2026 (queue P21). A group whose reach holds write access to every state is protected. The grant stays and the edge is flagged.

## Structure

An unexported coverProof type with a constructor that returns a value only when the caller reach covers the target reach. The grant, member and token services take this type, so no write path can skip the check. The flag record references the edge by stable ID, never by name.

## Risks

- The reach function can differ between the three paths. One shared function and one proof type prevent it.
- A concurrent grant, member add or revoke can change reach between check and write. The transaction rule prevents it, and a test covers each case.
- The owner decisions are provisional. A different answer changes the flag service, the protect rule and the token issue service, and their tests.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The store checks the proof and the fence in the write transaction, and fails closed on an unknown value. | Write directly through the adapter with a missing proof. Expect refusal and no row. |
| service | The service computes the reach of caller and target, builds the proof, refuses when cover fails, and sets or clears the flag. | Remove the check. The tests for a non-covering caller and for concurrent writes fail. |
| HTTP handler | The handler maps the sentinel to 403 with text that names the target and the missing actions, and checks the action for the flag routes. | Send a refused grant, member add, token create, and an acknowledge without cover over the API. Expect 403. |
