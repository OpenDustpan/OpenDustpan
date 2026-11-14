## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for injection without values, fail closed, redirect strip, redaction, records, finish-phase audit, list-records and plan-phase block. Check: TestProxyInjectionHoldsNoValue fails on main.
- [ ] 1.2 Add a failing e2e step against the real runner pool and the real Pulumi CLI. BLOCKED by the missing route name and credential kind in the pack: the records route path and the credential kind are not named there. Check: e2e step fails on main and the job log lacks blocked by the job proxy.

## 2. Implementation

- [ ] 2.1 Implement credential hold and injection with fail closed. Check: `go test ./pkg/jobproxy/... -run TestProxyInjection -count=1` passes.
- [ ] 2.2 Implement response scan and redaction with over-limit refuse. Check: `go test ./pkg/jobproxy/... -run TestProxyResponseScan -count=1` passes.
- [ ] 2.3 Add the per-request records route (list-records) with authorization actions. Check: Route test passes and the route declares actions.
- [ ] 2.4 Write phase aggregation and the idempotent audit entry (finish-phase). Check: `go test ./pkg/audit/... -run TestProxyPhaseAudit -count=1` passes.
- [ ] 2.5 Enforce the plan-phase block with method-based write check. Check: `go test ./pkg/jobproxy/... -run TestPlanPhaseWriteBlock -count=1` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the routes with authorization actions. Check: Route test passes.
- [ ] 4.2 Add the e2e step against the real runner. BLOCKED by the missing route name and credential kind in the pack. Check: e2e job log contains blocked by the job proxy.
- [ ] 4.3 Write the docs page in the same pull request. Check: Docs page shows records and the plan-phase block.
- [ ] 4.4 Run the usability check. Check: Persona completes the path from the docs page alone.
- [ ] 4.5 Run make check. Check: `make check` reports zero findings.
