## Context

Server side of runner pools: join tokens, join, poll and claim for jobs of both tools, lease expiry with takeover, live log read stream. Runner binary lives in its own repository and is out of scope.

## Decisions

1. **One-use join in one transaction.** Join uses stored SHA-256 of token, one-use consume in the same transaction as runner insert, keyed by pool ID and runner name with stable runner ID after join.
2. **Claim with lease and takeover.** Poll maps to job.Claimer.Claim for the pool; unmatched labels return no work and the job stays queued; lease expiry returns the claim to the queue for another runner. Base poll holds up to 50 seconds with 5 second recheck; cross-instance wake is the wake-long-poll change.

## Structure

Unexported claimLease type with constructor newClaimLease that checks pool, owner, expiry and monotonic renew; only the constructor builds a live lease.

## Risks

- Token replay without nonce cache gives double join.
- Lease renewal without owner check lets one runner hold the claim of another runner.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP adapter | Handler checks Dustpan-Proof signature, time window, nonce cache, and Dustpan-Release header before any state read. | Replay a poll proof and a stale proof; both get bad-proof and no claim. |
| service | Service checks join-token hash, expiry, prior use, and pool match; checks claim owner on renew; checks runner live on each claim use. | Join twice with one token; second join gets token-used. Renew a claim from another runner; request is refused. |
| port adapter | Store consumes token and inserts runner in one transaction; claim insert uses conditional write on lease owner and expiry. | Run two concurrent joins with one token; one wins and the other gets token-used. |
