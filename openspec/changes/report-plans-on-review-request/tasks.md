## 1. Failing tests first

- [ ] 1.1 Write failing report tests for one summary check and one edited comment. Check: reviewrequesttest report cases fail on main.
- [ ] 1.2 Write failing Forge conformance cases for check and comment methods. Check: forgetest check and comment cases fail on main.
- [ ] 1.3 Write failing renderer and summary tests: pending, failed, Git host error, deleted comment, concurrent edits. Check: renderer table test fails on main.
- [ ] 1.4 Write failing timing tests across three levels, with an unset level, a collection that forbids overrides and audit entries. Check: unitsettings timing matrix test fails on main.

## 2. Definition of done

- [ ] 2.1 Implement the report service, renderer, GitHub adapter and timing setting. Check: go test ./pkg/reviewrequest/... ./pkg/forge/... ./pkg/unitsettings/... passes.
- [ ] 2.2 Wire the routes with their authorization actions and the webhook intake. Check: The route test passes with an action on each route.
- [ ] 2.3 Add the e2e step with the fake GitHub API: three units, one push, one second push. Check: make e2e shows one summary check and one edited comment.
- [ ] 2.4 Write the docs page and run the usability check from the page alone. Check: make docs-lint passes.
- [ ] 2.5 Run the full check. Check: make check reports zero findings.
