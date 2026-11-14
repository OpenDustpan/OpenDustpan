## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server tests for revoked-delegation-blocks-next-request: request-after-revoke, prior-answer-replay, store-error-fails-closed. Check: Server test TestRequestAfterRevokeFails on main: BLOCKED by the NEW delegation gate on revoked time.
- [ ] 1.2 Add failing tests for no-scheduled-work-starts: scheduler-after-revoke and queued-work-after-revoke. Check: Scheduler test TestSchedulerAfterRevokeFails on main: BLOCKED by the NEW delegation gate on scheduler start.
- [ ] 1.3 Add failing tests for running-work-stops and in-flight-work-freezes: revoke-during-run, takeover-after-revoke, finished-call-stands. Check: Run test TestRevokeDuringRunFails on main: BLOCKED by the NEW stop reason revoked.

## 2. Build

- [ ] 2.1 Resolve the delegation row again at each use and deny when revoked time is set with no allow cache. Check: Agent request under a revoked delegation answers 403 with the NEW error code delegation.revoked and writes the denied entry in the shape of every-step-audited.
- [ ] 2.2 Connect revocation to the run cancel path so live runs end with the NEW stop reason revoked. Check: Revocation cancels live durable claims at the next write boundary with the NEW stop reason revoked and no write after the boundary.
- [ ] 2.3 Resolve the delegation row again at each later step including scheduled starts with the fenced sweeper retry every 30 seconds. Check: Scheduler evaluation after revocation starts nothing and writes collapsed denied audit entries.
- [ ] 2.4 Write each denial after revoke to the audit log in the shape of every-step-audited. Check: GET /api/audit/entries shows the revoke entry and each denied entry with agent, delegation, and reason.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add the e2e step against the real binary over TLS on PostgreSQL once the Dusty run job exists. Check: E2e step is BLOCKED until dusty-proposes-change-order lands the Dusty run job with a test model port; then the step revokes through the route of person-delegates-dusty with an access token and reads GET /api/audit/entries with an access token.
- [ ] 4.2 Write the user docs page in the same pull request. Check: Docs page for revoke effects with the exact verify command, each run as written.
- [ ] 4.3 Run the usability check. Check: Console shows the revoke entry and each denied entry; the persona completes revoke from the docs page alone.
- [ ] 4.4 Pass the full check. Check: `make check` with zero findings.
