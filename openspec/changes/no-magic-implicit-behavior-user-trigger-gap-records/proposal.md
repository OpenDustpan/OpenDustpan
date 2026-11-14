---
version: 1
---

## Problem

A namespace administrator sets a schedule trigger and a plan does not run, with no record of the skipped slot or the replaced plan, so the gap looks like silent behavior.

## Evidence

- Trigger records are read and written through the unit trigger routes on main. Proof: ANY/GET/POST /api/dustpan/v1/namespaces/{}/units/{}/triggers
- One trigger is addressed through the single-trigger routes on main. Proof: DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/triggers/{}
- Trigger domain package exists on main. Proof: pkg/trigger
- Audit entries are read through the audit entries routes on main. Proof: ANY /api/audit/entries

## Fix boundary

- Store evaluated trigger paths and show schedule next run time where the trigger record is read.
- Write audit entries for skipped slots and replaced plans with server actor and reason.

Out of scope:

- Job effective values
- Automatic token disable records
- Identity default documentation

## Check

- Add failing read-model test for trigger path and next run time in NEW pkg/trigger/gapread_test.go:1. A person's step. Check: NEW pkg/trigger/gapread_test.go TestTriggerReadShowsPathAndNextRun fails on main, passes after the change.
- Add failing audit test for a skipped schedule slot with server actor and reason in NEW pkg/trigger/gapskip_test.go:1. A person's step. Check: NEW pkg/trigger/gapskip_test.go TestSkippedSlotWritesAuditEntry fails on main, passes after the change.
- Add failing audit test for a replaced stored plan with server actor and reason in NEW pkg/trigger/gapreplace_test.go:1. A person's step. Check: NEW pkg/trigger/gapreplace_test.go TestReplacedPlanWritesAuditEntry fails on main, passes after the change.
- Wire path and next run time into the trigger read path in pkg/trigger:1 using existing route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} with its authorization gate and an access token. A person's step. Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} returns path and next run time with an access token.
- Wire skip and replace audit writes into the scheduler path in NEW pkg/trigger/gaprecord.go:1 using existing route GET /api/audit/entries with its authorization gate and an access token. A person's step. Check: GET /api/audit/entries lists the skip and replace entries with an access token.
- Add trigger gap records docs page in NEW docs/reference/trigger-gaps.md:1 covering path, next run time, and audit reasons. A person's step. Check: Docs page NEW docs/reference/trigger-gaps.md:1 renders the gap record fields.
- Add e2e trigger read step in NEW e2e/triggergaps_test.go:1 calling existing route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with its authorization gate and an access token. A person's step. Check: E2E step reads GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with an access token.
- Run usability check of the trigger gap path from NEW docs/reference/trigger-gaps.md:1 and record where the path still costs time. A person's step. Check: Usability check in NEW docs/reference/trigger-gaps.md:1 completes the namespace administrator path from the docs page alone.

Depends on:

- `no-magic-implicit-behavior-user-job-effective-values` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Audit ship suite counts skipped entries and moves the cursor on main. (`pkg/audit/ship_test.go:345 (func TestASkippedEntryIsCountedAndTheCursorMoves)`)
- Postgres store suite frees a long-connection slot on release on main. (`pkg/store/postgres/long_path_internal_test.go:253 (func TestReleaseOfStalledLongConnFreesTheSlot)`)
- Durable test suite covers in-memory schedules on main. (`pkg/durable/durabletest/durabletest_test.go:35 (func TestMemSchedules)`)
- Grant test covers refusal of the namespace service record on main. (`pkg/access/namespace_service_grant_test.go:16 (func TestGrantRefusesTheNamespaceServiceRecord)`)
- Audit lag suite grows entries while the receiver is down on main. (`pkg/audit/lag_test.go:67 (func TestLagEntriesGrowsWhileTheReceiverIsDown)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Where do automatic trigger decisions become visible? Trigger reads show path and next run time; skipped slots and replaced plans show as audit entries. No stored history list is added.
- Who is recorded as the actor of an automatic skip or replace? No ADR text in the pack touches triggers or audit, so the pack answers it: the record carries server actor and reason per the no-magic rule to write who did what and why.
- How long are skipped-slot audit entries kept? Provisional decision: same retention as other namespace audit entries; no separate retention number is set, so no open question remains.
- What is schedule next run time when a trigger cannot fire? Null next run time. A trigger with no schedule, a disabled trigger, or a schedule that can never fire returns null for next run time.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
