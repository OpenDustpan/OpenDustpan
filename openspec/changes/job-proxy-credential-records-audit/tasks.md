## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests in NEW file pkg/jobproxy/proxy_test.go for injection without values, fail closed, redirect strip, redaction, records, finish-phase audit, list-records and plan-phase block. Check: TestProxyInjectionHoldsNoValue fails on main.
- [ ] 1.2 Add a failing e2e step in NEW file e2e/jobproxy_test.go against the real runner pool and the real Pulumi CLI. The step reads the records through `GET /api/audit/entries` with an access token. Check: e2e step fails on main and the job log lacks blocked by the job proxy.

## 2. Implementation

- [ ] 2.1 Implement credential hold and injection with fail closed in NEW file pkg/jobproxy/vault.go (NEW proxyCredentialVault and NEW newProxyCredentialVault). The task adds the NEW top-level test `TestProxyInjection` in a `_test.go` file of `pkg/jobproxy`, so that the Check runs it. Check: `go test ./pkg/jobproxy/... -run TestProxyInjection -count=1` passes.
- [ ] 2.2 Implement response scan and redaction with over-limit refuse in NEW file pkg/jobproxy/scan.go. The task adds the NEW top-level test `TestProxyResponseScan` in a `_test.go` file of `pkg/jobproxy`, so that the Check runs it. Check: `go test ./pkg/jobproxy/... -run TestProxyResponseScan -count=1` passes.
- [ ] 2.3 Add the per-request records route (list-records) with authorization actions. Declare it in NEW file internal/api/route/proxyrecords.go and write its adapter in NEW file internal/api/proxyrecords.go. Check: Route test passes and the route declares actions.
- [ ] 2.4 Write phase aggregation and the idempotent audit entry (finish-phase) in NEW file pkg/audit/proxyphase.go. The task adds the NEW top-level test `TestProxyPhaseAudit` in a `_test.go` file of `pkg/audit`, so that the Check runs it. Check: `go test ./pkg/audit/... -run TestProxyPhaseAudit -count=1` passes.
- [ ] 2.5 Enforce the plan-phase block with method-based write check in NEW file pkg/jobproxy/planblock.go (NEW planEffectiveRules and NEW newPlanEffectiveRules). The task adds the NEW top-level test `TestPlanPhaseWriteBlock` in a `_test.go` file of `pkg/jobproxy`, so that the Check runs it. Check: `go test ./pkg/jobproxy/... -run TestPlanPhaseWriteBlock -count=1` passes.

- [ ] 2.6 SECURITY. Build the header strip and the per-redirect host check in NEW file pkg/jobproxy/requestcheck.go. The proxy removes job credential headers and checks each redirect host against the effective rule set. Check: `go test ./pkg/jobproxy/... -run TestRequestCheckHostile -count=1` passes.
- [ ] 2.7 SECURITY. Add a hostile test in NEW file pkg/jobproxy/requestcheck_test.go, test name TestRequestCheckHostile. The test sends a forged Authorization header and a redirect to an unlisted host straight to the request check. Check: `go test ./pkg/jobproxy/... -run TestRequestCheckHostile -count=1` passes and the test fails when the strip or the redirect check is removed.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the routes with authorization actions. Check: Route test passes.
- [ ] 4.2 Add the e2e step against the real runner in e2e/jobproxy_test.go (the file of task 1.2). The step reads the records through `GET /api/audit/entries` with an access token. Check: e2e job log contains blocked by the job proxy.
- [ ] 4.3 Write the docs page in the same pull request in NEW file docs/guides/job-proxy-records-audit.md. Check: Docs page shows records and the plan-phase block. `make docs-lint` passes on each edited guide.
- [ ] 4.4 Agent pre-run (human run-through in person-steps (job-proxy-credential-records-audit 4.4)): Run the usability check. Check: Persona completes the path from the docs page alone. Record the transcript in the pull request body.
- [ ] 4.5 Run make check. Check: `make check` reports zero findings.
