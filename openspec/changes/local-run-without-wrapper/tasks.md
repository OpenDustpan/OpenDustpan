## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing e2e test for plain tofu apply taking the whole-unit lock at the lock call, through the TFE-compatible route /api/v2/workspaces/{}/actions/lock with an access token. Check: TestPlainApplyTakesWholeUnitLock fails on main, passes after fix.
- [ ] 1.2 Add failing e2e test for a runner job queued behind a plain holder and started after release. BLOCKED by strict-case-takes-turns, which builds the FIFO whole-unit lock queue; the plain lock uses the TFE-compatible route /api/v2/workspaces/{}/actions/lock with an access token. Check: TestRunnerJobWaitsForPlainHolder fails on main, passes after fix.
- [ ] 1.3 Add failing test for holder principal, interval, end cause, and reason on workspace row and history. Check: TestHistoryNamesPlainHolder fails on main, passes after fix.
- [ ] 1.4 Add failing test for a plain lock refused while a runner claim is active. BLOCKED by two-disjoint-claims, which adds the claim record. Check: TestPlainLockRefusedDuringClaim fails on main, passes after fix.
- [ ] 1.5 Add failing test for expiry and force-unlock with unit:manage, fence version, and audit entry, through /api/v2/workspaces/{}/actions/force-unlock with an access token. Check: TestPlainLockReleaseOnDisconnect fails on main, passes after fix.
- [ ] 1.6 Add failing test for FIFO order of waiting runner jobs behind a plain holder. Check: TestPlainFifoQueueOrder fails on main, passes after fix.

## 2. Implementation

- [ ] 2.1 Implement strict lock path at the OpenTofu lock call for requests with no associated claim. Check: Lock with no associated claim returns the lock with reason local-without-wrapper, and a runner job queues behind the holder in FIFO order.
- [ ] 2.2 Implement refusal of a plain lock while a runner claim is active. Check: Plain lock during active claim is refused with the claim identity.
- [ ] 2.3 Implement expiry reuse and operator force-unlock with fence and audit. Check: Expired or force-unlocked plain lock writes fence version and audit entry with unit:manage, and switch check only for client-encrypted state; queued job starts.
- [ ] 2.4 Record plain holder on workspace row, history, wait record, and audit. Check: Workspace read shows live holder and reason, wait record names the holder, and history shows holder principal, interval, end cause, and reason.

## 3. Definition of done

- [ ] 3.1 Wire route with authorization actions and audit entries. Check: TFE-compatible lock route checks run-plan or run-apply on the workspace object and upload checks run-apply; route test passes.
- [ ] 3.2 Add e2e step against real OpenTofu and fake cloud provider, using the route /api/v2/workspaces/{}/actions/lock with an access token and a runner job through /runner/v1 with a job token. BLOCKED by strict-case-takes-turns for the queue. Check: e2e step replays plain tofu apply during a running runner apply and checks provider log order on Linux with PostgreSQL.
- [ ] 3.3 Write guide section for plain run lock behavior. Check: Docs page runs as written from a developer laptop.
- [ ] 3.4 Run usability check of user path. Check: Developer completes plain apply and reads holder from docs page alone; each error names one command.
- [ ] 3.5 Run full check suite. Check: `make check` reports zero findings.
