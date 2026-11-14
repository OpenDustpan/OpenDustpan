---
version: 1
---

Issue: #2590.

## Problem

The EventCredentialsIssued rule has no writer, so the audit log has no record of runner credential issuance.

## Evidence

- EventCredentialsIssued exists as an audit event constant. Proof: pkg/runner/audit.go:42
- AuditRules defines the runner audit rules. Proof: pkg/runner/audit.go:61 (func AuditRules)
- A producer test checks that each audit event has a producer. Proof: pkg/runner/handoff_test.go:158
- The issue states the producer test lists EventCredentialsIssued in an explicit unproduced set. Proof: Issue #2590 body: The producer test lists EventCredentialsIssued in an explicit "unproduced" set.

## Fix boundary

- Write one EventCredentialsIssued audit entry at each place the server issues runner credentials.
- Add a hostile test that issuance without the audit write fails.
- Remove EventCredentialsIssued from the unproduced set in the producer test.

Out of scope:

- Changes to runner credential format or lifetime.
- Changes to other unproduced audit events.

## Check

- Add failing producer and hostile issuance audit tests in pkg/runner. Check: TestEveryAuditEventHasAProducer fails on main and passes after the fix.
- Write the entry in the NEW IssueCredentials path and remove EventCredentialsIssued from the unproduced set. Check: `rg EventCredentialsIssued pkg/runner/handoff_test.go` shows no unproduced entry and `go test ./pkg/runner/ -count=1` passes.
- Wire the writer into the served issuance path and verify the audit entry. Check: Server test calls ANY /runner/v1 issuance with a runner credential and finds the audit entry.
- Run the full checks and usability review of the audit entry. Confirmed by: CI (make check). Check: `make check` passes with zero findings.

Depends on:

- `runner-exchange-no-denial-limiter` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Runner audit catalog defines EventCredentialsIssued. (`pkg/runner/audit.go:42`)
- Runner audit catalog defines its audit rules. (`pkg/runner/audit.go:61 (func AuditRules)`)
- A producer test verifies that each runner audit event has a producer. (`pkg/runner/handoff_test.go:158 (func TestEveryAuditEventHasAProducer)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which ADR controls the missing audit entry? No ADR in the pack touches runner audit events. The pack adrs list is empty. The fix follows the project convention that each automatic action writes a record, and issue #2590 directs the writer placement.
- What happens when the audit write fails? The entry write and the credential store occur in one transaction. On audit write failure the transaction rolls back and issuance is refused with the existing code runner.invalid.
- What does the entry contain? The entry stores actor, object, credential ID, expiry and reason. It never stores the secret credential value.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
