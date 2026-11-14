## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for one-use join token and token-used refusal. Check: TestJoinTokenOneUse fails on main, passes after fix.
- [ ] 1.2 Add failing test for expired and revoked join token refusal with new codes token-expired and token-revoked. Check: TestJoinTokenExpiredRevoked fails on main, passes after fix.
- [ ] 1.3 Add failing test for revoke, list, 1 hour default, 7 day maximum, pool mismatch and bad JoinChallenge. Check: TestJoinTokenRevokeList fails on main, passes after fix.
- [ ] 1.4 Add failing test that TFE agent token create gives JSON:API error naming POST /pools/{pool}/join-tokens. Check: TestTfeAgentTokenRefused fails on main, passes after fix.
- [ ] 1.5 Add failing test for poll claim of a Pulumi job and an OpenTofu job from one pool with concurrent poll race. Check: TestClaimBothTools fails on main, passes after fix.
- [ ] 1.6 Add failing test for queued job with unmatched labels and missing-label output. Check: TestUnmatchedLabelsStayQueued fails on main, passes after fix.
- [ ] 1.7 Add failing test for 60s lease expiry, stale renew refusal, retired live claim and takeover audit entry. Check: TestLeaseExpiryTakeover fails on main, passes after fix.
- [ ] 1.8 Add failing hostile test for replayed and stale Dustpan-Proof on poll. Check: TestProofReplayRefused fails on main, passes after fix.
- [ ] 1.9 Add failing test for renew by a runner that does not own the claim. Check: TestRenewOwnerCheck fails on main, passes after fix.
- [ ] 1.10 Add failing test for must-rejoin on expired key and retired refusal on poll, renew, and session request. Check: TestMustRejoinAndRetired fails on main, passes after fix.
- [ ] 1.11 Add failing test for 15s heartbeat lists, sessionOpen true, canceled true and rotateKey after 25 days. Check: TestHeartbeatCancelRotation fails on main, passes after fix.
- [ ] 1.12 Add failing test for Dustpan-Release mismatch, 4 MiB oversize and wrong-pool key refusal. Check: TestRequestLimits fails on main, passes after fix.
- [ ] 1.13 Add failing test for ordered log read stream with unit:read-logs and resume from chunk number. Check: TestLiveLogReadStream fails on main, passes after fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire routes for pool join tokens under /api/dustpan/v1/namespaces/{namespace}/runner-pools, poll, claim, renew and heartbeat under ANY /runner/v1 with runner-key proof, and live log read; update the runner join guide page with each error command and check each command on the page. Check: `make e2e` runner slice passes on SQLite and PostgreSQL over TLS with a real runner that joins with a join token, polls ANY /runner/v1 with its runner-key proof, claims Pulumi and OpenTofu jobs, takes over after kill, refuses a TFE agent token create, and reads the live log; existing routes ANY /runner/v1 and ANY /api/runners/{}/pools stay reachable.
- [ ] 3.2 Run full check with race, lint and conformance suites. Check: `make check` reports zero findings.
