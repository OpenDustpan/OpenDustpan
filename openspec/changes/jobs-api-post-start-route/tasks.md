## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add NEW consistency test pkg/job/startactions_declared_test.go that compares the declared union with startActionFor and startAction in pkg/job/actions.go for each ExecutorKind value. Check: NEW pkg/job/startactions_declared_test.go TestStartActionsMatchDeclaration fails on main: declared set misses unit:run-apply.
- [ ] 1.2 After the route-guard dependency lands, update the jobs POST start route row in internal/api/route and its operation text to declare the union {unit:run-plan, unit:run-apply} with the per-kind wording. Check: Route test through the route.Enforce path shows the union and NEW TestStartActionsMatchDeclaration passes.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Regenerate the checked-in API description document and run the route and API description tests. Check: Route policy check and pkg/dustpan/apidesc_reads_test.go:39 pass. Confirmed by: CI (route and apidesc tests).
- [ ] 3.2 Update the jobs API docs page for the start operation. Check: Developer reads the jobs docs page and sees both start actions with the per-kind wording. Confirmed by: CI (docs page check).
- [ ] 3.3 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (make check).
