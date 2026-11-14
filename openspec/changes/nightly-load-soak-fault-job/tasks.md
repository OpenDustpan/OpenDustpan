## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Inventory main for harness entry points. Write failing tests in the NEW file `perf/compare/compare_test.go` (NEW package `perf/compare`) for the NEW compare program: breach, within budget, record-only, stale entry, missing measurement, repeat breach, recovery Check: `go test ./perf/compare/ -count=1` fails before the program exists; any missing harness entry point is recorded as BLOCKED by the missing harness

## 2. Workflow and budgets

- [ ] 2.1 Add the NEW file `perf/compare/main.go` (the compare program, NEW path `perf/compare/main.go`), the NEW file `perf/budgets.json` and the three NEW wrapper scripts `perf/run-load.sh`, `perf/run-soak.sh` and `perf/run-toxiproxy.sh` Check: `go test ./perf/compare/ -count=1` passes
- [ ] 2.2 Add the NEW file `.github/workflows/nightly-perf.yml` with service containers, pinned actions, concurrency, timeout and `if always()` upload Check: `gh workflow view nightly-perf.yml` lists load, soak and toxiproxy-faults jobs. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 2.3 Do a trial run of the NEW file `.github/workflows/nightly-perf.yml` with a lowered budget in the NEW file `perf/budgets.json` to prove the fail, the exact-title issue and the comment on repeat Check: `gh workflow run nightly-perf.yml` ends failed, with one issue and the measurements artifact. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

## 3. Definition of done

- [ ] 3.1 Wire the schedule and manual dispatch, with no pull request trigger Check: `gh workflow run nightly-perf.yml` starts a run with the workflow_dispatch trigger; no run starts on a pull request
- [ ] 3.2 Add the section "The nightly run" to the existing files docs/guides/load.md and docs/guides/soak.md: the schedule, `perf/budgets.json`, the rerun command, and that nightly figures are never a service level agreement. Check: both files have the section "The nightly run" and `make docs-lint` reports nothing
- [ ] 3.3 Usability check of the failed-run path Check: an Operator reruns from the docs page alone and each breach names `gh run view <run-id>`. A person's step.
- [ ] 3.4 Final check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
