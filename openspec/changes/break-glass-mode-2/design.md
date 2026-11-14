## Context

Mode 2 recovery for an installation with no working administrator. The server checks the signature of an offline-signed one-time request, refuses reuse, issues one session and writes an audit entry. The review waits for the RFC.

## Decisions

1. **Mode choice is immutable after first boot.** The store refuses a change of the recovery mode after data exists. A start in signed-request mode with no pinned key serves no recovery.
2. **Single-use requests by stored request ID.** One store holds used IDs with compare-and-set on use. A second use fails under retry, restart or concurrent submit. Used IDs are purged at expiry plus 7 days.
3. **One transaction for use.** Session issue, generation bump and audit entry commit in one transaction. A crash before commit leaves the request unused.
4. **Bounded pre-check cost.** An 8 KB body limit and a per-address refusal throttle with a bounded log. A valid signature bypasses the throttle.

## Structure

Unexported recovery service with a constructor that loads the pinned public key and the mode. Only its Redeem method marks a request ID used and issues the session.

## Risks

- Loss of the offline key leaves mode 2 unusable until a restore.
- Clock skew rejects fresh requests.
- A stolen offline key gives a short session; audit makes the use visible.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP adapter | Checks mode, 8 KB body limit, shape, expiry, skew and ID form. | Send expired, future-dated, oversize and malformed requests directly to the handler. |
| Service | Checks the Ed25519 signature, audience and installation ID, and consumes the ID once. | Replay an ID, use another key, and submit the same ID twice at once to the service. |
| Store | Unique request ID and immutable mode. | Insert a duplicate ID and change the mode after first boot against the store. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| security | 1 session for each valid request ID; a replay gives 409 with 0 sessions | sessions | recovery redeem |
| reliability | 0 sessions from a request that crashed before commit, 0 second sessions after commit | sessions | recovery redeem, one server restart |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph offline[Offline operator]
 K[Offline key]
 end
 subgraph net[Network boundary]
 R[Recovery request]
 end
 subgraph srv[Server]
 H[Handler] --> S[Service] --> D[(Store)]
 end
 K --> R --> H
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Network boundary | S | An attacker forges a recovery request. | boundary check | The service checks the Ed25519 signature against the pinned key. | Submit a request signed by another key to the service. |
| Network boundary | S | An attacker forges a recovery request. | encode or contain | The handler decodes the body as strict JSON with fixed fields and a bounded ID form; it never builds paths or queries from it. | Send a request with unknown fields and a SQL-like ID directly to the handler and expect 400 and no store call. |
| Network boundary | S | An attacker forges a recovery request. | limit | 8 KB body limit and the per-address refusal throttle. | Send a 1 MB body and expect refusal before parsing. |
| Network boundary | S | An attacker forges a recovery request. | authorize at point of use | The service checks the pinned key, mode, audience and installation ID at redeem, not only at the handler. | Call the service directly with a request for another installation ID and expect refusal. |
| Network boundary | S | An attacker forges a recovery request. | fail closed | Any unknown value gives 401 and an audit entry. | Submit a malformed request and check for 401 and one audit entry. |
| Network boundary | T | An attacker replays a used request. | boundary check | The handler checks the request ID form and the expiry before the service call. | Send an empty and an over-long ID to the handler and expect 400. |
| Network boundary | T | An attacker replays a used request. | encode or contain | The store uses placeholders only and a unique key on the request ID. | Submit an ID with quote characters to the store and expect a plain miss or one row. |
| Network boundary | T | An attacker replays a used request. | limit | Used IDs live until expiry plus 7 days, then the purge removes them. | Run the purge and check that a row inside the window stays. |
| Network boundary | T | An attacker replays a used request. | authorize at point of use | The store consumes the ID once with compare-and-set. | Submit the same ID twice at once to the store. |
| Network boundary | T | An attacker replays a used request. | fail closed | A store error on consume gives 503 and no session. | Inject a store fault and expect no session and an audit entry. |
| Server | D | A flood of bad requests exhausts the server. | boundary check | The handler refuses a wrong mode and a wrong content type before any signature work. | Send a wrong content type and expect refusal with no signature check. |
| Server | D | A flood of bad requests exhausts the server. | encode or contain | The refusal log is a bounded ring keyed by client address; no request text is stored. | Send 100000 refusals and check the log size stays at the bound. |
| Server | D | A flood of bad requests exhausts the server. | limit | 8 KB body limit and a per-address refusal throttle; a valid signature bypasses it. | Send a flood of bad signatures, then one valid request from the same address. |
| Server | D | A flood of bad requests exhausts the server. | authorize at point of use | The service checks the signature again for each request that passes the throttle. | Call the service directly after the throttle passes a forged request and expect refusal. |
| Server | D | A flood of bad requests exhausts the server. | fail closed | When the throttle state is unavailable, the handler refuses. | Break the throttle state and expect 503. |
| Server | E | The recovery session gains more rights than recovery. | boundary check | The session carries the recovery administrator subject only. | Decode the issued cookie and check that it names one subject. |
| Server | E | The recovery session gains more rights than recovery. | encode or contain | The session is a sealed cookie bound to the session generation. | Alter the cookie bytes and expect 401. |
| Server | E | The recovery session gains more rights than recovery. | limit | The session lifetime uses the freshAuthSeconds window. | Use the session after the window and expect 401. |
| Server | E | The recovery session gains more rights than recovery. | authorize at point of use | Each route checks the action for the object against the stored grants of the recovery administrator. | Call a route without a grant with the session and expect 403. |
| Server | E | The recovery session gains more rights than recovery. | fail closed | A stale generation gives 401 and an audit entry. | Use a cookie from before a second recovery and expect 401. |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Redeem | Crash before commit | Submit the same request ID again. | No session, no used ID. |
| Redeem | Concurrent duplicate submit | The loser makes a new request ID. | One session and one used ID. |
