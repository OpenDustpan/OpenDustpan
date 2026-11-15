## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for the three job points in NEW file pkg/job/pulumi_gates_test.go. Check: `go test ./pkg/job/... -run TestChangeProposePulumiJob -count=1` fails on main
- [ ] 1.2 Add failing handler tests for state.write at the update create in NEW file internal/api/pulumi/state_write_test.go. Check: `go test ./internal/api/pulumi/... -run TestStateWriteAtUpdateCreate -count=1` fails on main
- [ ] 1.3 Add failing tests for the credential and operation kind scenarios in NEW file internal/api/pulumi/state_write_kind_test.go. Check: `go test ./internal/api/pulumi/... -run TestStateWriteCredentialAndKind -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Wire the three job evaluations in pkg/job/service.go (the file with the change.propose point at line 61). Check: `go test ./pkg/job/... -count=1` passes
- [ ] 2.2 Wire state.write in the update create handler in NEW file internal/api/pulumi/state_write.go, called from the update create route in the adapter of internal/api/pulumi. Check: `go test ./internal/api/pulumi/... -count=1` passes
- [ ] 2.3 Add e2e steps in NEW file e2e/pulumi_gates_test.go with the real Pulumi CLI that run `pulumi up` against the route `POST /api/stacks/{owner}/{project}/{stack}/update/{kind}` with an access-token credential of a person, for a gate in enforce mode (403 shown by the CLI) and in evaluate mode (update runs, record written) Check: `make e2e` passes the new Pulumi enforce and evaluate steps. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.4 Ship the docs page in NEW file docs/user/gates-pulumi.md. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Check routes and authorization actions Check: The route test passes
- [ ] 4.2 Run the usability check Check: A developer completes the path from the docs page alone. A person's step.
- [ ] 4.3 Run the full check Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
