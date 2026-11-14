## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. tests and fix

- [ ] 1.1 Add failing readiness tests with a fake clock for api-only, worker-only, and full servers in pkg/dustpan. Check: NEW test TestApiOnlyWithDurableStoreStaysReady in pkg/dustpan fails on main with 503 claim-loop after 60 s fake time.
- [ ] 1.2 Gate the claim-loop check on the workerEnabled flag in pkg/dustpan. Check: NEW tests pass, TestClaimLoopRan still passes, and a mutation test that removes the workerEnabled guard fails.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. definition of done

- [ ] 3.1 Wire the gated check into the Start, Run and readiness path of the server. Check: `curl -s http://localhost:8080/health/ready` answers 200 for api-only and healthy worker servers on route GET /health/ready, and 503 only for worker and full layouts with a stuck loop.
- [ ] 3.2 Document the component rule for embedders on the existing SDK docs page. Check: The docs page shows api-only, worker-only and full layouts with the readiness rule.
- [ ] 3.3 Run full verification Confirmed by: CI (make check) Check: `make check` with zero findings.
