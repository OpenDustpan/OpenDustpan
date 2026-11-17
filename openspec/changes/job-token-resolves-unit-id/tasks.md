## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 SECURITY. Write failing cases in `pkg/identity/jobtoken_rule_test.go` as the NEW top-level test TestJobTokenRuleResolvesUnitID, a table: the own unit by name allows (no stable ID on the object), another unit by name is refused, a unit the port does not find is refused, a port that fails answers an error that is not a denial, an object that carries a stable ID is not resolved again, and a principal that is not a job token never calls the port. Check: go test ./pkg/identity/ -run TestJobTokenRuleResolvesUnitID -count=1 fails on main because the NEW field `UnitIDs` and the NEW port `UnitIDReader` do not exist and `pkg/identity/jobtoken_rule.go:123` allows only an object with a stable ID.
- [x] 1.2 SECURITY. Write a failing test in `pkg/dustpan/jobtoken_state_route_test.go` (NEW) as the NEW top-level test TestJobTokenReachesOwnUnitState: a job token reads `/tofu/state/{namespace}/{workspace}` of its own unit (200), of another unit (403), of a unit removed and made again with the same name (401 "job token ended"), and with no token (401). Check: go test ./pkg/dustpan/ -run TestJobTokenReachesOwnUnitState -count=1 fails on main because the own unit answers 403 (`internal/api/tofuhttp/tofuhttp.go:270` authorizes an object with no stable ID).

## 2. Build

- [x] 2.1 SECURITY. In `pkg/identity/jobtoken_rule.go` add the NEW port `UnitIDReader` (method `UnitID(ctx, Object) (string, bool, error)`, the signature of `access.Units`) and the NEW field `UnitIDs` of `JobTokenRule`. In `JobTokenRule.Authorize`, when the object is a unit with no stable ID, read the stable ID through `UnitIDs` and compare it with the unit of the job. A rule with no port refuses such an object (fail closed). Add a comment that says the read is a defense-in-depth layer and that the comparison does not rely on the route. Check: go test ./pkg/identity/ -run TestJobTokenRuleResolvesUnitID -count=1
- [x] 2.2 In `pkg/dustpan/dustpan.go` (`newAuthPorts`) wire the store that meets `access.Units` as `UnitIDs` of the rule. Check: go test ./pkg/dustpan/ -run TestJobTokenReachesOwnUnitState -count=1
- [x] 2.3 SECURITY. Prove the guard: run the test of 1.1 once with the comparison of the stable ID removed from `JobTokenRule.Authorize` in `pkg/identity/jobtoken_rule.go`, and paste the failing output into the pull request body. Check: the pull request body holds the failing run.

## 3. Definition of done

- [ ] 3.1 Add an e2e step in `e2e/job_token_state_test.go` (NEW) that reads the state of the unit of a job with its job token through the real OpenTofu CLI: call the route /tofu/state/{namespace}/{workspace} with the token (credential kind job-token, identity.CredentialJobToken), on the unit of the job and on another unit; and call GET /api/stacks/{namespace}/{project}/{stack}/export with the same token kind, on the unit of the job (200) and on another unit (403). Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [x] 3.2 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
