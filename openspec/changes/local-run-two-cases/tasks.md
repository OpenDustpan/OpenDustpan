## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Failing e2e in NEW file e2e/local_run_two_cases_test.go for the overlap case: no provider call from the wrapper until the blocker ends, then a new plan on the new base. BLOCKED by the claim register route and the wrapper credential kind, which two-disjoint-claims and ADR 369 define Check: TestLocalOverlapWaitsForBlocker fails on main.
- [ ] 1.2 Failing e2e in NEW file e2e/local_run_two_cases_test.go for the disjoint case: overlapping apply periods with no lost resource. BLOCKED by the claim register route and the wrapper credential kind, which two-disjoint-claims and ADR 369 define Check: TestLocalDisjointOverlapsRunner fails on main.
- [ ] 1.3 Failing test in NEW file internal/cli/localclaim/wait_test.go for discard of old plan, new plan on the new base, and re-entry into wait on a second refusal Check: TestLocalReplanNewBase fails on main
- [ ] 1.4 Failing test in NEW file internal/cli/localclaim/wait_test.go for timeout exit with no provider call and for interrupt during wait and after accept Check: TestLocalTimeoutAndInterrupt fails on main
- [ ] 1.5 Failing test in NEW file internal/cli/localclaim/wait_test.go for retry hint with backoff with jitter, honor of Retry-After, progress output, and 403 exit with no retry Check: TestLocalRetryHintAndAuth fails on main

## 2. Implementation after ADR 368 and ADR 369

- [ ] 2.1 Add in NEW file internal/cli/localclaim/wait.go the wrapper wait, retry, new plan on the new base, timeout flag wired to the values of ADR 368 decision 5, and progress output Check: go test -race ./... -run TestLocalClaimRetry
- [ ] 2.2 Wire the wrapper command in NEW file internal/cli/localclaim/register.go to the claim register route. BLOCKED by the claim register route of two-disjoint-claims Check: `dustpan tofu apply` against a dev server on PostgreSQL.go runs `dustpan tofu apply` with the real binary against the e2e test server, through the claim register route of two-disjoint-claims, with an access token).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 e2e step against the real tofu CLI for overlap wait and disjoint side-by-side apply. BLOCKED by the claim register route and the wrapper credential kind of two-disjoint-claims Check: make e2e with real tofu CLI on PostgreSQL and a fake provider time log for both local cases.
- [ ] 4.2 Docs page for the wrapper wait, progress output, and timeout flag with values from ADR 368 decision 5 Check: make docs-lint
- [ ] 4.3 Agent pre-run (human run-through in person-steps (local-run-two-cases 4.3)): Usability check from the docs page alone Check: A Developer completes both cases from the docs page alone, and each error names the one command that helps. Record the transcript in the pull request body.
- [ ] 4.4 Run make check Check: `make check` with zero findings.
