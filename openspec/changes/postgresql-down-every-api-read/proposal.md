---
version: 1
---

Issue: #2344.

## Problem

With PostgreSQL down, every API read answers in maintenance mode and worker WARN lines say only error.

## Evidence

- With PostgreSQL down most routes answer maintenance.in-maintenance while the installation holds normal mode. Proof: Issue #2344 Actual: Most routes answer 503 maintenance.in-maintenance; the installation is in normal mode.
- Only server routes answer namespace.unavailable. Proof: Issue #2344 Actual: Only /api/server/* routes answer namespace.unavailable.
- Worker lines carry error error with no cause class. Proof: Issue #2344 Actual: durable operation claim failed ... error:error.
- The code defines both codes separately. Proof: internal/errcode/catalog_maintenance.go:14 and internal/errcode/catalog_namespace.go:15.
- No ADR in the pack answers the mapping. Proof: Context pack adrs: [] and rfcs: [].

## Fix boundary

- Reads answer namespace.unavailable with Retry-After when the store is unavailable, not maintenance.in-maintenance.
- Tool state reads keep their contract shapes through the same classifier.
- Worker claim and schedule WARN lines name the cause class without driver text.

Out of scope:

- Retry policy changes
- New readiness probe
- Restore behavior changes

## Check

- Add a failing server test in the NEW file internal/servertest/pgdown_test.go that stops PostgreSQL and calls the existing route `GET /api/dustpan/v1/maintenance` with credential kind `access token` and passes its existing authorization gate, and expects namespace.unavailable with Retry-After. It covers the NEW mapStoreError. Declaration and adapter files: internal/api/problem/problem.go and internal/api/server. Check: TestPgDownReadAnswersUnavailable. Confirmed by: CI (make test).
- Add a failing e2e step in the NEW file e2e/pgdown_test.go that calls the existing route `ANY /api/inventory/unit-collection` with credential kind `access token` and passes its existing authorization gate, using the real Pulumi CLI for Pulumi state routes and the real OpenTofu CLI for TFE state routes, and checks the 503 contract shapes. Check: TestPgDownToolReadsAnswerUnavailable. Confirmed by: CI (make e2e).
- Add a failing worker test in the NEW file pkg/job/jobtest/pgdown_test.go for the NEW logCauseClass: claim and schedule failures log database-unavailable, and a hostile error omits host, DSN and credential. Check: TestPgDownWorkerLogNamesCauseClass. Confirmed by: CI (make test).
- Add the NEW func CauseClass in the NEW file internal/errcode/cause_class.go with the allowlist, and map it to namespace.unavailable in internal/api/problem/problem.go near line 373. Check: TestPgDownReadAnswersUnavailable. Confirmed by: CI (make test).
- Route worker claim and schedule store errors through CauseClass in the NEW file pkg/job/cause_log.go. Check: TestPgDownWorkerLogNamesCauseClass. Confirmed by: CI (make test).
- Wire the mapping on the existing route ANY /api/inventory/unit-collection with an access token, authorization actions unchanged, with a harness PostgreSQL stop and restart and recovery without a server restart. Check: `make e2e` PostgreSQL stop and restart step. Confirmed by: CI (make e2e).
- Update the docs error-code entry for namespace.unavailable with the cause, the Retry-After hint and the one command. Check: `make docs-lint`. Confirmed by: CI (make docs-lint).
- Run the usability check from the docs page and the full checks. Check: `make check` with zero findings. Confirmed by: the driver's gates.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines error code maintenance.in-maintenance. (`internal/errcode/catalog_maintenance.go:14`)
- Main defines error code namespace.unavailable. (`internal/errcode/catalog_namespace.go:15`)
- Main has a cause class helper for audit shipping. (`pkg/audit/ship.go:1215 (func causeClass)`)
- Main keeps the problem note that maps a wait to namespace.unavailable. (`internal/api/problem/problem.go:373`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Is any ADR touched? No. The pack lists no ADRs or RFCs, so the issue text decides the mapping.
- Which code on a failed mode read? namespace.unavailable; maintenance.in-maintenance only when the mode read succeeds and holds maintenance.
- Where does the classifier live? NEW func CauseClass in internal/errcode. The audit helper at pkg/audit/ship.go:1215 stays for audit shipping.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
