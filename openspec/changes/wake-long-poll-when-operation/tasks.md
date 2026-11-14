## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing poll-wake tests for same-kind start, other-kind ignore, rollback no wake, Claim on subscribe, thundering herd, and lost-signal bound. Check: Failing test PollWakesOnSameInstanceStart with poll interval far above 1 s gets work within 1 s; PollIgnoresOtherKind and PollIgnoresRollback keep waiting.
- [ ] 1.2 Add failing poll limiter tests for runner key, isolation between runners, one-poll code, nonce release, Retry-After: 1, fixed-window edge with fake clock, and 5 minute eviction. Check: Failing tests EleventhPollGets429, SecondConcurrentPollGets429 with one-poll code, LimitedPollKeepsNonce, and IdleLimiterEntryEvicted.
- [ ] 1.3 Add failing session limiter test for the 51st request keyed by session ID with launcher backoff. Check: Failing test FiftyFirstSessionRequestGets429 after session-paths change merges; if that change is not merged, this test waits for it.

## 2. Build

- [ ] 2.1 Build the wake hub, publish after commit on both start paths, and wait on signal, 5 s timer, deadline, and request end with unsubscribe. Check: `go test -race ./internal/api/runnerapi/... ./pkg/durable/...` passes on SQLite and PostgreSQL with no goleak failure.
- [ ] 2.2 Build the per-runner limiter and, after the session paths exist, the per-session limiter with 429 rate-limited and Retry-After: 1. Check: `go test -race ./internal/api/runnerapi/...` passes for poll limiter with standard library only and 5 minute eviction; session limiter lands with the session-paths change.
- [ ] 2.3 Update docs/reference/runner-api.md with the wake and limit behavior. Check: Runner API reference states per-instance count, 10 polls, 50 session requests, Retry-After: 1, 5 minute eviction, and the 5 s fallback.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the hub and limiter into the server on ANY /runner/v1 with runner-key and job-session credentials and run an e2e step with the real runner against one instance. Check: Real runner poll on ANY /runner/v1 with runner-key credential that waits gets claimed work within 1 s of a same-instance start; 11th poll in one second gets 429 rate-limited with Retry-After: 1; session-limit e2e runs after the session-paths change merges.
- [ ] 4.2 Publish the docs page update and complete the usability check. Check: Docs page states per-instance count, limits, Retry-After, and the 5 s fallback; usability run from the docs page passes.
- [ ] 4.3 Run the full check with race, SQLite, and PostgreSQL. Check: `make check` reports zero findings.
