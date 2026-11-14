## Context

Grant, membership and token issue are act-as paths. The reach check and the write run in one transaction. A later widening is flagged, not refused.

## Decisions

1. **Cover check at each new act-as edge.** Compute reach with the shared reach function. Compare cover before a grant, a member add or a token issue, in the write transaction. Store the approved reach as a sorted set of action and object pairs.
2. **Transaction rule.** PostgreSQL uses serializable isolation. SQLite serializes writers. On a serialization failure the service retries up to 3 times with no delay. After the third failure it returns 409 and asks the caller to retry. Both adapters run the same conformance test.
3. **Flag instead of refuse growth.** Owner decision, 13 Nov 2026 (queue P18). Each write that widens an edge sets a flag by the stable ID of the edge in the same transaction. A covering person acknowledges or revokes it. Each action writes an audit entry.
4. **Flag life cycle.** The flag holds the stable ID of the edge. When the grant, principal, group or membership ends, the same transaction ends the flag and writes an audit entry.
5. **Protected group.** Owner decision, 13 Nov 2026 (queue P21). A group with write access to every state is protected. A grant that protects a group with identity provider members needs a trust decision in the same request.

## Structure

An unexported coverProof type with a constructor that returns a value only when the caller reach covers the target reach. The grant, member and token services take this type, so no write path skips the check. The flag references the edge by stable ID.

## Risks

- The reach function can differ between paths. One shared function and one proof type prevent it.
- A concurrent write can change reach between check and write. The transaction rule prevents it and a test covers each case.
- The owner decisions are provisional. A different answer changes the flag, protect and token issue services and their tests.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The store checks the proof and the fence in the write transaction and fails closed. | Write through the adapter with a missing proof. Expect refusal and no row. |
| service | The service builds the proof, refuses when cover fails, and sets or clears the flag. | Remove the check. The tests for a non-covering caller and for concurrent writes fail. |
| HTTP handler | The handler maps the sentinels to 403 or 409 and checks the action of the flag routes. | Send a refused grant, member add, token create and an acknowledge without cover. Expect 403. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Latency of the grant check | added time of at most 50 | milliseconds at the 95th percentile | one grant or member add on a principal with at most 100 grants |
| Serialization retries | at most 3 | retries | one write call of grant, member add or token issue |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
  subgraph Client[Untrusted client]
    C[Caller with access token]
  end
  subgraph Server[Server process]
    H[HTTP handler]
    S[Service with cover proof]
  end
  subgraph Store[Database]
    D[(Grants, memberships, flags, audit)]
  end
  C --> H
  H --> S
  S --> D
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Untrusted client | E | A caller with a small reach gives a token right to a target with a larger reach and gains its reach. | boundary check | The handler validates the ID of the target and the trust decision field with an allowlist. | Send a grant with a malformed target ID. Expect 400 and no row. |
| Untrusted client | E | A caller with a small reach gives a token right to a target with a larger reach and gains its reach. | encode or contain | The store uses placeholders only and writes the approved reach as a sorted set of typed pairs. | Send a target ID with SQL text. Expect 400 and no row change. |
| Untrusted client | E | A caller with a small reach gives a token right to a target with a larger reach and gains its reach. | limit | The request body has a size limit and the approved reach has a bound on its pairs. | Send an oversized grant body. Expect 413 and no row. |
| Untrusted client | E | A caller with a small reach gives a token right to a target with a larger reach and gains its reach. | authorize at point of use | The service builds the cover proof for the target at write time. | Send a grant from a non-covering caller. Expect 403 and no grant. |
| Untrusted client | E | A caller with a small reach gives a token right to a target with a larger reach and gains its reach. | fail closed | The store refuses a write with no proof and writes an audit entry. | Write through the adapter with no proof. Expect refusal and an audit entry. |
| Server process | T | A concurrent write changes reach between the check and the write. | boundary check | The service rejects a grant whose target or role is not in the current catalog before it opens the transaction. | Send a grant with an unknown role. Expect refusal before any write. |
| Server process | T | A concurrent write changes reach between the check and the write. | encode or contain | The check reads and the write use the same transaction handle, so no read leaves the transaction. | Run a grant and a revoke at the same time. Expect the check to see the revoke or the grant to fail. |
| Server process | T | A concurrent write changes reach between the check and the write. | limit | The check and the write run in one serializable transaction with at most 3 retries. | Run a concurrent grant and revoke on both adapters. Expect a refusal of the grant without cover. |
| Server process | T | A concurrent write changes reach between the check and the write. | authorize at point of use | The service computes cover again inside the transaction, not from a value that the handler read. | Remove cover between the handler read and the write. Expect 403. |
| Server process | T | A concurrent write changes reach between the check and the write. | fail closed | After the third serialization failure the service returns 409 and stores nothing. | Force 4 failures. Expect 409 and no row. |
| Database | R | An acknowledge or a revoke of a flag leaves no record. | boundary check | The handler accepts only a flag ID of the known form. | Acknowledge with a malformed ID. Expect 400 and no audit gap. |
| Database | R | An acknowledge or a revoke of a flag leaves no record. | encode or contain | The audit entry text removes CR and LF from names before it writes them. | Use a group name with a newline. Expect one audit entry with no line break. |
| Database | R | An acknowledge or a revoke of a flag leaves no record. | limit | The acknowledge route accepts one flag for each call. | Send a list of flag IDs. Expect 400 and no change. |
| Database | R | An acknowledge or a revoke of a flag leaves no record. | authorize at point of use | The service checks cover of the caller for the edge before the flag change. | Acknowledge without cover. Expect 403 and no change. |
| Database | R | An acknowledge or a revoke of a flag leaves no record. | fail closed | The flag change and its audit entry share one transaction. | Fail the audit write. Expect the flag to stay and the call to fail. |
| Untrusted client | I | A caller reads flagged edges that it cannot cover. | boundary check | The handler validates the filter and page parameters with an allowlist. | Send an unknown filter. Expect 400. |
| Untrusted client | I | A caller reads flagged edges that it cannot cover. | encode or contain | The list query uses placeholders for the filter values. | Send a filter with SQL text. Expect 400 or an empty list. |
| Untrusted client | I | A caller reads flagged edges that it cannot cover. | limit | The list has a maximum page size. | Ask for a page of 100000. Expect the maximum page. |
| Untrusted client | I | A caller reads flagged edges that it cannot cover. | authorize at point of use | The list filters each edge by the reach of the caller. | List flags as a non-covering caller. Expect none of the other edges. |
| Untrusted client | I | A caller reads flagged edges that it cannot cover. | fail closed | If the reach of the caller cannot be computed, the list returns 403 and no edge. | Fail the reach function. Expect 403 and an empty body. |
