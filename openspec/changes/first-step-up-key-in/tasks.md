## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write a failing test for each scenario of the spec Check: go test of the bootstrap package fails on main
- [ ] 1.2 Write the store tests for the marker, the admin reset and the challenge use Check: go test of the store conformance suite fails on main

## 2. Implementation

- [ ] 2.1 Build the host command, the bootstrap token type and the store method for the marker Check: go test of the bootstrap package passes
- [ ] 2.2 Move the lock into the AcceptStep port Check: TestAcceptStepTakesTheLock
- [ ] 2.3 Add the store method that consumes a key-add challenge Check: store conformance test for challenge use
- [ ] 2.4 Add the admin challenge counter Check: TestAdminChallengeCountIsSeparate
- [ ] 2.5 Add the store method for the admin reset and its audit entry Check: store conformance test for the reset
- [ ] 2.6 Add the marker read to the API Check: route test and API call that returns the marker

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the host command with its authorization actions Check: route test and `opendustpan host step-up-bootstrap --help`
- [ ] 4.2 Add an e2e step that runs the host command of the real binary, then reads the marker with GET /api/dustpan/v1/settings and the access token of a site administrator Check: `make e2e` passes
- [ ] 4.3 Write the docs page with the sole-key recovery and the restore note Check: a test runs each command of the page
- [ ] 4.4 Usability check from the docs page alone Check: each error names one command
- [ ] 4.5 Run all checks Check: `make check` has zero findings
