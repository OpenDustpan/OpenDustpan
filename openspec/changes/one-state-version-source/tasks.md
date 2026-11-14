## 1. Check the write points

- [x] 1.1 Confirm the write points and the version of each tool in both dialects (`design.md`, "The version ID of each tool" and "The one write path"). Write any difference into `design.md` before task 2. Check: the list in `design.md` names each function and line.

## 2. Schema and store

- [x] 2.1 Baselines (SQLite, PostgreSQL): add `unit_state`, keyed by unit ID; remove `unit_state_versions` and `unit_final_versions`. Regenerate sqlc. Check: `make generate`, then `sqlc diff` is clean.
- [x] 2.2 Add `publishUnitState` to both stores. Call it next to `SetWorkspaceCurrent`, at each lock release, for an allowed unlocked write, in `PutCheckpoint` and in `PutBuiltCheckpoint`. Derive `unit_state` in the portable restore. Check: the store tests of section 4, one row per caller.
- [x] 2.3 Carry `StateOrigin` from the handlers to each publish point. Check: a test of a job-token write records its job; a laptop write records an empty origin.
- [x] 2.4 Point the job store reads (`StateVersion`, `FinalVersion`) at `unit_state`. Delete `PutUnitStateVersion`, `ObserveStateVersion`, `PutFinalVersion` and the harness method `SetStateVersion`. Check: `go build ./...` and `rg` finds no caller.
- [x] 2.5 The replan of the reconciler names both state versions in its reason. Check: a test reads the reason.

## 3. Conformance suites

- [x] 3.1 Rewrite the `jobtest` cases on `Harness.WriteState`, implemented by each store test with the real workspace or stack store. Remove the version fake from the in-memory job store. Check: `go test ./pkg/job/... ./pkg/store/...` (store packages with `-timeout 60m`).

## 4. Verification

- [x] 4.1 Store and suite tests of `design.md` (tests 1 to 6). Check: each test fails when its write point or guard is removed.
- [ ] 4.2 Server test: a final write of unit A starts the `follows` plan of unit B through the real dispatcher. Check: the test in `pkg/dustpan`. Moved to #3005.
- [ ] 4.3 E2E: the path of the proposal with the real `tofu` CLI and the real `pulumi` CLI. Check: `make e2e` runs both steps on SQLite and PostgreSQL. Moved to #1315: no approve route yet (`pkg/job/service.go:330` returns `ErrUnsupported`).
- [x] 4.4 Docs: `docs/guides/approval-rules.md` says that an apply refuses a plan whose state changed, and that the job plans again. Check: `make docs-lint` exits 0.
- [ ] 4.5 Usability check: a person follows the guide, causes the stale plan, and reads the reason in the job. Check: the steps and the answers are in the pull request description.
- [x] 4.6 `make check` reports zero findings.
