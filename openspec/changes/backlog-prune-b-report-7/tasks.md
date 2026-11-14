## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Triage record

- [ ] 1.1 Reconcile the 135 read against the 138 counted and post the corrected list as a comment on issue #2555. Writes NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md with the corrected list. Check: `gh issue view 2555 --comments` shows the corrected list
- [ ] 1.2 Post the follow-up state with proof for each decision and keep the 6 UNSURE issues open. Writes the proof lines to NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md. Check: `gh issue view 2555 --comments` shows proof per decision
- [ ] 1.3 Verify each of the 7 DONE closes against its fix proof and reopen any mismatch. Writes the result to NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md. Check: `gh issue list --state closed --search 2555`
- [ ] 1.4 Record the remaining work of the 17 PARTLY DONE issues in NEW file openspec/changes/backlog-prune-b-report-7/triage-record.md and in the follow-up state. Check: `gh issue view 2555 --comments` records each remainder

## 2. Definition of done

- [ ] 2.1 Confirm no product diff exists and the change stays tracker-only. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
