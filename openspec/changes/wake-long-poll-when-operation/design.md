## Context

Poll waits in internal/api/runnerapi against job.Claimer.Claim. Starts commit in pkg/durable and in job dispatch. Rate checks guard POST /runner/v1/poll per runner ID and session paths per session ID.

## Decisions

1. **Instance-local wake hub with Claim on subscribe.** Add an instance-local hub in the server process that maps each durable kind to waiting polls. Start paths publish the kind only after commit. Each poll subscribes for its pool kinds, runs Claim at once on subscribe, then retries on signal, on the 5 s tick for operations that other instances start, or on the 50 s deadline. The at-once Claim closes the subscribe and publish race and the 5 s tick bounds a lost signal.
2. **Per-instance rate limiter after authentication.** Add a fixed-window limiter with the standard library only, keyed by authenticated runner ID on poll and by session ID on session paths. Order each request as proof and authentication, then limiter, then the one-poll rule and Claim or session work. Evict keys idle for 5 minutes. Answer 429 rate-limited with Retry-After: 1. A limiter refusal creates no open poll and closes no open poll.

## Structure

Unexported type wakeHub with constructor newWakeHub and methods subscribe(kinds) and publish(kind after commit). Only durable commit paths hold the publisher. Unexported type limiter with constructor newLimiter and method allow(key) with mutex-guarded map. No new tool and no custom analyzer.

## Risks

- Hub publish before commit wakes a poll that finds no work.
- Limiter table grows without bound without eviction.
- Fake clock tests hide the subscribe and publish race.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Port adapter | Proof and authentication run first; the limiter counts only authenticated runner IDs and session IDs. | Flood poll with bad proofs; the server answers 401 and holds no limiter entry for unknown IDs. |
| HTTP handler | Limiter runs after authentication and before Claim and session work and answers 429 rate-limited with Retry-After: 1. | Send 11 polls in one second from one runner and 51 requests in one second from one session; each excess gets 429 rate-limited and the other runner still gets 200 or 204. |
| Service | One-poll-at-a-time rule stays independent under the rate: a second concurrent poll gets 429 with the existing one-poll code. | Open two concurrent polls of one runner under the rate; the second gets 429 without code rate-limited and the first keeps waiting. |
