---
version: 1
---

Issue: #2555.

## Problem

An agent keeps the triage state of 135 open issues numbered 2000 and up current so readers know what is done, partly done, open, or unsure.

## Evidence

- Issue #2555 checked 135 issues numbered 2000 and up against main at 3e36b85 with counts DONE and closed 7, PARTLY DONE 17, OPEN 108, UNSURE 6. Proof: #2555 body: Report of the backlog prune for open issues numbered 2000 and up. Checked against main at 3e36b85. 135 issues read. Counts: DONE and closed 7. PARTLY DONE 17. OPEN 108 (includes issues that wait for an owner decision). UNSURE 6 (kept open).
- Issue #2555 says follow-up comments list each decision with its proof. Proof: #2555 body: Follow-up comments list each decision with its proof.

## Fix boundary

- No server, CLI, API, or docs-page change
- Tracker state only: post the follow-up comments of report #2555 with proof per decision

Out of scope:

- Fixing any of the reviewed issues beyond the 7 DONE closes
- Product, API, store, docs-page, or e2e changes
- A new prune process or automation

## Check

- Reconcile the 135 read against the 138 counted and post the corrected list as a comment on issue #2555. Writes NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md with the corrected list. Check: `gh issue view 2555 --comments` shows the corrected list
- Post the follow-up state with proof for each decision and keep the 6 UNSURE issues open. Writes the proof lines to NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md. Check: `gh issue view 2555 --comments` shows proof per decision
- Verify each of the 7 DONE closes against its fix proof and reopen any mismatch. Writes the result to NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md. Check: `gh issue list --state closed --search 2555`
- Record the remaining work of the 17 PARTLY DONE issues in NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md and in the follow-up state. Check: `gh issue view 2555 --comments` records each remainder
- Confirm no product diff exists and the change stays tracker-only. Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main ships audit backlog batching covered by a large-backlog test (`pkg/audit/ship_test.go:508 (TestALargeBacklogShipsInBatches)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

1. How is the count gap of 135 read versus 138 counted resolved, and what happens if the gap hides an unread issue? Decides: PM.
   - Options: Reconcile the list first; re-triage any issue that the gap exposes, and correct the counts before any comment; Post comments first and reconcile later.
   - Recommended: Reconcile the list first; re-triage any issue that the gap exposes, and correct the counts before any comment Reason: The totals add to 138 while the report states 135 read, so the sets are not trusted until reconciled.

Not spec-ready: a code check fails: task "Confirm no product diff exists and the change stays tracker-" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: `openspec/changes/backlog-prune-b-report-7/triage-record.md` is named by the design or a task but is not in the touches of the stack plan.

Questions considered and answered:

- Does any ADR govern this backlog prune report? No ADR is touched. The pack names no ADR and the work posts triage comments only.
- How is a wrong DONE close caught? It is reopened in the tracker and removed from the DONE count. The follow-up state records the correction.
- Why do the reported counts not add to 135? 7 plus 17 plus 108 plus 6 equals 138. The gap is an open question until the list is reconciled.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
