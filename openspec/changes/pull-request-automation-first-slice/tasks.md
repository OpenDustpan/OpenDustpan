## 1. Failing tests

- [ ] 1.1 Write failing e2e test for push to review request yielding plans, one status, and one comment. Check: TestFirstSliceComposedFlow fails on main.
- [ ] 1.2 Write failing e2e test for new push editing the same comment. Check: TestFirstSliceEditSameComment fails on main.

## 2. Implementation

- [ ] 2.1 Wire the step-7 e2e step over the three composed changes with a real runner plan. Check: `make e2e` first-slice step shows plans, one status, and one edited comment on the fake GitHub API.
- [ ] 2.2 Write docs page for the first slice that names the three composed behaviors. Check: Docs page for review request plans ships in same pull request.

## 3. Definition of done

- [ ] 3.1 Run wiring, route authorization, e2e, docs and usability checks. Check: `make check` with zero findings.
