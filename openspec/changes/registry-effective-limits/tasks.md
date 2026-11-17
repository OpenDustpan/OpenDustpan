## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing test for the route in NEW file internal/api/registry/limits_test.go: the answer holds the six limits with units, and each value equals the value of the check. Name TestLimits_showsEffectiveValues as NEW. Check: `go test -count=1 -v -run '^TestLimits_showsEffectiveValues$' ./internal/api/registry` lists the NEW test as PASS on the PR head. Confirmed by: the pull request body quotes the failing output from the commit of this task, before task 2.1.
- [ ] 1.2 Add failing hostile tests in NEW file internal/api/registry/limits_hostile_test.go: no read grant (403), a body and a query value (400), an unknown namespace (404) and a hostile path value (404 without echo). Name TestLimits_Hostile as NEW. Check: `go test -count=1 -v -run '^TestLimits_Hostile$' ./internal/api/registry` lists the NEW test as PASS on the PR head. Confirmed by: the pull request body quotes the failing output from the commit of this task, before task 2.1.
- [ ] 1.3 Add a failing test for the constructor in NEW file pkg/registry/limits_test.go: a limit of zero or less is refused. Name TestLimits_refusesNonPositive as NEW. Check: `go test -count=1 -v -run '^TestLimits_refusesNonPositive$' ./pkg/registry` lists the NEW test as PASS on the PR head. Confirmed by: the pull request body quotes the failing output from the commit of this task, before task 2.1.

## 2. Build

- [ ] 2.1 Add `Limits` and its constructor in NEW file pkg/registry/limits.go, and make the handler (internal/api/registry/registry.go:49), the service (internal/registry/service/service.go:40) and the unpack of the parent change read the one value. Check: `go test -race -count=1 ./pkg/registry/... ./internal/registry/... ./internal/api/registry/...` passes.
- [ ] 2.2 Add GET /api/dustpan/v1/namespaces/{namespace}/registry/limits to buildRoutes in internal/api/registry/routes.go with the action registry:read and the handler in NEW file internal/api/registry/limits.go, and declare its answer in internal/api/registry/operations.go. SECURITY: the hostile tables of the three layers are the tasks 1.1 to 1.3, and each guard has a comment that it is a defense-in-depth layer. Check: `go test -count=1 -run 'TestRoutes' ./internal/api/registry` passes (a route without an action fails the route test).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add a section with the six limits and the command that shows them to the registry publish page, docs/guides/registry-publish.md (the page that the parent change adds). Check: Docs page commands run as written for the read of the limits. Confirmed by: CI (an e2e step in the NEW file e2e/registry_effective_limits_test.go runs the command).
- [ ] 4.2 Add an e2e step in NEW file e2e/registry_effective_limits_test.go that reads the limits with an access token and checks each value against the value in the error of a refused publish. Check: `make e2e` runs the step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the usability check of the read of the limits from the docs page alone. Check: A namespace administrator reads the limits from the docs page alone; the error of a missing grant names its one command. A person's step.
- [ ] 4.4 Pass the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
