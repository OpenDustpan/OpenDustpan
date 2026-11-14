## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for 24 hour durable idempotency retention on job start Check: TestJobStartIdempotencyKept24h fails on main
- [ ] 1.2 Add failing test for stored answer after restart or failover Check: TestJobStartIdempotencySurvivesRestart fails on main
- [ ] 1.3 Add failing test for retry while the first request still runs Check: TestJobStartIdempotencyInFlightReplays fails on main
- [ ] 1.4 Add failing test for same key with different body refused with 409 job.idempotency-conflict Check: TestJobStartIdempotencyConflict409 fails on main
- [ ] 1.5 Add failing test for retry after 24 hours starting a new job Check: TestJobStartIdempotencyExpiredAfter24h fails on main
- [ ] 1.6 Add failing test for one-transaction write of job row and replay record Check: TestJobStartAtomicStoreNoDuplicate fails on main
- [ ] 1.7 Add failing test for retry after crash before store Check: TestJobStartCrashBeforeStoreStartsNew fails on main
- [ ] 1.8 Add failing test for non-agent source refused for an agent actor Check: TestJobStartAgentSourceRefused fails on main
- [ ] 1.9 Add failing test for denial record with action, actor, reason, and time Check: TestJobStartDenialNamesRefusedAction fails on main
- [ ] 1.10 Add failing test for poison skip, count, and next-poll retry Check: TestClaimPollSkipsPoisonOperation fails on main
- [ ] 1.11 Add failing test for bound 64 skip count, log, and later-poll processing Check: TestClaimPollCountsBoundSkips fails on main
- [ ] 1.12 Add failing test for cancel after change began ending job and durable record as canceled Check: TestJobCancelAfterChangeBeganEndsBothCanceled fails on main
- [ ] 1.13 Add failing check for the defense-in-depth comment on the bindClaim phase check Check: rg defense in depth pkg/store/internal/jobstore/apply.go fails on main

## 2. Fix

- [ ] 2.1 Keep idempotency answers for 24 hours in the durable store with one-transaction write and fix the Route comment Check: go test ./pkg/job/... ./pkg/store/...
- [ ] 2.2 Refuse non-agent source for an agent actor and record the refused action with actor, reason, and time Check: go test ./pkg/job/...
- [ ] 2.3 Skip poison operations with count, backoff, alert, and next-poll retry, and end job and durable record both as canceled Check: go test ./pkg/store/...
- [ ] 2.4 Count and log operations skipped by the bound of 64, process them on later polls in poll order, and mark bindClaim phase check as defense in depth Check: go test ./pkg/store/...

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Prove wiring and route authorization on POST /api/dustpan/v1/namespaces/{namespace}/jobs with an access token Check: `curl -s -H 'Authorization: Bearer <token>' -H 'Idempotency-Key: <key>' -H 'Content-Type: application/json' -d '{"operation":"plan"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` returns the stored answer, plus `make check` with zero findings
- [ ] 4.2 Prove a job start retry, a refused source, and a claim poll with skips from the docs page alone Check: `curl -s -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' -d '{"operation":"plan","source":"api"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` proves a refused source and a claim poll with skips
- [ ] 4.3 Update the docs page for the job start retry, denial record, cancel state, and claim skip behavior Check: docs page for job start and claim updated in the same pull request
