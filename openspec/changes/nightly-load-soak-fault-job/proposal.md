---
version: 6
---

Issue: #295.

## Why

**Persona: operator.** An Operator tries to keep each release stable across instances. The load harness, the soak run and the Toxiproxy fault tests exist on main, but nothing runs them each night and nothing compares their numbers with a limit. A regression in throughput, soak stability or network behavior shows only when a person runs a target by hand. A nightly job with budgets in the repository catches it early and files one issue with the numbers.

The user path this change must make work:

1. **Docs page.** Read the section "The nightly run" in docs/guides/load.md and docs/guides/soak.md: the schedule, the budget file, the rerun command.
2. **Console.** Not used. The nightly job has no console surface; the issue and the run page of the repository host carry the numbers.
3. **CLI.** Rerun the workflow after a fix with `gh workflow run nightly-perf.yml`.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A measurement exceeds its budget | The nightly job failed on a budget breach and opened or updated one issue with the numbers. The run lists the measurement name, the suite, the measured value, the budget and the run id. | `gh run view <run-id>` |
| A service container is unhealthy, the harness crashed, the run timed out, or a report cannot be read | The job log names the container or the step. No regression issue is filed. Run the workflow again. | `gh workflow run nightly-perf.yml` |

Evidence for the claims above:

- Issue #295 asks for a nightly workflow that runs the load harness, the soak run and the Toxiproxy tests, compares each measurement with budgets kept in the repository, fails the job and opens an issue on regression. Proof: Issue #295: "Compares each measurement with budgets kept in the repository (for example `perf/budgets.json`). A regression above the budget fails the job and opens an issue with the numbers."
- The issue says a person accepts the first values as the first budgets. Proof: Issue #295: "The first run records values. A person accepts them as the first budgets."

## What Changes

Everything below is a new file. No server code, no harness code and no soak code changes.

- NEW `perf/compare/`: a Go package with three adapters (load report, soak CSV, fault test events) that write the common measurements file, the budget comparison and the issue sync. NEW `cmd/perfcompare/main.go` is its command line. A prior branch holds a first version of both (see the design, "Reuse of prior work").
- NEW `perf/run-load.sh`, `perf/run-soak.sh`, `perf/run-toxiproxy.sh`: thin wrappers around `go run -tags journalrecording ./load`, `make soak` and `make faults`, on PostgreSQL.
- NEW `.github/workflows/nightly-perf.yml`, delivered as a workflow patch in the pull request body (a cloud session cannot push workflow files).
- The two existing guides get the section "The nightly run", and lose the bullets of "What this guide does not cover" that this change answers.

Out of scope:

- Load harness, soak run and fault test changes
- Soak duration and CLI matrix changes
- The S3 latency benchmark (`make bench-s3`). Its report, the blob flow report (`-flows blobs`) and the scale report (`-flows scale`) are not part of the nightly job.
- Release sizing guide tables
- A SQLite run of the nightly job. The nightly job runs PostgreSQL only; SQLite is a later change.
- Crash tests and fault wrappers, which stay in `make check` and the e2e suite
- Any change to the faults job of the existing CI workflow

## Usability

Where the path still costs the person time or surprises them:

- An Operator reads three job logs to find the regression. The compare step writes one table to the job summary with name, suite, measured value, budget and run id.
- First budgets need a person. The job summary and the artifact `measurements.json` hold the measured values to copy into `perf/budgets.json` in a reviewed pull request. There is no automatic budget change.
- The built-in growth rule of the soak run (docs/guides/soak.md "Growth rule") stays as it is and decides the result of `make soak` by hand. In the nightly job the soak budgets decide. Until a person accepts soak budgets, a soak run that fails the built-in rule does not fail the nightly job. The job summary shows the built-in verdict as a line, so the person sees it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `make load` runs `go run ./load -out load-report.json`. (`Makefile:337 (make target load)`)
- `make soak` runs the soak test with `SOAK_DURATION` (default 30m), the worker counts and `SOAK_REPORT_DIR`. (`Makefile:403 (make target soak)`)
- The soak test writes `soak-<store>-<stamp>.csv` and `.md` into the report directory. (`e2e/soak_test.go:467 (func soakWriteReports)`)
- `make faults` runs the fault tests and reads `TOXIPROXY_URL`. (`Makefile:378 (make target faults)`)
- The fault tests assert and write no numbers. The PostgreSQL test joins when `DUSTPAN_TEST_POSTGRES_URL` is set. No test in the fault suite has an S3 consumer. (`e2e/faults/faults_test.go:473 (func TestPostgresCutThroughProxy)`)
- The S3 latency benchmark is `make bench-s3`. It is not one of the three suites. (`Makefile:340 (make target bench-s3)`)
- The load guide says "the nightly budget job" at line 11 and ends with a list "What this guide does not cover" that holds the bullet "Budget thresholds for the nightly job." (`docs/guides/load.md:339`)
- The soak guide says "The first nightly run records values, and a person accepts them as budgets." at line 82, and its list "What this guide does not cover" holds two nightly bullets. (`docs/guides/soak.md:102`)

More facts on main, in paths that the fact check does not read. The harness report type is `Report` in (read only) `load/harness.go:232`; times are milliseconds. The default run set of the harness is `all`, in (read only) `load/main.go:117`. Without the `journalrecording` tag the report has no journal runs (read only `load/harness.go:185`), and the load guide says the nightly run uses the tag. The package `soak` is importable: `Assess` (read only `soak/soak.go:192`) computes the slopes and the verdict from the samples, and `WriteCSV` (read only `soak/soak.go:290`) writes the samples; the `.md` holds the verdict only. The workflow `ci.yml` runs `make faults` on a digest-pinned Toxiproxy service container, on pull requests that touch the e2e paths and not on the schedule ((read only) `.github/workflows/ci.yml:815`). The nightly workflow pattern is `schedule` with a minute other than 0, `workflow_dispatch` and `permissions: contents: read` ((read only) `.github/workflows/e2e-ha-nightly.yml:13`). The issue pattern is a text search `in:title` and `gh issue create` ((read only) `.github/workflows/cli-release-watch.yml:57`); the search is not an exact title match, so the new job does not copy it as it is.

New in this change (does not exist on main, checked with `ls perf .github/workflows/nightly-perf.yml`): the `perf/` directory, `perf/budgets.json`, `perf/compare`, `cmd/perfcompare/main.go`, `perf/run-*.sh` and `.github/workflows/nightly-perf.yml`. Every measurement name and every Go name in this change is NEW.

## Judgement on the form of this change

The change adds no product behavior: it is CI and tooling around three suites that main already has. By the project rule, tooling and CI need no OpenSpec change, and a pull request with the evidence in its body is enough. The change stays because issue #295 asks for budget semantics (zero tolerance, record-only, one issue for each measurement) that a reviewer must agree on first. Keep it small. It may also be driven as a plain tooling brief from the tasks below.

## ADR check

Each quote is copied from `docs/adr/0040-load-fault-and-crash-testing.md`. The "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md` line 53.

**ADR 40: Load, fault and crash testing, and the sizing guide.** The ADR records an approach that the project owner accepted on 2026-10-11 ("The project owner accepted this approach on 2026-10-11, and asked that the results feed the documentation on architecture and sizing."). Its text carries no "(owner)" tag, so the decisions are not owner-tagged in the sense of `openspec/project.md`. Treat them as owner-accepted and do not weaken them.

- Decisions that apply: 4, 5, 6, 7 (all current).
- Changed by: none. Chain to the end: none. Current rule: this ADR.
- Quote 4: "Soak run with real CLIs. A run of many concurrent `pulumi up` and `tofu apply` processes for 30 minutes checks for leaks and errors over time."
- Quote 5: "Network faults with Toxiproxy. A Toxiproxy container in CI sits between the server and PostgreSQL, the S3-compatible server and the CLIs."
- Quote 6: "The load harness, the soak run and the Toxiproxy tests run in a nightly job, not on each pull request."
- Quote 7: "Budgets. The nightly job compares each measurement with a budget kept in the repository. A regression above the budget fails the job and opens an issue."
- Consequence: "The budgets need a first run to set them. The first nightly run records values, and a person accepts them."
- Effect: the change implements decisions 6 and 7 with the existing suites. Decision 5 names an S3-compatible server in the path of the Toxiproxy tests. No test in `e2e/faults` has an S3 consumer on main (see "What main already does"), so the nightly job starts no S3 container. A fault test with an S3 path is a later change with its own test.
- Conflict: none. The `faults` job of `ci.yml` runs on pull requests, which decision 6 does not describe. This change leaves it as it is; the nightly workflow itself never runs on a pull request.

Other ADRs found with `rg -n -i "nightly|soak|toxiproxy" docs/adr docs/rfc`: ADR 43 (the nightly run with the newest Pulumi CLI), ADR 298 and ADR 141 name other nightly jobs or freeze schedules. They do not decide anything about this job. No RFC decides it.

## Open questions

none

Questions considered and answered:

- Where do load, soak and fault tests run and what happens on regression? ADR 40 decisions 6 and 7: a nightly job runs them, compares with repository budgets, fails and opens an issue.
- Which entry points does the job use? The three on main: `go run -tags journalrecording ./load`, `make soak` and `make faults`. No new harness.
- Does the load report give p95? No. Most fields are one median (`p50Ms`) or one timing. The measurements file has optional `p50`, `p95` and `max`, and each adapter fills only what its source has (design, decision 2).
- Is an S3 service container needed? No. Load runs the default `fs` blob store, soak and faults have no S3 consumer. See the ADR check.
- How does the soak growth rule map to a budget? The adapter recomputes the slopes from the CSV with `soak.Assess` and emits each slope as a measurement. The budget bounds the slope. The built-in verdict stays. See design, decision 3.
- Do nightly measurements feed the sizing guide? No. ADR 40 decision 8: sizing tables come from a load run on each release.
- Where does the workflow come from? A cloud session cannot push `.github/workflows`. The pull request body holds the patch under "Workflow patch (apply locally)", and a person applies it.

Drafted by the spec factory of the spec compiler, then rewritten against main. Every path and line was checked on main.
