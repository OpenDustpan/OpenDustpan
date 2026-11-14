## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing test for split writes on stdout and stderr. Check: NEW test `TestLocalMaskSplitWrites` in pkg/launcher fails on main, passes after
- [ ] 1.2 Write failing test for a refused start when the open or set build fails. Check: NEW test `TestLocalMaskRefusesWithoutSet` fails on main, passes after
- [ ] 1.3 Write failing test for a run with an empty secret list. Check: NEW test `TestLocalMaskEmptySetProceeds` fails on main, passes after
- [ ] 1.4 Write failing test for flush on child exit and on a signal. Check: NEW test `TestLocalMaskFlushOnExit` fails on main, passes after
- [ ] 1.5 Write failing test that credential material never reaches display or upload bytes. Check: NEW test `TestLocalMaskNeverLogsCredential` fails on main, passes after
- [ ] 1.6 Write failing test for a secret that the masker cannot hold. Check: NEW test `TestLocalMaskRefusesUnholdableValue` fails on main, passes after

## 2. Definition of done

- [ ] 2.1 Wire the masker into the local wrapper path. Check: `go test ./pkg/launcher/...` passes
- [ ] 2.2 Add an e2e step with real tofu and the fake provider. BLOCKED by the local log upload route and the local credential kind of depends_on and of the owner question. Check: BLOCKED by local-run-two-cases; the step checks masked display and masked upload bytes
- [ ] 2.3 Write new docs page docs/guides/local-wrapper.md for local masking and its limits. Check: Each command on the page runs as written in a test
- [ ] 2.4 Run the usability check from the docs page. Check: A developer completes the path from the docs page alone and each error names one command
- [ ] 2.5 Run the full check suite. Check: `make check` reports zero findings
