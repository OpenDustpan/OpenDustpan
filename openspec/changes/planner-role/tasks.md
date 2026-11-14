## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests for planner reads with the Pulumi CLI over POST/GET /api/stacks/{}/{}/{}/state routes and OpenTofu with a personal access token. BLOCKED by the exact route list of groups-roles-grants for the tool-neutral grant route. Check: TestPlannerReadsStateWithBothCLIs fails on main.
- [ ] 1.2 Write failing tests for refused up, destroy, apply, reveal, other routes, grant union and revocation, with an access token credential. Check: TestPlannerWriteRefused, TestPlannerSecretRevealRefused, TestPlannerGrantUnion and TestPlannerRevocation fail on main.

## 2. Implementation

- [ ] 2.1 Define the planner action set and wire action checks into the handlers and store adapters. Check: `go test ./pkg/access/... ./internal/api/... ./pkg/store/...` passes.
- [ ] 2.2 Refuse planner plan-job start. Check: `go test ./internal/api/...` shows 403.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the e2e planner step with the real Pulumi CLI and OpenTofu using a personal access token against the stack reveal route and the TFE-compatible state routes. Check: make e2e planner step passes.
- [ ] 4.2 Update docs/guides/planner-role.md and run the usability check. Check: Each command runs as written.
- [ ] 4.3 Run the full check. Check: `make check` reports zero findings.
