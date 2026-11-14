## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add TestReducingRoutesMatchGateBothDirections in the NEW file pkg/maintenance/reducing_test.go against the existing ReducingRoutes names in pkg/maintenance/reducing.go. It covers gate against table, a server entry with no mapped control-path method, and a mapped method with no table entry. Check: `go test ./pkg/maintenance/ -run TestReducingRoutesMatchGateBothDirections -count=1` fails on main
- [ ] 1.2 Add TestReducingDocStatesTestOnly in pkg/maintenance/reducing_test.go. It reads pkg/maintenance/reducing.go and requires the runner-only production wording. Check: `go test ./pkg/maintenance/ -run TestReducingDocStatesTestOnly -count=1` fails on main

## 2. Move and wire

- [ ] 2.1 Move the table to NEW internal/maintenancegate/table.go, wire pkg/dustpan/maintenance.go through IsReducing for runner routes on the existing route GET /api/dustpan/v1/maintenance with an access token, reword the doc in pkg/maintenance/reducing.go, and move the two-way test with its entry-to-method map to internal/maintenancegate/table_test.go. Check: `rg -n ReducingRoute pkg internal cmd e2e` shows no export outside NEW internal/maintenancegate
- [ ] 2.2 Run `make sdk-surface` in the same change to refresh the checked-in snapshot file internal/tools/sdksurface/api.txt for the removed pkg exports. Check: `make sdk-surface` leaves no diff in internal/tools/sdksurface/api.txt

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the relocated two-way test, the constructor hostile cases in internal/maintenancegate/table_test.go, and the gate callers in pkg/dustpan. Run `make check` with zero findings. Confirmed by: CI. Check: `go test ./internal/maintenancegate/ ./pkg/maintenance/ ./pkg/dustpan/ -count=1` passes. Confirmed by: CI.
