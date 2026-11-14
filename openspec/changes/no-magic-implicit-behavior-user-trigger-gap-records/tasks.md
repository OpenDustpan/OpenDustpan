## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing read-model test for trigger path and next run time in NEW pkg/trigger/gapread_test.go:1. A person's step. Check: NEW pkg/trigger/gapread_test.go TestTriggerReadShowsPathAndNextRun fails on main, passes after the change.
- [ ] 1.2 Add failing audit test for a skipped schedule slot with server actor and reason in NEW pkg/trigger/gapskip_test.go:1. A person's step. Check: NEW pkg/trigger/gapskip_test.go TestSkippedSlotWritesAuditEntry fails on main, passes after the change.
- [ ] 1.3 Add failing audit test for a replaced stored plan with server actor and reason in NEW pkg/trigger/gapreplace_test.go:1. A person's step. Check: NEW pkg/trigger/gapreplace_test.go TestReplacedPlanWritesAuditEntry fails on main, passes after the change.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire path and next run time into the trigger read path in pkg/trigger:1 using existing route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} with its authorization gate and an access token. A person's step. Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} returns path and next run time with an access token.
- [ ] 3.2 Wire skip and replace audit writes into the scheduler path in NEW pkg/trigger/gaprecord.go:1 using existing route GET /api/audit/entries with its authorization gate and an access token. A person's step. Check: GET /api/audit/entries lists the skip and replace entries with an access token.
- [ ] 3.3 Add trigger gap records docs page in NEW docs/reference/trigger-gaps.md:1 covering path, next run time, and audit reasons. A person's step. Check: Docs page NEW docs/reference/trigger-gaps.md:1 renders the gap record fields.
- [ ] 3.4 Add e2e trigger read step in NEW e2e/triggergaps_test.go:1 calling existing route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with its authorization gate and an access token. A person's step. Check: E2E step reads GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with an access token.
- [ ] 3.5 Run usability check of the trigger gap path from NEW docs/reference/trigger-gaps.md:1 and record where the path still costs time. A person's step. Check: Usability check in NEW docs/reference/trigger-gaps.md:1 completes the namespace administrator path from the docs page alone.
- [ ] 3.6 Run `make check` on pkg/trigger:1 with zero findings. A person's step. Check: `make check` with zero findings.
