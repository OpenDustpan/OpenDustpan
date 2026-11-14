## 1. Failing tests first

- [ ] 1.1 Add failing API tests for bad input error that names the input Check: TestSelfServiceRequestValidationFailsOnMain with unknown input
- [ ] 1.2 Add failing API tests for quota refusal with no unit and no job Check: TestSelfServiceQuotaRefusedFailsOnMain with units max reached
- [ ] 1.3 Add failing render tests for one revision giving identical bytes and no secret value Check: TestSelfServiceRenderDeterministicFailsOnMain for Pulumi and OpenTofu
- [ ] 1.4 Add failing tests for one revision in flight and idempotent retry Check: TestSelfServiceConcurrentSubmitFailsOnMain for second submit in flight and retry
- [ ] 1.5 Add failing tests for Idempotency-Key retry giving one revision Check: TestSelfServiceIdempotencyKeyFailsOnMain for POST self-service requests retry
- [ ] 1.6 Add failing authz tests with audit entry Check: TestSelfServiceDenyFailsOnMain for submit without action
- [ ] 1.7 Add failing tests for recheck refusal and release Check: TestSelfServiceQuotaRecheckFailsOnMain for units fail at apply
- [ ] 1.8 Add failing concurrency tests for one winner and 409 Check: TestSelfServiceQuotaConcurrencyFailsOnMain for last-slot race

## 2. Build

- [ ] 2.1 Implement Self-service core, blueprint store, choice engine, quota reservation on the SQL counter Check: `go test ./pkg/selfservice/... ./pkg/quota/...` passes
- [ ] 2.2 Wire routes with authorization actions and audit entries Check: `curl POST /api/dustpan/v1/namespaces/{namespace}/self-service/requests` makes a unit
- [ ] 2.3 Run e2e for both tools through the real binary over TLS on SQLite and PostgreSQL Check: e2e step with real Pulumi CLI and real OpenTofu runs `pulumi up` and `tofu apply` on a runner

## 3. Definition of done

- [ ] 3.1 Check wiring, route actions, and policy Check: Route test lists each route with its action and the public list holds no signed route
- [ ] 3.2 Run e2e step against the real CLI and the real directory Check: e2e step 1 of milestone 6 passes on Linux on SQLite and PostgreSQL
- [ ] 3.3 Write the docs page with publish and request commands Check: `docs/guides/publish-a-blueprint.md` commands run as written
- [ ] 3.4 Run a usability check Check: Developer completes the path from the docs page alone and each error names the one command
- [ ] 3.5 Run make check with zero findings Check: `make check` reports zero findings
