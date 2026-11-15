## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write a failing test for each scenario of the spec in NEW internal/stepupbootstrap/bootstrap_test.go. Check: go test of the bootstrap package fails on main
- [ ] 1.2 Write the store tests for the marker, the admin reset and the challenge use in the NEW test file pkg/store/sqlite/stepup_test.go and in the NEW test file pkg/store/postgres/stepup_test.go. Check: go test of the store conformance suite fails on main

## 2. Implementation

- [ ] 2.1 Build the host command in NEW cmd/opendustpan/host_stepup_bootstrap.go, the bootstrap token type in NEW internal/stepupbootstrap/bootstrap.go, and the store method for the marker (NEW) in NEW pkg/store/sqlite/stepup.go and NEW pkg/store/postgres/stepup.go. Check: go test of the bootstrap package passes
- [ ] 2.2 Move the lock into the AcceptStep port (NEW method) in internal/stepup/stepup.go, and add the adapters NEW pkg/store/sqlite/stepup.go and NEW pkg/store/postgres/stepup.go. Check: TestAcceptStepTakesTheLock (NEW) in NEW internal/stepup/accept_step_test.go
- [ ] 2.3 Add the store method that consumes a key-add challenge (NEW) in NEW pkg/store/sqlite/stepup.go and NEW pkg/store/postgres/stepup.go. Call it from internal/stepup/stepup.go:728 (CheckKeyAdditionChallenge). Check: store conformance test for challenge use in NEW pkg/store/sqlite/stepup_test.go and NEW pkg/store/postgres/stepup_test.go
- [ ] 2.4 Add the admin challenge counter (NEW) in NEW pkg/store/sqlite/stepup.go and NEW pkg/store/postgres/stepup.go. Check: TestAdminChallengeCountIsSeparate (NEW) in NEW pkg/store/sqlite/stepup_test.go and NEW pkg/store/postgres/stepup_test.go
- [ ] 2.5 Add the store method for the admin reset and its audit entry (NEW) in NEW pkg/store/sqlite/stepup.go and NEW pkg/store/postgres/stepup.go. Check: store conformance test for the reset in NEW pkg/store/sqlite/stepup_test.go and NEW pkg/store/postgres/stepup_test.go
- [ ] 2.6 Add the marker read to the API. Declare the route in NEW internal/api/apidesc/stepup_bootstrap.go. Write its adapter in NEW internal/api/server/stepup_bootstrap.go. Check: route test in NEW internal/api/server/stepup_bootstrap_test.go and API call that returns the marker

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the host command with its authorization actions Check: route test and `opendustpan host step-up-bootstrap --help`
- [ ] 4.2 Add an e2e step in NEW e2e/stepup_bootstrap_test.go that runs the host command of the real binary, then reads the marker with GET /api/dustpan/v1/settings and the access token of a site administrator Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Write the NEW docs page docs/guides/step-up-bootstrap.md with the sole-key recovery and the restore note Check: a test runs each command of the page. `make docs-lint` passes on each edited guide.
- [ ] 4.4 Usability check from the docs page alone Check: each error names one command. A person's step.
- [ ] 4.5 Run all checks Check: `make check` has zero findings. Confirmed by: CI (the agent runs the fast checks only).
