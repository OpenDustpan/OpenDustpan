## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for the three job points Check: `go test ./pkg/gate/... -run TestChangeProposePulumiJob -count=1` fails on main
- [ ] 1.2 Add failing handler tests for state.write at the update create Check: `go test ./internal/api/pulumi/... -run TestStateWriteAtUpdateCreate -count=1` fails on main
- [ ] 1.3 Add failing tests for the credential and operation kind scenarios Check: `go test ./internal/api/pulumi/... -run TestStateWriteCredentialAndKind -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Wire the three job evaluations Check: `go test ./pkg/job/... -count=1` passes
- [ ] 2.2 Wire state.write in the update create Check: `go test ./internal/api/pulumi/... -count=1` passes
- [ ] 2.3 Add e2e steps with the real Pulumi CLI for enforce and evaluate Check: `make e2e` passes
- [ ] 2.4 Ship the docs page Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check routes and authorization actions Check: The route test passes
- [ ] 4.2 Run the usability check Check: A developer completes the path from the docs page alone
- [ ] 4.3 Run the full check Check: `make check` reports zero findings
