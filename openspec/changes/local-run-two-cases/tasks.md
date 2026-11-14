## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Failing e2e for the overlap case: no provider call from the wrapper until the blocker ends, then a new plan on the new base. BLOCKED by the claim register route and the wrapper credential kind, which two-disjoint-claims and ADR 369 define Check: TestLocalOverlapWaitsForBlocker fails on main
- [ ] 1.2 Failing e2e for the disjoint case: overlapping apply periods with no lost resource. BLOCKED by the claim register route and the wrapper credential kind, which two-disjoint-claims and ADR 369 define Check: TestLocalDisjointOverlapsRunner fails on main
- [ ] 1.3 Failing test for discard of old plan, new plan on the new base, and re-entry into wait on a second refusal Check: TestLocalReplanNewBase fails on main
- [ ] 1.4 Failing test for timeout exit with no provider call and for interrupt during wait and after accept Check: TestLocalTimeoutAndInterrupt fails on main
- [ ] 1.5 Failing test for retry hint with backoff with jitter, honor of Retry-After, progress output, and 403 exit with no retry Check: TestLocalRetryHintAndAuth fails on main

## 2. Implementation after ADR 368 and ADR 369

- [ ] 2.1 Add the wrapper wait, retry, new plan on the new base, timeout flag wired to the values of ADR 368 decision 5, and progress output Check: go test -race ./... -run TestLocalClaimRetry
- [ ] 2.2 Wire the wrapper command to the claim register route. BLOCKED by the claim register route of two-disjoint-claims Check: `dustpan tofu apply` against a dev server on PostgreSQL

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 e2e step against the real tofu CLI for overlap wait and disjoint side-by-side apply. BLOCKED by the claim register route and the wrapper credential kind of two-disjoint-claims Check: make e2e with real tofu CLI on PostgreSQL and a fake provider time log for both local cases
- [ ] 4.2 Docs page for the wrapper wait, progress output, and timeout flag with values from ADR 368 decision 5 Check: make docs-lint
- [ ] 4.3 Usability check from the docs page alone Check: A Developer completes both cases from the docs page alone, and each error names the one command that helps
- [ ] 4.4 Run make check Check: `make check` with zero findings
