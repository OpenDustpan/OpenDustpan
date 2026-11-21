## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing test for split writes on stdout and stderr. Check: NEW test `TestLocalMaskSplitWrites` in pkg/launcher fails on main, passes after
- [ ] 1.2 Write failing test in NEW file pkg/launcher/local_mask_test.go for a refused start when the open or set build fails. Check: NEW test `TestLocalMaskRefusesWithoutSet` fails on main, passes after
- [ ] 1.3 Write failing test in NEW file pkg/launcher/local_mask_test.go for a run with an empty secret list. Check: NEW test `TestLocalMaskEmptySetProceeds` fails on main, passes after
- [ ] 1.4 Write failing test in NEW file pkg/launcher/local_mask_test.go for flush on child exit and on a signal. Check: NEW test `TestLocalMaskFlushOnExit` fails on main, passes after
- [ ] 1.5 Write failing test in NEW file pkg/launcher/local_mask_test.go that credential material never reaches display or upload bytes. Check: NEW test `TestLocalMaskNeverLogsCredential` fails on main, passes after
- [ ] 1.6 Write failing test in NEW file pkg/launcher/local_mask_test.go for a secret that the masker cannot hold. Check: NEW test `TestLocalMaskRefusesUnholdableValue` fails on main, passes after

## 2. Definition of done

- [ ] 2.1 Wire the masker into the local wrapper path. Check: `go test ./pkg/launcher/...` passes
- [ ] 2.2 Add an e2e step with real tofu and the fake provider in the existing file e2e/e2e_masked_test.go. The step runs the local wrapper with the launcher token bound to the apply claim. BLOCKED by the local log upload route and the local credential kind of depends_on and of the owner question. Check: BLOCKED by local-run-two-cases; the step checks masked display and masked upload bytes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.3 No route: the test checks the page text and the commands it names only. Write new docs page docs/guides/local-wrapper.md for local masking and its limits. Check: Each command on the page runs as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/runner_only_feature_in_local_docs_test.go checks the page text and the commands it names, as e2e/install_docs_test.go does; the page names no route). `make docs-lint` passes on each edited guide.
- [ ] 2.5 Run the full check suite. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
