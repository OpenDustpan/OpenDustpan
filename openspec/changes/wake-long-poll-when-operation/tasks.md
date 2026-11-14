## 1. Failing tests

- [ ] 1.1 Add failing poll-wake tests for same-kind start, other-kind ignore, rollback no wake, Claim on subscribe, and lost-signal bound. Check: Failing test PollWakesOnSameInstanceStart with poll interval far above 1 s gets work within 1 s; PollIgnoresOtherKind and PollIgnoresRollback keep waiting.
- [ ] 1.2 Add failing poll limiter tests for runner key, isolation between runners, one-poll code, Retry-After: 1, and 5 minute eviction. Check: Failing tests EleventhPollGets429, SecondConcurrentPollGets429 with one-poll code, and IdleLimiterEntryEvicted.
- [ ] 1.3 Add failing session limiter test for the 51st request keyed by session ID. Check: Failing test FiftyFirstSessionRequestGets429 after #970 session paths exist; if #970 is not merged, this test waits for #970.

## 2. Build

- [ ] 2.1 Build the wake hub, publish after commit on both start paths, and wait on signal, 5 s timer, deadline, and request end. Check: `go test -race ./internal/api/runnerapi/... ./pkg/durable/...` passes on SQLite and PostgreSQL with no goleak failure.
- [ ] 2.2 Build the per-runner and per-session limiter with 429 rate-limited and Retry-After: 1. Check: `go test -race ./internal/api/runnerapi/...` passes for poll and session limiters with standard library only and 5 minute eviction.
- [ ] 2.3 Update docs/reference/runner-api.md with the wake and limit behavior. Check: Runner API reference states per-instance count, 10 polls, 50 session requests, Retry-After: 1, 5 minute eviction, and the 5 s fallback.

## 3. Definition of done

- [ ] 3.1 Wire the hub and limiter into the server and run an e2e step with the real runner against one instance. Check: Real runner poll that waits gets claimed work within 1 s of a same-instance start; 11th poll in one second gets 429 rate-limited with Retry-After: 1.
- [ ] 3.2 Publish the docs page update and complete the usability check. Check: Docs page states per-instance count, limits, Retry-After, and the 5 s fallback; usability run from the docs page passes.
- [ ] 3.3 Run the full check with race, SQLite, and PostgreSQL. Check: `make check` reports zero findings.
