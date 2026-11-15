## Context

One workspace takes runner claims and one wrapped local claim. The wrapper reuses the claim register, base-state serve, and per-instance merge of two-disjoint-claims and concurrent-applies-merge-core. This change adds only the wait, the retry, the timeout enforcement, and the replan on the new base.

## Decisions

1. **Reuse claim path.** The wrapper reuses the claim register, base-state serve, and per-instance merge of the other changes, with no new rule for history, expiry, authorization, or fence.
2. **No provider write without an accepted claim.** The wrapper starts no provider call before its claim is accepted. After a wait it discards the old plan and makes a new plan on the new base before any provider write. When the new register attempt is refused, the wrapper re-enters the wait loop.
3. **Values from ADR 368.** The wrapper retries with backoff from 1 to 30 seconds with full jitter and obeys `Retry-After`. It waits 30 minutes by default; `--claim-timeout` takes 0 to 6 hours. Its apply claim has a lease of 90 seconds, renewed every 30 seconds. The wrapper acts as the signed-in person and gives `tofu` a launcher token bound to its apply claim, through the credentials helper and never in a variable (ADR 369 decisions 1 and 3).

## Structure

An unexported readyToApply type inside the wrapper process holds the accepted claim identifier and the base state version identifier. Only the register-success constructor makes a readyToApply value, and the apply start function takes a readyToApply value, so a start without accept does not compile.

## Risks

- A stale plan applied on an old base loses the change of the blocker.
- Waiting wrappers in lockstep overload the server without backoff with jitter.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Handler returns 409 with blocking claim identifier and retry hint on overlap, and 403 with the missing action on denied authorization. | Send an overlapping local register; it gets 409 with the blocking claim identifier and makes no provider call. |
| service | Service refuses overlap before any provider call and refuses an upload from an expired claim or an old base. | Register an overlapping local claim during a running claim; it is refused and a disjoint local claim is accepted. |
| CLI wrapper | Wrapper waits with backoff with jitter, honors the retry hint and 503 Retry-After, enforces the timeout, and replans on the new base. | Hold a blocker past the timeout and return 503 with Retry-After during wait; the wrapper exits with no provider call on timeout. |
