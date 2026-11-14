## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing e2e test for push to review request yielding plans, one status, and one comment. Route: GET /api/dustpan/v1/namespaces/{namespace}/units with an access token; the fake GitHub API is read with its own test credential. BLOCKED by the three depends_on changes until they merge. Check: TestFirstSliceComposedFlow fails on main: plans listed via GET units, status and comment read from the fake GitHub API.
- [ ] 1.2 Write failing e2e test for new push editing the same comment. Route: GET units with an access token; fake GitHub API read. BLOCKED by report-plans-on-review-request. Check: TestFirstSliceEditSameComment fails on main: comment count on the fake GitHub API stays at one after a second push.
- [ ] 1.3 Write failing e2e test for push with no touched unit starting nothing. Route: GET units with an access token; fake GitHub API read. BLOCKED by start-plan-on-push-review. Check: TestFirstSliceEmptyMatch fails on main: GET units shows no new plan and the fake GitHub API shows no new status.

## 2. Implementation

- [ ] 2.1 Wire the step-7 e2e step over the three composed changes with a real runner plan. Route: GET units with an access token; webhook delivery from the fake GitHub API. BLOCKED by the three depends_on changes. Check: `make e2e` step-7 run shows plans on real runners, one status and one edited comment on the fake GitHub API.
- [ ] 2.2 Write the docs page for the first slice that names the three composed behaviors. Check: Docs page commands run as written against the fake GitHub API and GET units; page ships in the same pull request.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the wiring, authorization, e2e, docs and usability checks. Route: GET /api/dustpan/v1/namespaces/{namespace}/units with an access token of a principal that can read units; no new route is added. BLOCKED by the three depends_on changes for the e2e part. Check: `make check` with zero findings; e2e step plus docs usability check pass.
