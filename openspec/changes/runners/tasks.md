## 1. Failing tests first

- [ ] 1.1 Add failing test for one-use join token and token-used refusal. Check: TestJoinTokenOneUse fails on main, passes after fix.
- [ ] 1.2 Add failing test for expired and revoked join token refusal. Check: TestJoinTokenExpiredRevoked fails on main, passes after fix.
- [ ] 1.3 Add failing test for poll claim of a Pulumi job and an OpenTofu job from one pool. Check: TestClaimBothTools fails on main, passes after fix.
- [ ] 1.4 Add failing test for queued job with unmatched labels and missing-label output. Check: TestUnmatchedLabelsStayQueued fails on main, passes after fix.
- [ ] 1.5 Add failing test for lease expiry, second expiry, stale renew refusal, and retired live claim. Check: TestLeaseExpiryTakeover fails on main, passes after fix.
- [ ] 1.6 Add failing hostile test for replayed and stale Dustpan-Proof on poll. Check: TestProofReplayRefused fails on main, passes after fix.
- [ ] 1.7 Add failing test for renew by a runner that does not own the claim. Check: TestRenewOwnerCheck fails on main, passes after fix.
- [ ] 1.8 Add failing test for must-rejoin on expired key and retired refusal on poll, renew, and session request. Check: TestMustRejoinAndRetired fails on main, passes after fix.
- [ ] 1.9 Add failing test for 50 second poll hold with 5 second recheck and empty close. Check: TestBaseLongPollWait fails on main, passes after fix.
- [ ] 1.10 Add failing test for ordered log read stream, resume from chunk number, and close on end or cancel. Check: TestLiveLogReadStream fails on main, passes after fix.

## 2. Definition of done

- [ ] 2.1 Wire routes for pool join tokens under /api/dustpan/v1/namespaces/{namespace}/runner-pools, poll, claim, renew, heartbeat, and live log read; update the runner join guide page with each error command and check each command on the page. Check: `make e2e` runner slice passes on SQLite and PostgreSQL over TLS with a real runner.
- [ ] 2.2 Run full check with race, lint and conformance suites. Check: `make check` reports zero findings.
