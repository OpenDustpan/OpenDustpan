## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 In `pkg/dustpan/apidesc_internal_test.go` add failing guard cases for a named handler type, a pointer-receiver handler type, an embedded field, and a negative case for an exclusion-listed non-route field. Check: TestEveryHandlerFieldIsListed fails on main for a wrapper handler type and for a pointer-receiver handler type without rows.
- [ ] 1.2 In `pkg/dustpan/apidesc_internal_test.go` widen the guard to each field type that implements the handler interface with the exclusion list rule. Check: `go test ./pkg/dustpan -run TestEveryHandlerFieldIsListed -count=1` passes.
- [ ] 1.3 In `pkg/dustpan/dustpan.go` keep the profiles handler in neutralHandlers, remove the adapters copy, and fix wiring with a startup nil check. Check: `go test ./pkg/dustpan -run TestEveryHandlerFieldIsListed -count=1` passes with one profiles field and startup fails on a nil profiles handler.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run the guard tests for the touched path, covering the existing route GET /api/dustpan/v1/openapi.json with an access token; no new route, so no new authorization action. Check: `go test ./pkg/dustpan -run TestEveryHandlerFieldIsListed -count=1` passes on the touched path.
- [ ] 3.2 Complete the usability check for the unchanged read path of the API description. A person's step. Check: Persona reads the API reference docs page alone with no surprise; path costs no extra step. Confirmed by: docs page read-through.
- [ ] 3.3 Run `make check` with zero findings. Check: `make check` passes with zero findings. Confirmed by: CI (make check).
