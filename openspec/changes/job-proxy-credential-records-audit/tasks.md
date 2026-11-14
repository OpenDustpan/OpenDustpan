## 1. Failing tests first

- [ ] 1.1 Add failing tests for injection without values, fail closed, redirect strip, redaction, records, audit, and plan-phase block. Check: TestProxyInjectionHoldsNoValue fails on main.
- [ ] 1.2 Add failing e2e step with real job log that shows blocked by the job proxy with host and path. Check: TestProxyRecordListShowsDecision fails on main.

## 2. Implementation

- [ ] 2.1 Implement proxy-only credential hold and request-time injection with fail closed. Check: `go test ./pkg/job/... -run TestProxyInjection -count=1` passes.
- [ ] 2.2 Implement response scan and redaction with scan findings. Check: `go test ./pkg/job/... -run TestProxyResponseScan -count=1` passes.
- [ ] 2.3 Add per-request records route with authorization actions. Check: New list proxy records route returns request records with authorization actions.
- [ ] 2.4 Write phase record aggregation and namespace audit entry with proxy facts. Check: `go test ./internal/... -run TestProxyPhaseAudit -count=1` passes.
- [ ] 2.5 Enforce plan-phase block for writes that only apply rules allow. Check: `go test ./pkg/job/... -run TestPlanPhaseWriteBlock -count=1` passes.

## 3. Definition of done

- [ ] 3.1 Wire routes with authorization actions. Check: Route declares authorization actions and the route test passes.
- [ ] 3.2 Add e2e step against the real runner and real directory. Check: E2e job log contains blocked by the job proxy with host and path.
- [ ] 3.3 Write docs page in the same pull request. Check: Docs page shows records route and plan-phase block.
- [ ] 3.4 Run usability check for refused requests and records. Check: Persona completes the path from the docs page alone.
- [ ] 3.5 Run make check with zero findings. Check: `make check` reports zero findings.
