---
version: 1
---

## Problem

Automatic token disables change access with no record, so review cannot tell what happened or why.

## Evidence

- Token management routes exist on main. Proof: ANY /api/server/principals/{}/tokens where route table or API description
- The audit read path exists on main. Proof: ANY /api/audit/entries where route table or API description
- No trigger list, audit kind, or disable record rule is in the pack for automatic token disables. Proof: Coverage note lists only trigger-route gaps and the pack lists no token-disable trigger or audit kind.

## Fix boundary

- Write one audit entry with server actor and fixed reason for each automatic token disable, in the same transaction as the disable.
- Key the entry by the stable token ID and never write the secret value.

Out of scope:

- Job effective values
- Trigger gap and skipped-slot records
- Identity default documentation

## Check

- In NEW `internal/token/autodisable_audit_test.go` add failing test TestAutomaticTokenDisableWritesAuditEntry that forces an expiry disable and calls `ANY /api/audit/entries` with an access token past the audit read gate. Check: NEW internal/token/autodisable_audit_test.go TestAutomaticTokenDisableWritesAuditEntry fails on main
- In NEW `internal/token/autodisable_audit_test.go` add failing test TestAutomaticTokenDisableAuditHasNoSecret that calls `ANY /api/audit/entries` with an access token past the audit read gate and shows the entry carries the stable token ID and no secret value. Check: NEW internal/token/autodisable_audit_test.go TestAutomaticTokenDisableAuditHasNoSecret fails on main
- Wire the audit write into the automatic expiry-disable path in `internal/token`, document the entry on the docs page, and run the usability check of `ANY /api/audit/entries` with an access token past the audit read gate and `make check` with zero findings. Confirmed by: CI (`make check`) Check: `GET /api/audit/entries` with an access token past the audit read gate returns the disable entry

Depends on:

- no-magic-implicit-behavior-user-job-effective-values
- `no-magic-implicit-behavior-user-trigger-gap-records` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has audit-entry test helpers used by conformance suites. (`pkg/principal/principaltest/groupclaims.go:576 (func auditEntries)`)
- Main has audit-entry checks in the manual-lock suite. (`pkg/manuallock/manuallocktest/suite.go:507 (func testAuditEntries)`)
- Main has a token error catalog. (`internal/errcode/catalog_token.go:7 (token.already-exists)`)
- Main has the audit read API and the token management API. (`pkg/audit/audit.go:69 (audit kind audit-read)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which automatic-disable rules are in scope for this change and what fixed reason code does each rule write?
   - Options: Expiry only with reason code token-expired; Expiry plus idle disable and principal-disable cascade, each with its own reason code.
   - Provisional decision (owner review 14 Nov): Expiry only with reason code token-expired, and later changes add each further rule with its own reason code.
   - Reason: The pack names no trigger for an automatic token disable, so the spec cannot name the covered rules or test them. Benchmark: AWS IAM access-key last-used and deactivation records use a fixed event name plus the rule that fired.
   - Persona impact: Expiry only upsets no persona; bulk cascades add entry volume for the site administrator. Escape hatch: Administrators keep the existing token list and audit list; no new search view ships in this change.
2. What exact server actor string and reason object do automatic token-disable entries use?
   - Options: Block on the job part until it defines the server actor string and the reason object; Define actor server and reason token-expired in this change and reuse it in the job part.
   - Provisional decision (owner review 14 Nov): Block on the job part until it defines the server actor string and the reason object, then reuse it here.
   - Reason: The roadmap says this part reuses the actor and reason shape from the job part, but that shape is not in the pack. Benchmark: PCI DSS requires the log to name the actor and the reason for each access change in a stable format.
   - Persona impact: A stable actor and reason upset no persona; two formats upset the site administrator who reads the log. Escape hatch: Readers keep filtering by stable token ID through the existing audit list.

Questions considered and answered:

- Which actor and reason shape do automatic token-disable records use? No ADR text is in the pack, so no ADR answers it. The split roadmap states this part reuses the actor and reason shape from the job part, but the shape is not in the pack, so the exact strings stay an open question.
- Which automatic-disable rules are in scope and does the disable roll back when the audit write fails? The pack lists token routes and the audit read route but no disable trigger, no audit kind for disables, and no transaction rule. The trigger list and the atomicity choice stay open questions.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
