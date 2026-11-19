## Context

CI tooling around three suites that main already has. No server runtime code changes, no harness change and no new dependency. The workflow runs the suites, an adapter turns each report into one common file, and a Go program compares that file with `perf/budgets.json` and plans the issue actions. The workflow runs `gh` for the actions.

## Decisions

1. **Entry points and workflow shape.** The wrappers run what main has: `go run -tags journalrecording ./load` (the tag is in the load guide for the nightly run, so the report holds the journal timings), `make soak` and `make faults`. They run on PostgreSQL only (`DUSTPAN_TEST_POSTGRES_URL`), so the load harness measures two instances on one database. The nightly job does not run SQLite; a SQLite row is a later change. The scope of a measurement is the suite: `load`, `soak` or `faults`. Service containers are PostgreSQL and Toxiproxy, with the same images as (read only) `.github/workflows/ci.yml`. No S3 container: no suite has an S3 consumer, so the wrappers need no hook for extra load flags. The workflow has `schedule` (a minute other than 0) and `workflow_dispatch` only, concurrency group `nightly-perf` with cancel-in-progress false, an `if` guard on the main branch like (read only) `.github/workflows/e2e-nightly.yml`, `timeout-minutes` 120 for the workflow jobs, `permissions: contents: read` on the suite jobs and `issues: write` on the compare job only. Actions pin to a full commit SHA. Each suite job uploads its raw report and its `measurements.json` with `if: always()`. Wrapper rules: the soak wrapper treats a `make soak` that exits non-zero with its CSV present as a measurement run; the fault wrapper checks `$TOXIPROXY_URL/version` first and treats a failed test as a measurement run (the compare decides), not as a failed step.
2. **One measurements file, with optional statistics.** `measurements.json` is a list of `{name, scope, unit, p50, p95, max}`. `p50`, `p95` and `max` are optional numbers; an adapter sets only what its source has. The load report holds one median or one timing for most fields, and never a p95, so the rule is: a field named `...P50Ms` sets `p50`; a field that is one timing or a stated maximum sets `max`; no adapter sets `p95` on main (the field stays for a later source). The name is a fixed string made by the adapter from a fixed prefix and, where the report has a list, the size or count of the row (for example load.checkpoint.full.1048576). The name never contains free text from a report. The budget key is `<name>@<scope>`.
3. **Adapters and the field map.** Prior work reuses the names of the first column below where it has one (marked "prior"). Names use lowercase words, digits and hyphens, with the size of a row in KiB or the entries of a batch as the last part. A mapped field that the report lacks, a negative or non-finite number, or a missing mapped list is an adapter error (`ErrMissingField` or `ErrInvalidFigure`): the job fails with no regression issue. The adapter ignores every field that is not in the tables.

   Load report (`load-report.json`, type `Report`). Scope `load`. Units `ms` and `bytes`.

   | Report field | Name | Statistic | Unit |
   | --- | --- | --- | --- |
   | `checkpoints[].fullMs` | load.checkpoint.full.<N>k (prior) | max | ms |
   | `checkpoints[].deltaMs` | load.checkpoint.delta.<N>k (prior) | max | ms |
   | `checkpoints[].fullPeakAllocBytes` | load.checkpoint.full-alloc.<N>k | max | bytes |
   | `checkpoints[].deltaPeakAllocBytes` | load.checkpoint.delta-alloc.<N>k | max | bytes |
   | `imports[].importMs` | load.import.<N>k (prior) | max | ms |
   | `imports[].peakAllocBytes` | load.import-alloc.<N>k | max | bytes |
   | `workspaceLocks.p50Ms` | load.workspace-lock.attempt (prior) | p50 | ms |
   | `updateConflicts.p50Ms` | load.update-conflict.start (prior) | p50 | ms |
   | `namespaceUpdates.p50Ms` | load.namespace-update.start (prior) | p50 | ms |
   | `database.firstP50Ms` | load.database.first | p50 | ms |
   | `database.secondP50Ms` (only when `secondPresent` is true) | load.database.second | p50 | ms |
   | `lists.workspaceListP50Ms` | load.list.workspace-page (prior) | p50 | ms |
   | `lists.workspaceListFullMs` | load.list.workspace-walk (prior) | max | ms |
   | `lists.inventoryMs` | load.list.inventory (prior) | max | ms |
   | `journal.runs[].appendP50Ms`, `appendMaxMs` (only when `journal.supported` is true) | load.journal.append.<entries> (prior) | p50 and max | ms |
   | `journal.runs[].completeMs` | load.journal.complete.<entries> (prior) | max | ms |
   | `journal.runs[].peakAllocBytes` | load.journal.alloc.<entries> | max | bytes |
   | `access.runnerRoute.gateOffP50Ms`, `gateNoListP50Ms`, `gateListP50Ms`, `gateRefusedP50Ms` | load.access.runner.gate-off, .gate-no-list, .gate-list, .gate-refused | p50 | ms |
   | `access.jobsRoute.<same four>` | load.access.jobs.<same four> | p50 | ms |

   Soak (`soak-postgres-<stamp>.csv`). Scope `soak`. The adapter reads the CSV into `Sample` values of the package `soak` and calls its `Assess`, so the slope code exists once. A run with the note "too few samples" is an adapter error (`ErrTooFewSamples`).

   | Source | Name | Statistic | Unit |
   | --- | --- | --- | --- |
   | goroutines slope | soak.goroutines.slope | max | per-minute |
   | heap bytes slope | soak.heap.slope | max | bytes-per-minute |
   | file descriptors slope (no measurement when the series has no data) | soak.fds.slope | max | per-minute |
   | database connections | none: the series never has data | | |
   | total failed CLI runs over total CLI runs | soak.error-rate | max | ratio |

   The built-in rule (`slope <= threshold`, `error rate <= 0.01`) has the same direction as a budget: higher is worse. A person may accept budgets equal to the built-in thresholds or to the measured values. The adapter copies the built-in verdict line into the job summary and never uses it for the decision.

   Faults (`go test -json -tags faults ./e2e/faults/...`). Scope `faults`. Each top-level test gives two measurements: `faults.test.<Test>` (prior: elapsed seconds of a passing test, max, unit `s`) and `faults.failed.<Test>` (max 1 when the test failed and 0 otherwise, unit `tests`). A budget `max` of 0 on the second makes a failed test a breach. A stream with no test event, a build failure, or a Toxiproxy server that does not answer is an infrastructure failure.
4. **Comparison rule.** A breach is a measured `p50`, `p95` or `max` above the budget field of the same name in the same entry, with zero tolerance. A measured field with no budget field, and a measurement with no entry, are record-only. A budget entry whose unit differs from the measurement is an error (`ErrUnitMismatch`), never a pass. A budget entry or field with no matching measurement gives a warning and never fails the job. Each measurement is evaluated alone. A budget change needs a reviewed pull request. Prior work compares every field and reads an absent budget field as 0, so a budget with only `max` breaches on any p50; the optional fields of this design replace that.
5. **Issues, in the compare command.** The `check` command with `-issues` opens, comments on and closes issues through the `gh` command, behind the small `Issues` interface so that tests use a fake. The title is exactly `[nightly-perf] <name>`. `Find` lists open issues with a text search as a filter and then keeps only the issue whose title equals the string exactly. A breach with no open issue opens one; a breach with one adds a comment; a measurement within budget with an open issue closes it with a recovery comment. An open issue whose measurement the run did not produce stays open and is not touched. The command runs `gh` with an argument list and sends bodies on standard input, never through a shell. The first failed `gh` call ends the command with an error and the artifact stays.
6. **Infrastructure failures and exit codes.** The wrapper or the adapter ends with a message that names the step when a service container is unhealthy, the harness or `go test` build fails, the report file is missing or unreadable, the run times out or is canceled, or the upload fails. The `check` command exits 0 with no breach, 1 with at least one breach (issues are synced first), and 2 for an input that is not valid or has no measurement. The workflow reads the status: 1 fails the job with the regression issue filed; 2 fails the job with no issue. Prior work exits 1 for both and cannot tell them apart.
7. **Docs in the existing guides.** The section "The nightly run" goes into docs/guides/load.md and docs/guides/soak.md: the schedule, the budget file, the measurement names, the rerun command, and that nightly figures are never a service level agreement. The bullets that this change answers leave "What this guide does not cover": "Budget thresholds for the nightly job" in both guides and "The nightly CI job, which is a separate issue." in soak.md. "Fault injection and crash testing" stays in both lists, because the nightly job runs the existing fault tests and adds no new fault injection to the harness or the soak run. The sentence in load.md:11 and the sentence in soak.md:82 stay true and link to the new section. No new docs page.

## Contract

Package `opendustpan.org/opendustpan/perf/compare` (prior work, reuse) and the command `cmd/perfcompare`:

```go
type Stats struct {
	P50, P95, Max *float64
	Unit, Scope   string
}
type Budgets map[string]Stats // key: the measurement name
type Measurement struct {
	Name, Unit, Scope string
	P50, P95, Max     *float64
}

func ParseBudgets(r io.Reader) (Budgets, error)
func ParseMeasurements(r io.Reader) ([]Measurement, error)
func ExtractLoad(r io.Reader) ([]Measurement, error)
func ExtractSoak(r io.Reader) ([]Measurement, error)
func ExtractFaults(r io.Reader) ([]Measurement, error)
func Evaluate(budgets Budgets, ms []Measurement) (Result, error)
func Table(res Result, runID string) string
func Sync(ctx context.Context, issues Issues, res Result, runID string) error
```

Prior work has `P50`, `P95` and `Max` as plain `float64`; this design changes them to optional values. Sentinel errors: prior `ErrNoMeasurement`, `ErrInvalidName`, `ErrInvalidFigure`, `ErrDuplicate`, `ErrTrailingData`, `ErrBadCSV`; new `ErrMissingField`, `ErrUnitMismatch`, `ErrTooFewSamples`. Each function wraps them with `fmt.Errorf("...: %w", err)`.

Command: `perfcompare extract-load|extract-soak|extract-faults` (standard input to standard output), and `perfcompare check -budgets perf/budgets.json -run-id <id> [-issues] <measurements.json>...`. The summary is a Markdown table with columns measurement, figure, measured, budget, unit, scope and run.

## Reuse of prior work

Prior work: `origin/nightly-load-soak-fault-job` at 82c3e0f. It has the compare package, `cmd/perfcompare`, three wrappers, `perf/budgets.json`, a test file and the two docs sections. The tasks reuse it. Where it differs from this design, this table says which side wins and why.

| Topic | Prior work | Decision | Why |
| --- | --- | --- | --- |
| Layout | library `perf/compare`, command `cmd/perfcompare` | prior wins | It follows the `cmd/` convention that `cmd/s3bench` uses. The older plan that names `perf/compare/main.go` is stale. |
| Stores | PostgreSQL only, scope is the suite | prior wins | Smaller. A budget key is the name alone. |
| Issue title and dedup | `[nightly-perf] <name>`, exact match after a search filter, `gh` behind an `Issues` interface | prior wins | One process, testable with a fake, no plan file. |
| Name allowlist | `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$` | prior wins | Test names have capitals. |
| Statistics | all three fields always set; a single value copied into p50, p95 and max; absent budget field is 0 | this design wins | A copied value invents p95, and an absent budget field breaches. Fields are optional. |
| Load field map | 11 rows, no allocation, database or access rows, no check for missing fields | this design wins, names of prior rows kept | More of the report, and a missing mapped field fails closed. |
| Journal timings | wrapper runs `go run ./load` without the tag | this design wins | Without `journalrecording` the report has no journal runs. |
| Soak figures | p50, p95 and max of the raw gauges over the samples | this design wins | A raw gauge level is not the growth rule of the soak guide, and it varies with the host. The slope is the documented leak measure. |
| Soak exit | `make soak` failing stops the wrapper | this design wins | The budgets decide in the nightly job; the CSV exists, so it is a measurement run. |
| Faults | elapsed seconds of passing tests; a failing test fails the step | both | Elapsed time stays (prior). A failed flag is added so that a failed test is a breach with an issue. |
| Exit codes | 1 for a breach and for an invalid input | this design wins | The workflow must tell a regression from an infrastructure failure. |
| Extra load flags hook | `PERF_LOAD_ARGS` | dropped | No consumer: no S3 container, and flags that need `-tags` cannot pass it. |
| Docs sections | both guides, the three "does not cover" bullets removed | prior wins | They need the names, the `{}` file shape and the `scope` text brought in line with this table. |

## Structure

No server structure changes. The only stored reference is `perf/budgets.json`, keyed by the stable measurement name and scope, never by branch, runner or run names. The compare package does no network call and reads no environment variable; only `cmd/perfcompare` runs `gh` (with `GH_TOKEN` from the workflow) and exits.

## Hostile input (defense in depth)

The workflow has `issues: write` and builds issue text from files. Treat every report, every budget file and the issue list as untrusted. One table for each layer, with a test per row that fails when the guard is removed:

| Layer | Hostile input | Guard |
| --- | --- | --- |
| Adapter | A report name or key with control characters, a newline, markdown, `@` or a shell word | The name is built from a fixed prefix and an integer. Free text of a report never enters a name or a title. |
| Adapter | A negative, NaN, infinite or missing number | `ErrInvalidFigure`, `ErrMissingField`; exit status 2, not a pass |
| Budgets | An entry with a name outside the allowlist, a negative or non-finite limit, an unknown field, trailing data | `ParseBudgets` rejects the file (`ErrInvalidName`, `ErrInvalidFigure`, `ErrTrailingData`); exit status 2 and no issue |
| Budgets | A unit that differs from the measurement | `ErrUnitMismatch`; exit status 2, never a pass |
| Issues | An open-issue title that equals the measurement title plus text, or differs in case or spaces | `Find` keeps an issue only on exact string equality; a near match opens a new issue |
| Issues | Issue body text from a measured string | the body holds only the name, scope, numbers, unit and run id that the program formats; the run id keeps only letters, digits, `-`, `_` and `.` |
| Issues | Title or body in a shell command | `gh` runs with an argument list and the body on standard input, never through a shell |
| Workflow | `${{ }}` text in a `run:` step | the workflow passes values as environment variables, with no interpolation into the script |
| Workflow | A fork pull request or a pull request trigger | no `pull_request` trigger; `issues: write` only on the compare job |

## Risks

- Budget drift when first values are accepted without review. A person reviews the first budgets pull request.
- Noisy runners cause false breaches. The recovery comment and a reviewed budget change handle it. Zero tolerance is the rule of the issue; a noisy measurement gets a larger budget in review, never a tolerance field.
- Load measurements of one run have no spread (one median or one timing). A single slow run may breach. The repeat-breach comment shows whether it lasts.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| job duration | complete within 120 minutes | minutes | each suite job of the nightly-perf workflow per run |
| soak duration | run real CLI processes for 30 minutes (the default of `make soak`) | minutes | each soak job per run |
| compare duration | finish with the measurements of one run | seconds | `perfcompare` per run, no network call |
