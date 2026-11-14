## 1. Failing tests first

- [ ] 1.1 Add failing test for 24 hour durable idempotency retention on job start Check: TestJobStartIdempotencyKept24h fails on main
- [ ] 1.2 Add failing test for stored answer after restart or failover Check: TestJobStartIdempotencySurvivesRestart fails on main
- [ ] 1.3 Add failing test for retry after crash before store Check: TestJobStartCrashBeforeStoreStartsNew fails on main
- [ ] 1.4 Add failing test for non-agent source refused for an agent actor Check: TestJobStartAgentSourceRefused fails on main
- [ ] 1.5 Add failing test for denial record with action, actor, reason, and time Check: TestJobStartDenialNamesRefusedAction fails on main
- [ ] 1.6 Add failing test for poison skip, count, and next-poll retry Check: TestClaimPollSkipsPoisonOperation fails on main
- [ ] 1.7 Add failing test for bound 64 skip count, log, and later-poll processing Check: TestClaimPollCountsBoundSkips fails on main
- [ ] 1.8 Add failing test for bind refusal on phase mismatch Check: TestClaimBindPhaseMismatchRefused fails on main

## 2. Fix

- [ ] 2.1 Keep idempotency answers for 24 hours in the durable store and fix the Route comment Check: go test ./pkg/job/... ./pkg/store/...
- [ ] 2.2 Refuse non-agent source for an agent actor and record the refused action with actor, reason, and time Check: go test ./pkg/job/...
- [ ] 2.3 Skip poison operations with count and retry on next poll, and end job and durable record both as canceled Check: go test ./pkg/store/...
- [ ] 2.4 Count and log operations skipped by the bound of 64, process them on later polls, and mark bindClaim phase check as defense in depth Check: go test ./pkg/store/...

## 3. Definition of done

- [ ] 3.1 Run wiring, route authorization check, e2e job start step, docs page update, and usability check Check: make check with zero findings
- [ ] 3.2 Prove a job start retry, a refused source, and a claim poll with skips Check: make e2e job start and claim path against the real server
