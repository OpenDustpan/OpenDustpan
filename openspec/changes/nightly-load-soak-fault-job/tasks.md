## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Inventory main for harness entry points. Write failing tests for the NEW compare program: breach, within budget, record-only, stale entry, missing measurement, repeat breach, recovery Check: `go test ./perf/compare/ -count=1` fails before the program exists; any missing harness entry point is recorded as BLOCKED by the missing harness

## 2. Workflow and budgets

- [ ] 2.1 Add the NEW compare program, `perf/budgets.json` and the three NEW wrapper scripts Check: `go test ./perf/compare/ -count=1` passes
- [ ] 2.2 Add the NEW workflow with service containers, pinned actions, concurrency, timeout and `if always()` upload Check: `gh workflow view nightly-perf.yml` lists load, soak and toxiproxy-faults jobs
- [ ] 2.3 Do a trial run with a lowered budget to prove the fail, the exact-title issue and the comment on repeat Check: `gh workflow run nightly-perf.yml` ends failed, with one issue and the measurements artifact

## 3. Definition of done

- [ ] 3.1 Wire the schedule and manual dispatch, with no pull request trigger Check: `gh workflow run nightly-perf.yml` starts a run with the workflow_dispatch trigger; no run starts on a pull request
- [ ] 3.2 Add the operations docs page: schedule, `perf/budgets.json`, the rerun command, nightly figures are never a service level agreement Check: `make docs-lint` reports nothing
- [ ] 3.3 Usability check of the failed-run path Check: an Operator reruns from the docs page alone and each breach names `gh run view <run-id>`
- [ ] 3.4 Final check Check: `make check` with zero findings
