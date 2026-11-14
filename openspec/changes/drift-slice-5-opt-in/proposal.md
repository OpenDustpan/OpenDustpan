---
version: 1
---

Issue: #1184.

## Problem

A namespace administrator tries to turn drift checks on for a stack or workspace, or to set a default for a collection or a namespace, so that checks start on their own with no manual run.

## Evidence

- drift.Checker exists on main and a service implements it. Proof: internal/driftsvc/driftsvc.go:31:var _ drift.Checker = (*Service)(nil)
- drift.PolicyStore is a port that the conformance harness uses. Proof: pkg/drift/drifttest/drifttest.go:64:	Policies   drift.PolicyStore
- No Scheduled method of drift.Checker exists on main. Proof: rg -n -F --glob '!*_test.go' 'drift.Checker.Scheduled' pkg internal cmd gives 0 matches
- The preset constants exist on main. Proof: pkg/drift/policy.go:27:	PresetHourly      Preset = "hourly"

## Fix boundary

- NEW SQL adapters of drift.PolicyStore in both dialects, with the baseline migration edited in place and drifttest run by both
- NEW routes under /api/dustpan/v1 to read, set and delete the drift schedule of a unit, a collection and a namespace, and to read the effective schedule of a unit
- NEW drift.Checker.Scheduled with the source kind schedule, a first check at an offset from a hash of the unit ID, and a pause after 5 failed checks in a row
- NEW notification type unit.drift-schedule-paused, with a severity and a row in the notification catalog, sent when a schedule pauses
- NEW error codes drift.invalid-schedule, drift.schedule-forbidden and drift.schedule-not-found
- Docs page: off by default, and a remediation needs a person to approve

Out of scope:

- Drift slice 1 check execution and runner plan reduction
- Automatic remediation and ignore records
- The notification of a drift status change (types unit.drifted and unit.drift-cleared)
- Run triggers beyond the schedule kind

## Check

- Write PolicyStore conformance cases for put, get, delete, effective order, restart of the pause state and delete of the object in pkg/drift/drifttest/policy.go. Check: `go test ./pkg/drift/drifttest/ -run TestPolicy -count=1` fails on main
- Write Scheduled cases in the NEW file internal/driftsvc/scheduled_test.go: off starts nothing, on starts the source kind schedule, 5 failures pause and send unit.drift-schedule-paused, a lock refusal counts 0, a clean check resets, the hashed offset, the 100-start limit, one start for two claims of a slot. Check: `go test ./internal/driftsvc/ -run TestScheduled -count=1` fails on main
- Write route cases in the NEW file internal/api/drift/schedule_test.go for 400, 403, 404, the 256-byte cron limit, a cron with a gap under 1 hour and the explicit off. They call the NEW routes with an access token. Check: `go test ./internal/api/drift/ -run TestSchedule -count=1` fails on main
- Add the SQL adapters in pkg/store/sqlite and pkg/store/postgres, edit the baseline migrations and queries.sql, and run drifttest through pkg/store/storetest/drift.go. Check: `go test ./pkg/store/... -run TestDrift -count=1 -timeout 60m` passes
- Add the NEW routes in internal/api/drift/drift.go with the settings action of the object. Check: `go test ./internal/api/drift/ -run TestSchedule -count=1` passes
- Add Scheduled in internal/driftsvc/driftsvc.go on the durable schedule claim, with the existing source kind schedule of pkg/job/source.go, and the pause notification of the NEW type unit.drift-schedule-paused. Check: `go test ./internal/driftsvc/ -run TestScheduled -count=1` passes
- Wire in pkg/dustpan/drift.go. Add the e2e step in e2e/ with the real CLI. It sets the schedule with PUT on the NEW unit schedule route, then reads GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift, with an access token that holds the settings action. Add the docs page and run the usability check from the page alone. Check: `make e2e` shows a scheduled check for the unit with the setting and none for the other unit
- Run all checks. Check: `make check` reports zero findings

Depends on:

- Drift slice 1 checks and drift.Checker
- The durable schedule and cron parser
- The source kind schedule in job.StartRequest
- The decision on the trigger object
- `drift-slice-6-notifications-when` (order of the milestone review)
- `inventory-api-spells-unitid-210` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The drift.Checker port exists with request validation. (`pkg/drift/ports.go:122 (func CheckRequest.Validate)`)
- The drift.PolicyStore port exists. (`pkg/drift/ports.go:58 (type PolicyStore)`)
- The memory policy helpers exist in drifttest. (`pkg/drift/drifttest/policy.go:24 (func daily)`)
- Job source kinds parse through ParseSourceKind. (`pkg/job/source.go:47 (func ParseSourceKind)`)
- The cron presets exist. (`internal/cron/preset.go:14 (const PresetHourly)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 263: Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule** (not owner-tagged).

- Decisions that apply: 5 (current), 14 (current), 15 (current), 16 (current), 17 (current).
- Changed by: ADR 354 extends; ADR 358 extends decision 7; ADR 366 changes decision 12. Chain to the end: ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 354, 358, 366.
- Quote: "The schedule reuses the durable schedule of issue 909. One cron parser serves both."
- Quote: "The schedule of a unit pauses after 5 failed checks in a row. The server sends a notification when it pauses."
- Effect: The slice makes the opt-in schedule with three levels, the presets, the cron rules, the pause and its notification. It adds no action.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Effect: The docs page ships with the routes.
- Conflict: none.

**ADR 258: Drift remediation is a plan that a person approves, by a built-in gate** (not owner-tagged).

- Decisions that apply: 1 (extended), 3 (changed).
- Changed by: ADR 263 changes decision 3; ADR 263 extends decisions 1 and 6. Chain to the end: ADR 263, ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 263.
- Quote: "Detection changes nothing."
- Effect: A scheduled check only plans and waits for no approval. A remediation needs a person.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "ADR 354 extends: triggers and drift schedules carry the delegation."
- Effect: A scheduled start carries the delegation of the namespace service principal. If that principal is missing or lacks the run action, the start fails closed and counts as a failed check.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: ADR 354: the quote "ADR 354 extends: triggers and drift schedules carry the delegation." is not in the ADR file (copy it character for character from the decision text of the pack).

Questions considered and answered:

- Which model turns the schedule on? ADR 263 decisions 5 and 14 to 17: off by default, three levels, durable schedule, pause after 5 failures with a notification.
- Does a scheduled check wait for approval? No. ADR 258 decisions 1 and 3: the check only plans.
- Which action do the routes use? ADR 263 decision 17: the settings action of the object. The routes add no action.
- Does a lock-held failure count toward the pause? No. A manual lock is a person's choice. Only a job that ran and failed counts.
- A unit moves to another collection, or has no collection. The next evaluation reads the new collection. Owner decision, 13 Nov 2026 (queue P5): a new namespace has a stored default collection, so its default applies like any collection. A unit in no collection reads the namespace level.
- A unit, collection or namespace is deleted. The policy row stores the stable ID of the object and is deleted in the same transaction as the object.
- What is the name of the pause notification type? unit.drift-schedule-paused. The drift status change slice adds unit.drifted and unit.drift-cleared. The three names differ.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
