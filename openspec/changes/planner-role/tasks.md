## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests in NEW file e2e/e2e_planner_test.go of planner reads with an access token on the existing route /api/stacks/{}/{}/{}/state/{}/outputs and the masked state read GET /api/v2/state-versions/{state_version_id}/resources. ADR 212 decision 1: a planner "reads state with the state actions that the role gives, as viewer does". ADR 41 gives the masked read with unit:read; the raw download GET /api/v2/state-versions/{state_version_id}/download needs unit:read-secrets, so `pulumi stack export` and `tofu state pull` answer access.denied. Check: TestPlannerReadsStateWithBothCLIs fails on main.
- [ ] 1.2 Write failing tests in NEW file e2e/e2e_planner_refused_test.go of refused writes and reveal with an access token on /api/stacks/{}/{}/{}/state/{}/reveal. Each refusal answers 403 with access.denied (internal/errcode/errcode.go:19). Check: TestPlannerWriteRefused and TestPlannerSecretRevealRefused fail on main.
- [ ] 1.3 Write failing tests TestPlannerAccessDecision in pkg/access/planner_role_test.go (existing file, pkg/access/planner_role_test.go:12 TestPlannerRole) that Authorizer.Authorize (pkg/access/authorizer.go:111) allows planner reads and refuses writes, reveal and a token for another principal with access.denied. Check: TestPlannerAccessDecision fails on main.
- [ ] 1.4 Write hostile tests in pkg/access/planner_role_test.go for the size limit and containment: TestPlannerHostileInputLimited and TestPlannerGrantContainment (a planner grant on one collection never reaches a unit of another collection). Check: TestPlannerHostileInputLimited and TestPlannerGrantContainment fail on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run the e2e planner step with the real Pulumi CLI and OpenTofu, a personal access token, route /api/stacks/{}/{}/{}/state/{}/reveal and the masked state route GET /api/v2/state-versions/{state_version_id}/resources. Check: make e2e planner step passes.
- [ ] 3.2 Update docs/guides/planner-role.md and run its commands. Check: Each command runs as written.
- [ ] 3.3 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
