## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add notice cases to NEW file pkg/drift/drifttest/notice.go for first drifted, first clean, repeat quiet, failed, canceled, refused and stale fence Check: TestRecordAppendsDriftedOnce fails on main
- [ ] 1.2 Add states and NEW MarkUnknown conformance in pkg/store/storetest/drift.go Check: TestStatesWriteDriftedInSyncUnknown on sqlite and postgres fails on main

## 2. Build

- [ ] 2.1 Add NEW types unit.drifted warning and unit.drift-cleared info to the single notify.Types catalog in pkg/notify Check: rg unit.drifted shows the two types in pkg/notify
- [ ] 2.2 Append the notice with row lock in Record in pkg/store/internal/driftstore Check: Record appends one notice on both adapters
- [ ] 2.3 Add NEW MarkUnknown in pkg/drift and call it from the state version write path with its fence Check: state write sets unknown in the same fenced transaction
- [ ] 2.4 Add the drift notice rows to the notification guide at NEW docs/guides/notifications-drift.md Check: docs page renders the drift notice rows

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run e2e states step on the drift read route with an access token Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift with an access token shows drifted then in-sync states
- [ ] 4.2 Run e2e delivery step on the subscription deliveries route with an access token Check: deliveries list shows one drifted delivery; BLOCKED by #1172 outbox
- [ ] 4.3 Run usability check from the notification page alone Check: each error names one command from the notification page alone
- [ ] 4.4 Run make check Check: zero findings
