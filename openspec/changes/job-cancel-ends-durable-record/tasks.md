## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the failing tests of the scenarios in the job conformance suite in NEW file `pkg/job/cancel_durable_test.go`. Check: go test ./pkg/job/... fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the job cancel route to the joint write. Add the e2e step that cancels a job on a real runner with the access token of a Developer who has the grant of the cancel action. Check: make e2e: POST of the existing job cancel route under /api/dustpan/v1/namespaces/{namespace}/jobs with a personal token, then GET of the job shows canceled. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Add the docs page in NEW file `docs/guides/job-cancel.md` and run the usability check from the page alone. Check: make docs-lint and make check with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
