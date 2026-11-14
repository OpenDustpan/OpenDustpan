---
version: 1
---

Issue: #216.

## Problem

The overnight loop kept every rule that a test or linter enforces and missed rules that only a reviewer checks. An agent needs those rules on one guide page so its pull requests pass review the first time.

## Evidence

- The identity conformance suite exists on main. Proof: pkg/identity/identitytest/identitytest.go:1:// Package identitytest is the conformance suite for identity.Authenticator
- A restore test checks that error text holds no driver detail. Proof: pkg/backup/restore_service_test.go:801 (TestRestoreErrorTextHoldsNoDriverDetail)
- A request log test checks that state upload logs hold a pattern and not a token. Proof: pkg/dustpan/requestlog_test.go:402 (TestStateUploadLogsPatternNotToken)

## Fix boundary

- Add the NEW file docs/guides/agent-review-loop.md with the fixed checklist for diffs that touch secrets, identity, routes, errors, migrations, limits, locks or crypto.
- State the audit cadence in the guide: a fresh-context audit every 20 merges or every 12 hours with last audited commit state.
- State the decision marking in the guide: the loop writes an RFC or proposed decision marked agent-drafted, and an ADR is written only after the owner accepts it.
- State the triage labels in the guide: ready-for-human for work that needs a person and local-agent for edits of .github/workflows/.
- State the branch hygiene in the guide: rebase on main before opening the pull request and use the pull request body as the squash message.

Out of scope:

- Product code for automated merge gates beyond the guide text
- Release and secret work that stays ready-for-human

## Check

- Add the fixed security checklist for diffs that touch secrets, identity, routes, errors, migrations, limits, locks or crypto Check: No route: the test reads the NEW file docs/guides/agent-review-loop.md only.
- Add the audit cadence, agent-drafted RFC marking, triage labels and branch hygiene Check: No route: the test reads the NEW file docs/guides/agent-review-loop.md only.
- Walk the guide from the page alone and record where the path costs time Check: No route: the walk reads docs/guides/agent-review-loop.md only.
- Run make check with zero findings. Confirmed by: CI (make check). Check: make check

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The conformance suite for identity.Authenticator exists. (`pkg/identity/identitytest/identitytest.go:14 (package identitytest)`)
- Restore error text is tested for no driver detail. (`pkg/backup/restore_service_test.go:801 (TestRestoreErrorTextHoldsNoDriverDetail)`)
- State upload logs are tested to hold a pattern and not a token. (`pkg/dustpan/requestlog_test.go:402 (TestStateUploadLogsPatternNotToken)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR already decide the review loop, triage labels or ADR frontmatter? The pack lists no ADR and no RFC for the review loop, so no decision applies and no conflict exists.
- Is the change security code? Yes. Diff text and labels are untrusted input, so the change carries a DFD, STRIDE items with five layers each, and failure modes for the review and audit steps.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
