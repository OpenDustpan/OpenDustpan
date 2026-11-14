## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing read-during-update tests for outputs and state version id. Check: TestOutputReadDuringJournaledUpdateUsesLastCheckpoint fails on main and passes after the fix.
- [ ] 1.2 Add failing tests for refusals and for the completion race, restart, cancel, and failure cases. Check: Tests for no-checkpoint 404, missing-rule 403, missing-grant 403, deny-before-not-found, completion-race stability, restart mid-journal, rule removed in flight, and deleted producer fail on main and pass after the fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Keep output reads on the last completed checkpoint path. Check: Output read path loads only CompletedCheckpoint values through NewCompletedCheckpoint that refuses a running update row.
- [ ] 3.2 Update docs/guides/output-sharing.md with the latest-state rule. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units` runs as written from the runbook in docs/guides/output-sharing.md.
- [ ] 3.3 Run definition of done: e2e read during a real journaled update on route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs with a personal token, docs page, usability check from the docs page alone. Check: Real Pulumi CLI journaled update, then GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs with a personal token returns the pinned checkpoint with updateRunning true; `make check` passes with zero findings.
