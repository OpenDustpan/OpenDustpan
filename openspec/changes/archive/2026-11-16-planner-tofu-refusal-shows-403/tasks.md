## 1. Failing test first

- [x] 1.1 In `e2e/e2e_planner_refused_test.go:134` (func plannerTofuWriteRefused), check the standard output and the standard error of `tofu apply` and `tofu destroy` as planner: it holds the server refusal (`access.denied`, or the 403 text that OpenTofu prints for it), and it does not hold `organization "` with `not found`. Replace the comment at :126-132 with the behavior on main: the entitlement-set read answers a planner with a grant on the collection, and the write answers 403 with `access.denied`. The CLI calls the routes GET /api/v2/organizations/{organization}/entitlement-set and POST /api/v2/workspaces/{id}/actions/lock with the access token of the planner. Check: TestPlannerWriteRefused in make e2e. Confirmed by: CI (the agent writes the e2e test).

## 2. Fix, only if 1.1 fails on main

- [x] 2.1 Find the request that answers 404 in the e2e server log of the failing run and record its method and path in the pull request body. If it is a route that ADR 374 decision 1 names (the entitlement set, the organization read, a workspace read by organization), declare it in `internal/api/tfe/routes.go` with the same discovery scope as the entitlement-set route (:140), and add its case to the discovery route test. If it is any other route, stop and report to the PM. If 1.1 passes on main, mark this task not applicable with the run that shows it. The test calls the route GET /api/v2/organizations/{organization}/entitlement-set with the access token of the planner. Check: TestPlannerWriteRefused in make e2e. Confirmed by: CI.

## 3. Definition of done

- [x] 3.1 In `docs/guides/planner-role.md`, under "Refused actions", say what `tofu apply` and `tofu destroy` as planner print: the 403 with `access.denied`. Check: make docs-lint
- [x] 3.2 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
