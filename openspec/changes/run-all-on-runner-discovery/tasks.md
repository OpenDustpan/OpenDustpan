## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for discovery job start on wrapper claim in NEW file pkg/runner/discovery_test.go through ANY /runner/v1 (declaration file: pkg/dustpan/apidesc_server_test.go) with a runner token passing the runner authorization gate. Check: TestDiscoveryStartsOneJob fails on main and passes after the change; it exercises ANY /runner/v1 with a runner token in the pool namespace.
- [ ] 1.2 Add failing server test for preview output with 3 unit references in NEW file pkg/runner/discovery_preview_test.go through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: TestDiscoveryPreviewListsUnits fails on main and passes after the change; it reads the preview through ANY /runner/v1 with a runner token.
- [ ] 1.3 Add failing hostile tests for path escape with symlinks, unit count limit, foreign token, and 300 second timeout in NEW file pkg/runner/discovery_hostile_test.go through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: TestDiscoveryRefusesEscape, TestDiscoveryRefusesLimit, TestDiscoveryRefusesForeignToken, and TestDiscoveryTimeout fail on main and pass after the change.
- [ ] 1.4 Add failing audit tests for start, complete, failed, and refused entries in NEW file pkg/audit/discovery_test.go through ANY /runner/v1 with a runner token and read through ANY /api/audit/entries with an access token passing the audit read gate. Check: TestDiscoveryAuditNamesCause and TestDiscoveryAuditRefusal fail on main and pass after the change.

## 2. Build

- [ ] 2.1 Wire discovery operation in NEW file pkg/runner/discovery.go (NEW error code `run.discovery-refused` in internal/errcode/catalog_runner.go) through ANY /runner/v1 with a runner token passing the runner authorization gate with namespace-scoped authorization. Check: Claim through ANY /runner/v1 with a runner token starts a terragrunt-discover job in the pool namespace.
- [ ] 2.2 Write audit entries for discovery start, complete, failed, and refused in NEW file pkg/audit/discovery.go through ANY /api/audit/entries with an access token passing the audit read gate. Check: Read ANY /api/audit/entries with an access token and check the start and complete entries.
- [ ] 2.3 Add repeat check on the idempotency key for the discovery record in NEW file pkg/runner/discovery_idempotency.go through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: Run discovery twice with the same Idempotency-Key header and check one discovery record.

- [ ] 2.4 SECURITY. Build the launcher sandbox path containment in NEW file pkg/launcher/discovery_path.go. The code cleans the path, resolves symlinks and `..` segments, and denies a path outside the working directory before any read. Check: a discovery path with `..` or a symlink to a place outside the working directory returns the NEW error code `run.discovery-refused` and no file is read.
- [ ] 2.5 SECURITY. Add a hostile test sent straight to the launcher in NEW file pkg/launcher/discovery_path_test.go. Check: TestLauncherRefusesEscapingDiscoveryPath calls the launcher function directly with a `..` path and a symlinked include that points outside the working directory, and the launcher refuses both.
- [ ] 2.6 Add the line of each NEW error code (the code and its arguments, as `TestNewCodesHaveTheirLine` prints it) in its place in the name order: `run.discovery-refused` in `internal/errcode/testdata/catalog/run.txt`. No route: the test reads the catalog only. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 3. Definition of done

- [ ] 3.1 Add e2e step against the real runner and the real directory over 3 units in NEW file e2e/run_all_on_runner_discovery_test.go through ANY /runner/v1 with a runner token passing the runner authorization gate. Check: Real runner claims a wrapper job over 3 Terragrunt units through ANY /runner/v1 with a runner token and the preview lists 3 unit references with no state version.
- [ ] 3.2 Write the Terragrunt discovery docs page in NEW file docs/guides/terragrunt-discovery.md in the same pull request. Check: Docs page command sequence runs as written against a test installation. A Go e2e test runs each command of the docs page against the e2e test server. `make docs-lint` passes on each edited guide.
- [ ] 3.3 Agent pre-run (human run-through in person-steps (run-all-on-runner-discovery 3.3)): Run the usability check of the discovery path. Check: Runner operator completes discovery from the docs page alone and each error names the one command that helps. Record the transcript in the pull request body.
- [ ] 3.4 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only)
