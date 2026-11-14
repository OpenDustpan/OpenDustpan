---
version: 3
---

Issue: #295.

## Why

**Persona: operator.** An Operator tries to keep each release stable across instances. Today regressions in throughput, soak stability and network faults surface only at release time. A nightly job with budgets catches them early and files an issue with the numbers.

The user path this change must make work:

1. **Docs page.** Read the operations docs page for the nightly load, soak and fault job
2. **Console.** Inspect a failed nightly run and open the linked regression issue with numbers
3. **CLI.** Rerun the workflow after a fix with one command

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Load measurement exceeds its budget | The nightly job failed on a budget breach and opened or updated one issue with the numbers. Read the run for the measurement name, the measured value, the budget and the run id. | `gh run view <run-id>` |
| PostgreSQL or S3-compatible service container unhealthy | A service container failed to start, the harness crashed, or the run timed out with no measurement. The job log names the container or step. No regression issue is filed. Run the workflow again. | `gh workflow run nightly-perf.yml` |

Evidence for the claims above:

- The issue asks for a nightly workflow that runs the load harness, the soak run and the Toxiproxy tests on service containers, compares each measurement with budgets in the repository, fails the job and opens an issue on regression. Proof: Issue #295: "Compares each measurement with budgets kept in the repository (for example `perf/budgets.json`). A regression above the budget fails the job and opens an issue with the numbers."
- The issue says a person accepts the first values as the first budgets. Proof: Issue #295: "The first run records values. A person accepts them as the first budgets."

## What Changes

- Add the NEW file `.github/workflows/nightly-perf.yml` with load, soak and Toxiproxy fault jobs on service containers
- Add the NEW file `perf/budgets.json` with per-measurement budgets and a NEW compare program in `perf/compare` that fails the job and opens one issue above budget
- Record first measured values for human acceptance as the first budgets through a reviewed pull request

Out of scope:

- Load harness implementation changes
- Soak run duration or CLI matrix changes
- Release sizing guide tables
- Shared cache work
- Crash tests and fault wrappers, which stay in make check and the e2e suite

## Usability

Where the path still costs the person time or surprises them:

- Operator reads three job logs to find the regression; the compare summary lists each breach in one table with name, measured value, budget and run id.
- First budgets need manual acceptance; the trial-run issue shows measured values to copy into `perf/budgets.json`.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The pack lists no nightly workflow, budgets file or perf harness on main; the existing tests it lists are unrelated to this item, so everything in this change is NEW. (`pkg/backup/service_test.go:1189 (func TestRunFinishesTableFilesTheExporterLeavesOpen)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 40: Load, fault and crash testing, and the sizing guide** (not owner-tagged).

- Decisions that apply: 6 (current), 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The load harness, the soak run and the Toxiproxy tests run in a nightly job, not on each pull request."
- Quote: "Budgets. The nightly job compares each measurement with a budget kept in the repository. A regression above the budget fails the job and opens an issue."
- Effect: Follows decisions 6 and 7: the nightly job runs the three suites, compares each measurement with a repository budget, fails and opens an issue on regression.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where do load, soak and fault tests run and what happens on regression? ADR 40 decisions 6 and 7: a nightly job runs them, compares with repository budgets, fails and opens an issue.
- Does the nightly job also run fault wrappers, benchmarks or crash tests? ADR 40 decision 6: those run in make check and the e2e suite, not in the nightly job.
- Do nightly measurements feed the sizing guide? ADR 40 decision 8: sizing tables come from a load run on each release, not from nightly runs.
- Which harness entry points exist on main? The pack lists none. Every path is marked NEW and the first task inventories main and records BLOCKED when an entry point is missing.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
