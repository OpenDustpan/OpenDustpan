## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing report tests for one summary check, three unit checks and one edited comment. Check: reviewrequesttest report cases fail on main.
- [ ] 1.2 Write failing Forge conformance cases for the check method. Check: forgetest check cases fail on main.
- [ ] 1.3 Write failing renderer and summary tests: pending, failed, no change, zero units, stale head, Git host error, deleted comment, duplicate marker, foreign marker, long list. Check: renderer table test fails on main.
- [ ] 1.4 Write failing timing tests across three levels, with an unset level, a later forbid, a bad value and audit entries. Check: unitsettings timing matrix test fails on main.

## 2. Definition of done

- [ ] 2.1 Implement the report service, renderer, GitHub adapter and timing setting. Check: go test ./pkg/reviewrequest/... ./pkg/forge/... ./pkg/unitsettings/... passes.
- [ ] 2.2 Wire the settings routes with their authorization actions and the webhook intake. Check: The route test passes with an action on each route.
- [ ] 2.3 Add the e2e step with the fake GitHub API: three units, one push, one second push, then PUT to /api/dustpan/v1/namespaces/{namespace}/settings with an access token of a principal that holds the settings manage grant. BLOCKED by the push-to-plan item (start-plan-on-push-review) for the plan results. Check: make e2e shows one summary check, three unit checks and one edited comment.
- [ ] 2.4 Write the docs page and run the usability check from the page alone. Check: make docs-lint passes.
- [ ] 2.5 Run the full check. Check: make check reports zero findings.
