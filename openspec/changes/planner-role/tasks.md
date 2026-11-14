## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests in NEW file e2e/e2e_planner_test.go of planner reads with an access token on the existing route /api/stacks/{}/{}/{}/state/{}/outputs and the OpenTofu state read. Check: TestPlannerReadsStateWithBothCLIs fails on main.
- [ ] 1.2 Write failing tests in NEW file e2e/e2e_planner_refused_test.go of refused writes and reveal with an access token on /api/stacks/{}/{}/{}/state/{}/reveal. Check: TestPlannerWriteRefused and TestPlannerSecretRevealRefused fail on main.
- [ ] 1.3 Write failing tests in pkg/gate/service_test.go (NEW file, package gate) that CheckAccess (pkg/gate/service.go:297) allows planner reads and refuses writes, reveal and a token for another principal. Check: TestPlannerCheckAccess fails on main.
- [ ] 1.4 Write hostile tests in NEW file pkg/gate/planner_hostile_test.go for the size limit and containment of request input on the refused routes. Check: TestPlannerHostileInputLimited fails on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run the e2e planner step with the real Pulumi CLI and OpenTofu, a personal access token, route /api/stacks/{}/{}/{}/state/{}/reveal and the OpenTofu state routes. Check: make e2e planner step passes.
- [ ] 3.2 Update docs/guides/planner-role.md and run its commands. Check: Each command runs as written.
- [ ] 3.3 Run the full check. Check: `make check` reports zero findings.
