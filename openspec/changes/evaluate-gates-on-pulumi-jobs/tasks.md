## 1. Failing tests first

- [ ] 1.1 Add failing conformance tests for change.propose, change.planned and change.apply on Pulumi jobs Check: `go test ./pkg/gate/... -run TestChangeProposePulumiJob -count=1` fails on main, passes after the fix
- [ ] 1.2 Add failing handler tests for state.write at the Pulumi update create with the Pulumi error shape Check: `go test ./internal/api/pulumi/... -run TestStateWriteAtUpdateCreate -count=1` fails on main, passes after the fix
- [ ] 1.3 Add failing tests for engine diagnostic errors at the three Pulumi job points with fail-closed result error Check: `go test ./pkg/gate/... -run TestPulumiGateEngineErrorFailsClosed -count=1` fails on main, passes after the fix

## 2. Implementation

- [ ] 2.1 Wire the three job evaluations in the Pulumi job path with fact producers per point Check: `go test ./pkg/job/... -count=1` passes
- [ ] 2.2 Wire state.write in the Pulumi update create before the store create Check: `go test ./internal/api/pulumi/... -count=1` passes and a denied create returns code and message
- [ ] 2.3 Wire no double evaluation so a runner update of its own job skips state.write Check: `go test ./pkg/job/... -run TestRunnerUpdateSkipsStateWrite -count=1` passes
- [ ] 2.4 Add hostile handler tests for a forged fact document and a cross-unit gate Check: `go test ./internal/api/pulumi/... -run TestPulumiGateForgedFacts -count=1` passes
- [ ] 2.5 Check the store layer: gate rows key on stable unit ID and the record cites the current gate version Check: `go test ./pkg/store/... -run TestGateRowsKeyOnUnitID -count=1` passes after a stack rename
- [ ] 2.6 Add e2e steps with the real Pulumi CLI for enforce deny at each of the four points and for evaluate mode Check: `make e2e E2E_TOOL=pulumi` passes with the real Pulumi CLI
- [ ] 2.7 Ship the docs page for gates on Pulumi jobs and CLI writes in the same pull request Check: `make docs-lint` passes and the page names each refusal and the one command that helps

## 3. Definition of done

- [ ] 3.1 Check wiring, routes and authorization actions Check: Routes declare their authorization actions and the route test passes
- [ ] 3.2 Run e2e against the real Pulumi CLI and the real directory Check: `make e2e` passes on SQLite and PostgreSQL through the real binary over TLS
- [ ] 3.3 Run a usability check of the refusal text and the decision record read Check: A developer completes the path from the docs page alone
- [ ] 3.4 Run the full check with zero findings Check: `make check` reports zero findings
