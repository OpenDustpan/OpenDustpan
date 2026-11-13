## Context

Mode 2 recovery for an installation with no working administrator. Offline operator key signs a one-time recovery request; server checks signature, refuses reuse, issues one admin session, writes audit entry, and opens a break-glass review only when the review opt-in is on.

## Decisions

1. **Mode choice is immutable after first boot.** Store the recovery-mode choice at first boot and refuse changes after data exists; the write carries no fence because no namespace exists yet.
2. **Single-use requests by stored request ID.** Hold pending and used recovery request IDs in one store with compare-and-set on use; second use of the same request fails even under retry, restart, or concurrent submit on two nodes.
3. **One transaction for use and review notice.** Commit session issue, session-generation bump of the recovery administrator, audit entry with mode time and source, and break-glass.used notification in one transaction on success; a crash before commit leaves the request unused, a crash after commit leaves one session and one used ID; refusals commit only a refused-use audit entry and open no review.
4. **Bounded refused-use writes that never block valid recovery.** Throttle refused recovery submits by client address with a bounded refusal log, but a request with a valid signature and unused ID always reaches the service; the throttle never refuses a valid recovery, so shared addresses and floods cannot deny recovery.
5. **Review opt-in on for verification and shared session-limit family.** The e2e step and the docs page turn breakGlassReview.enabled on before the mode 2 use and show its effective value in the API, so the step-11 check sees one opened review without changing the ADR 142 opt-in rule; the recovery session lifetime uses the same settings key family as break-glass-sign-in-typed.

## Structure

Unexported breakglass recovery service with constructor that loads pinned public key and recovery mode; only its Redeem method can mark a request ID used and issue the admin session.

## Risks

- Offline key loss leaves mode 2 unusable until restore from backup.
- Clock skew rejects fresh requests or accepts stale ones without tight expiry and skew bound.
- With the review opt-in off, a stolen offline key gives a short session with only an audit entry and no review; the docs page warns of this and tells the operator to turn the review on.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP adapter | Handler checks mode is signed-request, request shape is valid, expiry is in the future, issued-at is not in the future past skew, and request ID has the allowed form, plus per-address rate limit on refused submits that a valid signature bypasses. | Send expired request, future issued-at, oversize fields, and path traversal in key ID directly to handler with valid signature bypassed, plus a flood of bad signatures from one address followed by one valid request from the same address that must still succeed. |
| Service | Service checks Ed25519 signature against pinned offline public key, checks audience and installation ID match, and consumes request ID in one transaction. | Replay a used request ID, submit a request signed by another key or for another installation, and submit the same ID twice at once directly to service. |
| Store | Store enforces unique request ID and immutable recovery mode; session issue, generation bump, audit entry, and review notification commit in the same transaction. | Insert duplicate request ID, attempt mode change after first boot, and restart after consume-before-commit directly against store adapter. |
