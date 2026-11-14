## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Write failing conformance cases for Trigger Paths in `pkg/trigger/readcheck_test.go` (existing test file of package pkg/trigger). Check: The Paths conformance cases fail on main.
- [ ] 1.2 Write failing dispatcher tests for match, paths, fork refusal and replacement in `pkg/trigger/dispatcher_reviewrequest_test.go` (NEW). BLOCKED by the Forge port event, which feeds the dispatcher. Check: The dispatcher tests fail on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. definition of done

- [ ] 3.1 Implement Paths, Dispatcher.ReviewRequest, source kind forge and ordered replacement. BLOCKED by the Forge port event for the wiring. Check: The unit and conformance tests pass.
- [ ] 3.2 Keep the authorization actions of the trigger routes `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` with an access token. Check: The route test passes.
- [ ] 3.3 Add an e2e step on a fake forge in the NEW file e2e/start_plan_on_push_review_test.go. The step sends a push to a review request on the fake forge through the push intake route `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` setup with an access token, then reads the plan job. BLOCKED by git-host-webhook-intake-post. Check: The e2e step shows a plan, no plan, a fork audit entry and a replaced plan. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.4 Write the NEW docs page docs/guides/push-plans.md about push plans and the fork rule, run the usability check and run make check (the existing Make target in Makefile:14). Check: make check reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
