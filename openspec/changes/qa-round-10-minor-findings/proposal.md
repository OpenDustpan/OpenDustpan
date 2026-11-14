---
version: 1
---

Issue: #2411.

## Problem

Operator tries to run a clean installation from the docs, but QA round 10 shows small mismatches that confuse scripts and hide bad input.

## Evidence

- The error code restore.ended exists on main Proof: rg restore.ended matches internal/errcode/catalog_restore.go and internal/api/apidesc/openapi.json
- QA found U+FE0F, NBSP, and invalid UTF-8 pass the shared hidden-character set Proof: Issue #2411 item 1 names temporary-administrator reason, job location repository, and deleted-unit cursor
- QA found text/plain creates a namespace with 201 Proof: Issue #2411 item 4

## Fix boundary

- Refuse U+FE0F, NBSP, and invalid UTF-8 in the shared hidden-character set
- Refuse person names that differ only in case
- Replace internal lock and drift-start text with public error text and cap echoed version text
- Refuse text/plain namespace creates with 415
- Fix release-script messages for pre-release tags and CRLF records and fix ended-restore cancel

Out of scope:

- Copies of the hidden-character check covered by issue #2404
- PostgreSQL-down behavior covered by issue #2344
- Fleet lifecycle, engineering-health insights, and other backlog items

## Check

- Add failing test for U+FE0F, NBSP, and invalid UTF-8 in temporary-administrator reason and job location repository in internal/api/server/passkeys_test.go:428 Check: TestPasskeyNameWithHiddenCharacters
- Add failing test for hidden characters in deleted-unit cursor in pkg/access/review_cursor_internal_test.go:15 Check: TestDecodeReviewCursorBounds
- Add failing test for case-only duplicate person name in pkg/access/access_test.go:792 Check: TestGrantChecksSubjectAndNamespace
- Add failing test for cancel of succeeded restore as 409 restore.ended and second cancel as 202 in pkg/backup/service_durable_test.go:577 Check: TestStartRefusesAnInvalidID
- Add failing test for text/plain namespace create as 415 with no stored row in pkg/dustpan/bodygate_test.go:132 Check: TestSharedBodyGateAcrossAdapters
- Fix the shared hidden-character set, person-name folding, public lock and drift-start answers, and ended-restore cancel in internal/errcode/catalog_restore.go:30 Check: TestStartRefusesAnInvalidID
- Fix 415 on POST /api/server/namespaces and the version echo cap in pkg/dustpan/bodygate_test.go:132 Check: TestSharedBodyGateAcrossAdapters
- Fix the restore cancel and artifact-sweep wording in the NEW docs/guides/qa-round-10-fixes.md page Check: `rg -n "restore.ended" docs/guides/qa-round-10-fixes.md`

Depends on:

- hidden-character-checks-13-copies
- qa-round-9-small-mismatches
- re-enable-migration-api-version
- `hidden-character-checks-13-copies` (order of the milestone review)
- `qa-round-9-small-mismatches` (order of the milestone review)
- `re-enable-migration-api-version` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The error code restore.ended exists (`internal/errcode/catalog_restore.go:30 (error code restore.ended)`)
- The maintenance error code exists (`internal/errcode/catalog_maintenance.go:14 (error code maintenance.in-maintenance)`)
- A shared body gate exists across adapters (`pkg/dustpan/bodygate_test.go:132 (func TestSharedBodyGateAcrossAdapters)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide hidden characters, error text, or body checks? No ADR is listed in the pack, so no ADR answers them; the fix stays inside issue #2411.
- Should this change fix the 13 copies of the hidden-character check? No. Issue #2411 says issue #2404 covers the copies; this change fixes the shared set content only and the three QA paths keep calling the shared set.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
