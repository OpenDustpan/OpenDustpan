---
version: 1
---

Issue: #1308.

## Problem

Site administrator reviews who changed a job and why, but claim, release, phase failed, and finish events applied by the job claimer leave no audit entries.

## Evidence

- The job state defines EventClaim and EventRelease. Proof: pkg/job/state.go:106 (func State.live), pkg/job/state.go:110 (func State.live)
- The job state defines EventPhaseFailed. Proof: pkg/job/state.go:130 (func State.live)
- A runner release path goes through the claimer. Proof: TestReleaseGoesThroughTheClaimer in internal/api/runnerapi/protocol_review_test.go:22

## Fix boundary

- Write one audit entry per covered claimer-applied event in the same transaction as the state commit.
- Use the stable runner ID as subject for runner events and the server actor with reason for server-driven events.

Out of scope:

- New audit kinds outside claim, release, phase failed, and finish events
- Changes to runner authentication

## Check

- Add failing server test TestClaimerClaimWritesAuditEntry in internal/api/runnerapi/protocol_review_test.go calling ANY /runner/v1 with a runner token through the claimer gate. Check: TestClaimerClaimWritesAuditEntry fails on main, calling ANY /runner/v1 with a runner token through the claimer gate.
- Add failing server tests for release, phase failed, and finish entries in the NEW file internal/api/runnerapi/claimer_audit_test.go calling ANY /runner/v1 with a runner token through the claimer gate. Check: TestClaimerReleaseWritesAuditEntry, TestClaimerPhaseFailedWritesAuditEntry and TestClaimerFinishWritesAuditEntry fail on main, each calling ANY /runner/v1 with a runner token.
- Add failing server test for server-driven entries with reason lease-expiry, restart-retry, or server-release calling ANY /runner/v1 with a runner token through the claimer gate. Check: TestClaimerServerDrivenWritesAuditEntry fails on main in internal/api/runnerapi/claimer_audit_test.go, calling ANY /runner/v1 with a runner token through the claimer gate for the server-driven path.
- Write entries in the claimer service at commit time using the event mapping of pkg/job/state.go. Check: All five tests pass with the runner ID or server actor as subject.
- Keep the existing read route ANY /api/audit/entries and show the claimer entries through it. Check: Route test shows ANY /runner/v1 with its claimer gate and ANY /api/audit/entries with its read actions.
- Add an e2e step that claims and finishes work through ANY /runner/v1 with a runner token, then lists entries at ANY /api/audit/entries with an access token. Confirmed by: CI (make e2e). Check: E2E step lists the claimer entries.
- Write the docs page note for claimer audit entries. Check: Docs page names the covered events, the subject, and the read route ANY /api/audit/entries.
- Run the usability check of the read path: the site administrator lists entries from the docs page alone. A person's step. Check: Each denial names the one grant command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Job state defines EventClaim. (`pkg/job/state.go:106 (func State.live)`)
- Job state defines EventRelease. (`pkg/job/state.go:110 (func State.live)`)
- Job state defines EventPhaseFailed. (`pkg/job/state.go:130 (func State.live)`)
- Audit entries convert through toAuditEntry. (`internal/api/audit/audit.go:173 (func toAuditEntry)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an accepted ADR already decide audit entries for job claimer events? No ADR in the pack covers job claimer audit entries, so this change decides coverage, subject, and source below.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
