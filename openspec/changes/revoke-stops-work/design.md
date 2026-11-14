## Context

Delegation revocation is a permission cut. The check lives in stored state resolved at each use, not in a cached allow verdict. The revoke route, the revoke action, and the revoked time on the row come from person-delegates-dusty; the agent audit shape comes from every-step-audited; live and scheduled Dusty work comes from dusty-proposes-change-order.

## Decisions

1. **Revocation is stored state checked at each use.** Read revocation from the delegation row of person-delegates-dusty. Every later authorization resolves the row again in the same transaction as the work claim and denies when revoked time is set. The service holds no allow cache. This change adds no route, no action, and no stored field.
2. **Revoke and cancel commit together with a fenced sweeper.** Revocation commits revoked time plus cancel requests on each live run of the delegation in one transaction, then cancels each live durable claim at the next write boundary. The NEW stop reason is revoked. A sweeper owned by the worker component retries a missed cancel every 30 seconds under a 30 second fenced lease claim with fencing per ADR 298 decision 11, so only one instance acts. Scheduler refusals collapse to one denied audit entry per delegation per minute; each live agent request denial writes one entry in the shape of every-step-audited. If the audit append fails during a denial the server still denies with 503 and the NEW error code delegation.revoked and counts the denial in a metric.

## Structure

An unexported delegation gate type with one constructor that refuses a revoked delegation: the constructor reads the delegation row and returns the revoked sentinel when revoked time is set, so no caller builds an allow verdict for a revoked delegation.

## Risks

- A runner keeps a claim across revocation and writes one more step. Mitigation: the claim re-resolves the delegation row at each write boundary and the boundary denies.
- A scheduled turn starts between revocation commit and scheduler read. Mitigation: the scheduler resolves the row again inside the start transaction and refuses the start.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service resolves the delegation row in the same transaction as the claim, holds no allow cache, and denies when revoked time is set or the store errors. | Revoke between claim read and tool write; the write is denied and the denial names the revoked delegation. |
| HTTP handler | Handler checks authorization at the point of use for the delegation object and fails closed on any store error with 403 or 503 and no write. | Send one agent request after revocation with a valid bearer token; the handler answers 403 and writes the denied audit entry. |
| port adapter | Store adapter maps a revoked row to a domain revoked sentinel and maps a store error to a deny sentinel; it stores no verdict. | Send a second request that replays the bytes of a prior allow answer after revocation; the adapter returns revoked and the request is denied. |
