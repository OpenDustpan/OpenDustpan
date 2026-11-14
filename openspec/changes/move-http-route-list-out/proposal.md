---
version: 1
---

Issue: #1993.

## Problem

The exported route-path table in pkg/maintenance breaks the SDK surface rule and its doc overstates what reads it, so an added entry looks like maintenance support when it is not.

## Evidence

- pkg/maintenance/reducing.go exports ReducingRoute and ReducingRoutes on main. Proof: pkg/maintenance/reducing.go:3:// ReducingRoute is one route that only reduces access and that therefore
- The reducing-route helpers split into runner and server entries on main. Proof: pkg/maintenance/reducing.go:38:// runnerReducingRoutes returns the entries of the runner management API.
- Main has a reducing runner-route check in pkg/dustpan. Proof: pkg/dustpan/maintenance_reducing_internal_test.go:17 (func TestIsReducingRunnerRoute)

## Fix boundary

- Move the ReducingRoute table and ReducingRoutes list from pkg/maintenance/reducing.go to the NEW package internal/maintenancegate.
- Keep only the existing Kind constants exported from pkg/maintenance.
- Reword the doc in pkg/maintenance/reducing.go: production reads only the runner entries, and the installation-API entries only check the store control path in a test.
- Check both directions between gate, table and mapped store control-path methods, plus malformed entries.

Out of scope:

- Change of maintenance behavior for any route
- Change of the store transaction that each installation API method uses
- Change of runner management API routes

## Check

- Add TestReducingRoutesMatchGateBothDirections in the NEW file pkg/maintenance/reducing_test.go against the existing ReducingRoutes names in pkg/maintenance/reducing.go. It covers gate against table, a server entry with no mapped control-path method, and a mapped method with no table entry. Check: `go test ./pkg/maintenance/ -run TestReducingRoutesMatchGateBothDirections -count=1` fails on main
- Add TestReducingDocStatesTestOnly in pkg/maintenance/reducing_test.go. It reads pkg/maintenance/reducing.go and requires the runner-only production wording. Check: `go test ./pkg/maintenance/ -run TestReducingDocStatesTestOnly -count=1` fails on main
- Move the table to NEW internal/maintenancegate/table.go, wire pkg/dustpan/maintenance.go through IsReducing for runner routes on the existing route GET /api/dustpan/v1/maintenance with an access token, reword the doc in pkg/maintenance/reducing.go, and move the two-way test with its entry-to-method map to internal/maintenancegate/table_test.go. Check: `rg -n ReducingRoute pkg internal cmd e2e` shows no export outside NEW internal/maintenancegate
- Run `make sdk-surface` in the same change to refresh the checked-in snapshot file internal/tools/sdksurface/api.txt for the removed pkg exports. Check: `make sdk-surface` leaves no diff in internal/tools/sdksurface/api.txt
- Run the relocated two-way test, the constructor hostile cases in internal/maintenancegate/table_test.go, and the gate callers in pkg/dustpan. Run `make check` with zero findings. Confirmed by: CI. Check: `go test ./internal/maintenancegate/ ./pkg/maintenance/ ./pkg/dustpan/ -count=1` passes. Confirmed by: CI.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main exports the reducing-route list constructor in pkg/maintenance. (`pkg/maintenance/reducing.go:34 (func ReducingRoutes)`)
- Main checks a reducing runner route in pkg/dustpan. (`pkg/dustpan/maintenance_reducing_internal_test.go:17 (func TestIsReducingRunnerRoute)`)
- Main covers maintenance-mode behavior of a store bind-provider method. (`pkg/store/storetest/bind_provider.go:30 (func testBindProviderInMaintenanceMode)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 272: The public SDK surface** (not owner-tagged).

- Decisions that apply: 4 (extended).
- Changed by: ADR 364 extends decision 4. Chain to the end: ADR 364.
- Current rule: this ADR, with ADR 364.
- Quote: "Any other package moves to `internal/`."
- Quote: "No exported signature in `pkg/` names a type from `internal/`."
- Effect: The METHOD path table holds adapter route strings and is not a port, so it moves to internal. pkg/dustpan calls a string-in bool-out predicate so no pkg signature names the new internal type.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: path `internal/tools/sdksurface/api.txt` (.tasks[1].items[1].text) does not exist in the checkout; use an existing path, or name it as new ("add the new file internal/tools/sdksurface/api.txt").

Questions considered and answered:

- Can the route table stay exported from pkg/maintenance? No. ADR 272 decision 4 moves non-port route tables to internal. The table holds METHOD and path strings of adapters, not a port type.
- Where does the table live without a cycle? The NEW package internal/maintenancegate holds the table next to the gate. pkg/dustpan calls IsReducing with strings, so no pkg signature names an internal type.
- Is a one-direction gate-against-table test enough? No. The issue asks for both directions: a table entry with no control-path store method, and a control-path store method with no table entry.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
