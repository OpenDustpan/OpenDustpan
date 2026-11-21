## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing API tests in NEW file internal/api/gate_simulate_test.go for simulate per-record old and new outcomes with draft gates and approval rules. Check: TestSimulateDraftAgainstStoredRecords fails on main.
- [ ] 1.2 Add failing tests in NEW file internal/api/gate_simulate_skip_test.go for skipped records with no trace and no side effects, including a write side-effect draft and a schema-unknown record. Check: TestSimulateSkipsUnreadableChanges fails on main.
- [ ] 1.3 Add failing tests in NEW file pkg/gate/packs_test.go for pack list, pack install in evaluate mode, all-or-nothing enforce refusal, name conflict, same-version reinstall, and designed-against wording. Check: TestStarterPackInstallsEvaluateOnly fails on main.
- [ ] 1.4 Add failing tests in NEW file pkg/gate/simulate_test.go for one-snapshot replay with timeout and cancel, for 1,000-record refusal without truncation, and for two concurrent installs of one pack. Check: TestSimulateSnapshotTimeout and TestPackInstallRace fail on main.

## 2. Build

- [ ] 2.1 Build the simulate route in NEW file internal/api/gate_simulate.go with its authorization action and record filter of decision point, time range and 1,000 limit. Check: POST /api/dustpan/v1/namespaces/{namespace}/gates/simulate with namespace:read-gates returns stored and new results with no writes.
- [ ] 2.2 Ship starter packs in NEW file pkg/gate/packs.go as versioned data with list, atomic install and pack tests. Check: GET packs with namespace:read-gates and POST packs install with namespace:manage-gates; pack install makes only evaluate mode gates with test files run by gate.RunTestFile.
- [ ] 2.3 Write the docs page in NEW file docs/gates-simulate.md in the same pull request. Check: Docs page for simulate and starter packs passes make docs-lint.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the routes with their authorization actions. Check: Route declares its authorization actions and passes the route test.
- [ ] 4.2 Add an e2e step in NEW file e2e/gate_simulate_test.go against the real server and the real directory with a personal access token, for the route POST /api/dustpan/v1/namespaces/{namespace}/gates/simulate and the pack install route. Check: e2e step posts drafts to POST /api/dustpan/v1/namespaces/{namespace}/gates/simulate and installs one pack with a personal access token against the real server.
- [ ] 4.3 Agent pre-run (human run-through in person-steps (add-gate-simulate-route-starter 4.3)): Run the usability check from the docs page. Check: Persona follows the docs page alone and each error names the one command that helps. Record the transcript in the pull request body.
- [ ] 4.4 Run make check with zero findings. Check: make check passes with zero findings.
