---
version: 1
---

Issue: #1382.

## Problem

A webhook URL with userinfo or a blocked scheme must fail fast with a clear refusal, and a corrupt legacy row must fail closed rather than send to an empty target.

## Evidence

- The production sealer method OpenDestination exists on main. Proof: pkg/notify/seal.go:217 (func SinkSealer.OpenDestination)
- The production check exists on main next to the sealer. Proof: pkg/notify/seal.go:273
- The test helper checkDestination exists on main and calls OpenDestination. Proof: pkg/notify/notifytest/sinkurl.go:213 (func checkDestination)
- The sink test route exists on main. Proof: POST /api/dustpan/v1/namespaces/{}/sinks/{}/test
- The sink.invalid code exists on main. Proof: internal/errcode/catalog_sink.go:15

## Fix boundary

- Refuse scheme outside http and https and any userinfo in the production checkDestination with ErrNoURL, mapped to sink.invalid at the sink handler.
- Fix the SinkSealer.OpenDestination doc comment wrap.
- Omit a migrated legacy userinfo row with an empty target from the list; fail a restore that stages it with ErrCorrupt and name the sink.
- Add export and stage tests of a migrated database with a realistic keyed sink on sqlite and postgres.

Out of scope:

- New sink kinds or new sender behavior
- Change to the sink allowlist beyond http and https plus userinfo refusal

## Check

- Edit pkg/notify/seal.go:217 (func SinkSealer.OpenDestination) to add failing hostile tests for plain, percent-encoded, uppercase-scheme and whitespace forms. Check: NEW test TestSinkSealerRefusesUserinfoAndScheme fails on main, passes after the fix.
- Edit pkg/store/storetest/backup.go:1585 (func mustListRestores) to add migration tests with a realistic keyed sink, export and stage, on both dialects. Check: NEW test TestMigratedSinkExportStage fails on main and passes after the fix.
- Edit pkg/notify/seal.go:217 (func SinkSealer.OpenDestination) to refuse scheme and userinfo in checkDestination and fix the OpenDestination doc wrap. Check: POST /api/dustpan/v1/namespaces/{}/sinks with an access token and a userinfo URL answers sink.invalid.
- Edit pkg/notify/seal.go:217 (func SinkSealer.OpenDestination) and internal/api/server/restores.go:296 (func Handler.listRestores) to wire the empty-target refusal into list and RestoreService.Apply. Check: GET /api/dustpan/v1/namespaces/{}/sinks with an access token omits the row; ANY /api/server/restores with an access token fails with ErrCorrupt.
- Update the webhook sink docs page with the http and https rule. Check: Docs page states that userinfo is never stored. Confirmed by: CI (make docs-lint).
- Run the definition of done: wiring, route, docs, usability check, make check. Check: make check passes with zero findings. Confirmed by: CI (make check).

Depends on:

- restore-documents-give-failed-restore
- `restore-documents-give-failed-restore` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main opens a sealed sink destination through SinkSealer.OpenDestination. (`pkg/notify/seal.go:217 (func SinkSealer.OpenDestination)`)
- Main covers a mutated restore target that cannot apply. (`pkg/backup/restore_service_test.go:979 (TestRestoreMutatedTargetCannotApply)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide webhook URL sealing or sink migration? No ADR is in the pack. The fix stays inside the sealing boundary and keeps the ErrNoURL shape.
- Should the migration or the restore own the refusal of a legacy userinfo row? Restore owns the refusal. Migration keeps the empty target, list omits the row, open fails with ErrNoURL, and a staged restore fails with ErrCorrupt. The row failed before migration too, so fail closed beats silent drop.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
