## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the failing conformance case in pkg/job/jobtest for reads from an offset, run by each store adapter. Check: NEW test TestFollowReadsFromByteOffset fails on main
- [ ] 1.2 Add the new file pkg/job/offset_test.go with hostile cases for negative, non-numeric, overflowing, past-end and stale offsets. Check: NEW test TestOffsetCursorRefusesBadInput fails on main

## 2. Fix

- [ ] 2.1 Add the new file pkg/job/offsetcursor.go for the offset reader and implement it in each store. Check: TestFollowReadsFromByteOffset passes with zero bytes read before the offset
- [ ] 2.2 Add the new file internal/api/jobs/logs_offset.go to wire the offset and generation query values into the job log follow handler, on the existing job log read route with the job log read gate and an access token. Check: NEW test TestFollowPollOffset in internal/api/jobs passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Write the docs page for job log follow with the offset query and the invalid offset error. Check: make docs-lint passes
- [ ] 4.2 Run an e2e step with the real CLI against a growing log on route GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}/logs/{name} with an access token through the job log read gate; BLOCKED by NEW offset query support on that route. Check: make e2e passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the full check and confirm the usability path from the docs page. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
