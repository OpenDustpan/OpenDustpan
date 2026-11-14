---
version: 1
---

Issue: #2125, #2127, #2207.

## Problem

An operator enters maintenance before a restore and expects the check to stay fast as stacks grow. Today activeUpdates reads ActiveUpdate once per stack and twice per restore, and backup reads each state version one row at a time, so round trips grow with stacks and history.

## Evidence

- activeUpdates lives in pkg/maintenance and is called from the maintenance service. Proof: pkg/maintenance/maintenance.go:653:		updates, err := s.activeUpdates(ctx, ns)
- activeUpdates returns the running mutating updates of one namespace. Proof: pkg/maintenance/maintenance.go:674:func (s *Service) activeUpdates(ctx context.Context, ns namespace.Name) (activeWork, error) {
- The store port has single-row ActiveUpdate and the storetest calls it per ref. Proof: pkg/store/storetest/updates.go:634:	active, err := f.s.ActiveUpdate(t.Context(), f.ref)

## Fix boundary

- Add NEW port method ListActiveUpdates for the active updates of a namespace and use it in activeUpdates, so 3 and 30 stacks give the same number of maintenance reads.
- Page backup and restore state-version reads by id cursor with page size 200 and one read per workspace and per secret key, and remove the allowance in TestRoundTripsOfBackup.
- Keep the maintenance.busy refusal and authorization on route ANY/POST /api/dustpan/v1/maintenance/enter with no new route.
- Embedders with a third-party store add the NEW ListActiveUpdates method to match the updated port.

Out of scope:

- Console changes.
- New backup format or migration.
- Parsed minimum-version cache of #2207; that issue stays open and triage decides it separately.

## Check

- Add failing conformance cases for NEW ListActiveUpdates in pkg/store/storetest/updates.go:97, ordered by stack ID with empty-namespace, deleted-stack, and failure cases. Check: `go test ./pkg/store/storetest -run TestActiveUpdatesBatch -count=1` fails on main (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)
- Add failing round-trip cases for paged backup and restore reads in internal/servertest/roundtrips_test.go:1144 with 3 and 30 stacks. Check: `go test ./internal/servertest -run TestRoundTripsOfBackup -count=1` fails on main (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)
- Implement NEW ListActiveUpdates in both store adapters (pkg/store/sqlite, pkg/store/postgres) and use it in activeUpdates in pkg/maintenance/maintenance.go:674. Check: `go test ./pkg/store/... -count=1` passes (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)
- Page backup and restore state-version reads by id cursor with page size 200 in pkg/backup/portable_read.go:54 (func ReadPortableTable) and the restore copy path, read each workspace and secret key once, and remove the allowance in TestRoundTripsOfBackup. Check: `go test ./internal/servertest -run TestRoundTripsOfBackup -count=1` passes with no allowance (acceptance path: existing route ANY/POST /api/dustpan/v1/maintenance/enter, maintenance gate, access token)
- Wire the batch read through the existing route ANY/POST /api/dustpan/v1/maintenance/enter with its gate and access token, with no new route. Check: The existing route ANY/POST /api/dustpan/v1/maintenance/enter keeps maintenance.busy on active work and maintenance.unavailable on batch failure with an access token and its maintenance gate
- Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Service.activeUpdates returns the running mutating updates of one namespace. (`pkg/maintenance/maintenance.go:674 (func Service.activeUpdates)`)
- Store port defines single-row ActiveUpdate. (`pkg/stack/updates.go:79 (interface method ActiveUpdate)`)
- Round-trip test names the backup N+1 allowance. (`internal/servertest/roundtrips_test.go:1144 (test TestRoundTripsOfBackup)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR forbid a batch read on the store port? No. The pack lists no ADR text. Searched docs/adr and docs/rfc with rg for activeUpdates, ActiveUpdate, ListActiveUpdates, batch read: no decision forbids it. The port rule comes from CLAUDE.md: a NEW port method needs conformance cases in pkg/store/storetest before both adapters implement it.
- Does the fix change the maintenance refusal or the backup format? No. Error codes maintenance.busy and maintenance.unavailable stay. Only the read pattern changes from one read per stack to one batch read.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
