## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Add failing conformance cases for Trigger Paths. Check: Conformance cases for NewTrigger Paths default, normalization and root marker fail on main.
- [ ] 1.2 Add failing dispatcher tests for push intake. Check: Dispatcher tests for match, paths union, truncation, fork refusal, supersede, duplicates, spoof and denial fail on main and pass after the fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. delivery

- [ ] 3.1 Implement Paths, Dispatcher.ReviewRequest, forge source kind, service-principal start and ordered replacement. Check: New unit and conformance tests pass.
- [ ] 3.2 Wire Dispatcher.ReviewRequest to the Forge port event; trigger routes keep their authorization actions. Check: The route test passes for `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers`.
- [ ] 3.3 Add hostile tests for each guard layer. Check: Each guard test fails when its guard is removed.
- [ ] 3.4 Add an e2e step against a fake forge. Check: The step shows a push starts a plan, an unrelated push starts nothing, a fork push writes an audit entry and a newer push replaces the older plan.
- [ ] 3.5 Write the user docs page. Check: make docs-lint passes.
- [ ] 3.6 Run the definition of done: usability check from the docs page and make check. Check: make check reports zero findings.
