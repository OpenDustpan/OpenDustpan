## 1. Failing tests first

- [ ] 1.1 Add failing read-during-update tests for outputs and state version id. Check: TestOutputReadDuringJournaledUpdateUsesLastCheckpoint fails on main and passes after the fix.
- [ ] 1.2 Add failing tests for refusals and for the completion race, restart, cancel, and failure cases. Check: Tests for no-checkpoint 404, missing-rule 403, completion-race stability, restart mid-journal, rule removed in flight, and deleted producer fail on main and pass after the fix.

## 2. Definition of done

- [ ] 2.1 Keep output reads on the last completed checkpoint path. Check: Output read path loads only the last completed checkpoint through the constructor that refuses a running update id.
- [ ] 2.2 Update docs/guides/output-sharing.md with the latest-state rule. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units` runs as written from the runbook in docs/guides/output-sharing.md.
- [ ] 2.3 Run definition of done: e2e read during update, docs page, usability check from the docs page alone. Check: `make check` passes with zero findings.
