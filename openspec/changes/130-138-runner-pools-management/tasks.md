## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service tests for CreatePool create, get one, list, PATCH, and limit 50 pools per namespace. Check: NEW TestRunnerPoolsCreateListFailsOnMain in internal/runnersvc
- [ ] 1.2 Add failing service tests for CreateJoinToken single use, RevokeJoinToken, expiry, and 7-day TTL cap. Check: NEW TestRunnerJoinTokenSingleUseFailsOnMain in internal/runnersvc
- [ ] 1.3 Add failing service tests for DeletePool in-use refusal, purpose-set immutable with runner.immutable, and RetireRunner drain plus retire. Check: NEW TestRunnerPoolInUseAndRetireFailOnMain in internal/runnersvc

## 2. Build

- [ ] 2.1 Build SQL adapters for PoolStore and MemberStore join-token single-use transaction with namespace fence. Check: SQL adapters pass pkg/runner/runnertest suite on sqlite and postgres
- [ ] 2.2 Build the NEW service in internal/runnersvc and wire pool create, get one, PATCH, delete, drain, and retire routes with namespace:read-runners and namespace:manage-runners. Check: NEW internal/runnersvc wired to POST and GET ANY /api/runners/{}/pools
- [ ] 2.3 Enforce single-use token, SHA-256 storage, dpj_ prefix, and one-hour default TTL in one fenced join transaction in the NEW file internal/runnersvc/join.go. Use the join route in internal/api/runnerapi/routes.go. Check: Join route replays one CreateJoinToken secret twice and second join answers runner.token-used
- [ ] 2.4 Ship the NEW command opendustpan runner pools list for the namespace in the NEW file cmd/opendustpan/runner_pools.go. Check: `opendustpan runner pools list --namespace <ns>` lists the created pool

- [ ] 2.5 SECURITY. Check namespace:manage-runners or namespace:read-runners at the start of each management handler in the NEW file internal/api/runnerapi/authorize_runners.go. Check: a request without the action gets 403 and no row is written
- [ ] 2.6 SECURITY. Validate purpose, kind, isolation, concurrency 1 to 64 and TTL at most 7 days in the NEW file internal/runnersvc/validate.go. Check: the service refuses unknown or out-of-range values with runner.invalid and writes no row
- [ ] 2.7 SECURITY. Write the hostile handler test for the 403 on each management route without namespace:manage-runners in the NEW file internal/api/runnerapi/manage_runners_forbidden_test.go. Check: NEW TestManagementRoutesForbiddenWithoutManageRunners in internal/api/runnerapi sends each pool, join-token, drain and retire route without the action and expects 403
- [ ] 2.8 SECURITY. Write the hostile service test in the NEW file internal/runnersvc/validate_test.go that calls the service directly. Check: NEW TestServiceRejectsHostileInput in internal/runnersvc sends an unknown purpose, kind and isolation, concurrency 0 and a TTL above 7 days, and expects runner.invalid with no row written

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run e2e step against the real CLI: create pool, create token, join with dustpan-runner, retire, delete refused in use. Check: GET ANY /api/runners/{}/pools with access token of a namespace admin. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Add user docs page for pools and join tokens in the NEW file docs/guides/runner-pools.md in the same pull request. Check: docs page docs/guides/runner-pools.md runs as written. Confirmed by: CI (an e2e step in the NEW file e2e/130_138_runner_pools_management_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.3 Run usability check, route action test TestActionsWithoutRoutesAreUnclassified, and make check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
