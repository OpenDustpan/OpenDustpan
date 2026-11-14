## 1. Failing tests

- [ ] 1.1 Write the table test TestJobTokenRuleAuthorize in the NEW file `pkg/identity/jobtoken_rule_test.go`: the unit of the job is allowed; another unit, another namespace, a collection, a principal and the server are refused; a principal with a job ID that is not a consistent job token is refused every action; an administrator or temporary administrator mark gives a job token nothing more. Check: the test fails on main
- [ ] 1.2 Write TestJobTokenRuleListObjects in `pkg/identity/jobtoken_rule_test.go`: a job token lists its unit only, and nothing when the next scope does not name its unit. Check: the test fails on main
- [ ] 1.3 Write TestDecoratorLeavesAJobTokenAlone in the existing file `pkg/tempadmin/tempadmin_test.go`. Check: the test fails on main
- [ ] 1.4 Write TestNewAuthPortsAcceptsAJobToken and TestNewAuthPortsChainsBothTokenGates in the NEW file `pkg/dustpan/jobtoken_chain_internal_test.go`: the chain accepts a valid job token, a wrong value spends only the job token budget, and the rule sits outside the authorizer of an embedder. Check: the tests fail on main

## 2. Build

- [ ] 2.1 Add the rule in the NEW file `pkg/identity/jobtoken_rule.go`. It reads the stable unit ID of the job through a small port that `pkg/dustpan` gives it from the job store, at each request. Every refusal is identity.ErrDenied. Check: `go test ./pkg/identity/...` passes
- [ ] 2.2 Wire `jobtoken.Service` (`pkg/jobtoken/service.go:145`) into `newAuthPorts` (`pkg/dustpan/dustpan.go:878`) and `gatedAuthenticator` (`pkg/dustpan/area_signin.go:64`) with its own budget scope, and put the rule outside the authorizer of an embedder. Check: `go test ./pkg/dustpan/...` passes
- [ ] 2.3 Let `tempadmin.Decorator` (`pkg/tempadmin/authenticator.go:81`) pass a job token through, with a comment that names it a defense-in-depth layer. Check: `go test ./pkg/tempadmin/...` passes
- [ ] 2.4 Write an audit entry for each refusal of the rule, in the same request, through the audit port of `pkg/dustpan`. Check: the tests of 1.1 assert one audit entry per refusal

## 3. Definition of done

- [ ] 3.1 Run the local checks (`Makefile`). The e2e step with a real runner job is BLOCKED by add-job-session-paths-runner-token-issuer, which issues the token. Check: `make check` reports zero findings. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
