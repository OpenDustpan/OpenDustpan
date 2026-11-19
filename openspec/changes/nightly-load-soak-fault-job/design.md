## Context

CI tooling around three suites that main already has. No server runtime code changes, no harness change and no new dependency. The workflow runs the suites, an adapter turns each report into one common file, and a Go program compares that file with `perf/budgets.json` and plans the issue actions. The workflow runs `gh` for the actions.

## Decisions

1. **Entry points and workflow shape.** The wrappers run what main has: `go run -tags journalrecording ./load -out <dir>/load-report.json` (the tag is in the load guide for the nightly run), `make soak` with `SOAK_REPORT_DIR=<dir>` and `make faults` with `TOXIPROXY_URL`. Load and soak run once for SQLite and once for PostgreSQL (`DUSTPAN_TEST_POSTGRES_URL` set), so the scope of a measurement is the store. Faults run once with PostgreSQL set, so the PostgreSQL test joins. Service containers are PostgreSQL and Toxiproxy, with the same images as (read only) `.github/workflows/ci.yml` (digest-pinned). No S3 container: no suite has an S3 consumer. The workflow has on.schedule (a minute other than 0) and on.workflow_dispatch only, concurrency group `nightly-perf` with cancel-in-progress false, an `if` guard on the main branch like (read only) `.github/workflows/e2e-nightly.yml`, `timeout-minutes` 120 for the workflow jobs, and `permissions: contents: read` on the suite jobs and `issues: write` on the compare job only. Actions pin to a full commit SHA. Each suite job uploads its raw report and its `measurements-<suite>-<scope>.json` with `if: always()`.
2. **One measurements file, with optional statistics.** `measurements.json` is a list of `{name, scope, unit, p50, p95, max}`. `p50`, `p95` and `max` are optional numbers; an adapter sets only what its source has. The load report holds one median or one timing for most fields, and never a p95, so the rule is: a field named `...P50Ms` sets `p50`; a field that is one timing or a stated maximum sets `max`; no adapter sets `p95` on main (the field stays for a later source). The name is a fixed string made by the adapter from a fixed prefix and, where the report has a list, the size or count of the row (for example load.checkpoint.full.1048576). The name never contains free text from a report. The budget key is `<name>@<scope>`.
3. **Adapters and the field map.**

   Load report (`load-report.json`, type `Report`). Units: milliseconds `ms`, bytes `bytes`. Scope: report field `store` (`sqlite` or `postgres`).

   | Report field | Name | Statistic | Unit |
   | --- | --- | --- | --- |
   | `checkpoints[].fullMs` | load.checkpoint.full.<sizeBytes> | max | ms |
   | `checkpoints[].deltaMs` | load.checkpoint.delta.<sizeBytes> | max | ms |
   | `checkpoints[].fullPeakAllocBytes` | load.checkpoint.full_alloc.<sizeBytes> | max | bytes |
   | `checkpoints[].deltaPeakAllocBytes` | load.checkpoint.delta_alloc.<sizeBytes> | max | bytes |
   | `imports[].importMs` | load.import.<sizeBytes> | max | ms |
   | `imports[].peakAllocBytes` | load.import_alloc.<sizeBytes> | max | bytes |
   | `workspaceLocks.p50Ms` | load.workspace_lock | p50 | ms |
   | `updateConflicts.p50Ms` | load.update_conflict | p50 | ms |
   | `namespaceUpdates.p50Ms` | load.namespace_update | p50 | ms |
   | `database.firstP50Ms` | load.database.first | p50 | ms |
   | `database.secondP50Ms` (only when `secondPresent` is true) | load.database.second | p50 | ms |
   | `lists.workspaceListP50Ms` | load.list.workspace | p50 | ms |
   | `lists.workspaceListFullMs` | load.list.workspace_full | max | ms |
   | `lists.inventoryMs` | load.list.inventory | max | ms |
   | `journal.runs[].appendP50Ms` and `appendMaxMs` (only when journal.supported is true) | load.journal.append.<entriesPerBatch> | p50 and max | ms |
   | `journal.runs[].completeMs` | load.journal.complete.<entriesPerBatch> | max | ms |
   | `journal.runs[].peakAllocBytes` | load.journal.alloc.<entriesPerBatch> | max | bytes |
   | `access.runnerRoute.gateOffP50Ms`, `gateNoListP50Ms`, `gateListP50Ms`, `gateRefusedP50Ms` | load.access.runner.gate_off, `.gate_no_list`, `.gate_list`, `.gate_refused` | p50 | ms |
   | `access.jobsRoute.<same four>` | `load.access.jobs.<same four>` | p50 | ms |

   The adapter ignores the counts, the byte sizes of inputs, `durationMs`, `seedMs` and every field not in the table. A field in the table that the report lacks, a negative or non-finite number, or a store other than `sqlite` or `postgres` is an adapter error (the job fails with no regression issue).

   Soak (`soak-<store>-<stamp>.csv`). The adapter reads the CSV into the adapter reads the CSV into `Sample` values of the package `soak` and calls its `Assess`, so the slope code exists once. Scope: the store in the file name. A run with the note "too few samples" is an adapter error.

   | Source | Name | Statistic | Unit |
   | --- | --- | --- | --- |
   | Assessment.Metrics goroutines, slope | soak.goroutines.slope | max | per_minute |
   | heap bytes, slope | soak.heap_bytes.slope | max | bytes_per_minute |
   | file descriptors, slope (skipped when the series has no data) | soak.fds.slope | max | per_minute |
   | database connections | none: the series never has data (the DBConns field of the soak sample is always missing) | | |
   | Assessment.ErrorRate | soak.error_rate | max | ratio |

   The built-in rule (`slope <= threshold`, `error rate <= 0.01`) has the same direction as a budget: higher is worse. A person may accept budgets equal to the built-in thresholds or to the measured values. The adapter never reads the built-in verdict for the decision; it copies the verdict line into the job summary.

   Faults (`go test -json -tags faults ./e2e/faults/...`). Scope: `postgres`. Each top-level test of the package gives one measurement faults.<TestName>.failed with unit `tests` and `max` 1 when the test failed and 0 when it passed or was skipped on main by its own rule. A budget `max` of 0 makes a failed test a breach. If the event stream holds no test event, or a build failure, or the wrapper cannot reach `$TOXIPROXY_URL/version`, that is an infrastructure failure, not a measurement.
4. **Comparison rule.** A breach is a measured `p50`, `p95` or `max` above the budget field of the same name in the same entry, with zero tolerance. The entry also names the unit, and a unit that differs from the measurement is an error, never a pass. A measured field with no budget field, and a measurement with no entry, are record-only. A budget entry (or field) with no matching measurement gives a warning and never fails the job. Each measurement is evaluated alone. A budget change needs a reviewed pull request. `perf/budgets.json` starts as `{"budgets": {}}`.
5. **Issue plan and recovery.** The compare command reads `measurements*.json`, `perf/budgets.json` and `open-issues.json` (the output of `gh issue list --state open --label ... --json number,title --limit 200`, filtered to titles that start with `[nightly-perf] `). It writes `plan.json`, a list of `{action, title, number, body}` with action `open`, `comment` or `close`. The title is exactly `[nightly-perf] <name>@<scope>`, matched with string equality in the compare program, never with the host's text search. A breach with no open issue gives `open`; a breach with one gives `comment`; a measurement within budget with an open issue gives `close` with a recovery comment. An open issue whose measurement the run did not produce stays open and is not touched. The workflow step reads `plan.json` and runs `gh issue create`, `gh issue comment` or `gh issue close`, passing the title and body from files or environment variables, never by shell interpolation. If one `gh issue` call fails, the step fails the job and keeps the artifact.
6. **Infrastructure failures.** The wrapper or the adapter ends with a non-zero status and a message that names the step when a service container is unhealthy, the harness or `go test` build fails, the report file is missing or unreadable, the run times out or is canceled, or the upload fails. Such a run writes no `plan.json` and files no regression issue. A `make soak` run that exits non-zero with its report files present is a measurement run: the compare decides.
7. **Docs in the existing guides.** The section "The nightly run" goes into docs/guides/load.md and docs/guides/soak.md: the schedule, the budget file, the measurement names, the rerun command, and that nightly figures are never a service level agreement. The bullets that this change answers leave "What this guide does not cover": "Budget thresholds for the nightly job" in both guides and "The nightly CI job, which is a separate issue." in soak.md. "Fault injection and crash testing" stays in both lists, because the nightly job runs the existing fault tests and adds no new fault injection to the harness or the soak run. The sentence in load.md:11 and the sentence in soak.md:82 stay true and link to the new section. No new docs page.

## Contract

NEW package `opendustpan.org/opendustpan/perf/compare/perfcheck` (used by the NEW file `perf/compare/main.go`):

```go
type Measurement struct {
	Name  string
	Scope string
	Unit  string
	P50   *float64
	P95   *float64
	Max   *float64
}

func AdaptLoad(report []byte) ([]Measurement, error)
func AdaptSoak(csv io.Reader, store string) ([]Measurement, soak.Assessment, error)
func AdaptFaults(events io.Reader) ([]Measurement, error)

type Budget struct {
	Unit string
	P50  *float64
	P95  *float64
	Max  *float64
}

type Budgets map[string]Budget // key: "<name>@<scope>"

func ParseBudgets(data []byte) (Budgets, error)
func Compare(measured []Measurement, budgets Budgets) Result

type Action struct {
	Kind   string // "open", "comment" or "close"
	Title  string
	Number int
	Body   string
}

func Plan(result Result, runID string, open []OpenIssue) []Action
```

Sentinel errors in the package: `ErrMissingField`, `ErrBadNumber`, `ErrBadName`, `ErrUnitMismatch`, `ErrBadScope`, `ErrTooFewSamples`, `ErrNoEvents`. Each adapter and `ParseBudgets` wrap them with `fmt.Errorf("...: %w", err)`.

Command of the NEW file `perf/compare/main.go`: `perfcompare -budgets perf/budgets.json -open open-issues.json -run-id <id> -out plan.json -summary summary.md <measurements.json>...`. Exit status 0: no breach. 1: at least one breach (the plan is written). 2: an error of the kinds in decision 6 (no plan). The summary is a Markdown table with columns name, scope, measured, budget, unit and run id.

## Structure

No server structure changes. The only stored reference is `perf/budgets.json`, keyed by the stable measurement name and scope, never by branch, runner or run names. The compare logic is pure: it does no network call and reads no environment variable. `perf/compare/main.go` is the only place that exits.

## Hostile input (defense in depth)

The workflow has `issues: write` and builds issue text from files. Treat every report, every budget file and the issue list as untrusted. One table for each layer, with a test per row that fails when the guard is removed:

| Layer | Hostile input | Guard |
| --- | --- | --- |
| Adapter | A report name or key with control characters, a newline, markdown, `@` or a shell word | The name is built from a fixed prefix and an integer. Free text of a report never enters a name or a title. |
| Adapter | A negative, NaN, infinite or missing number; an unknown store | `ErrBadNumber`, `ErrMissingField`, `ErrBadScope`; the run is an infrastructure failure, not a pass |
| Budgets | An entry with a name outside `[a-z0-9._]+@(sqlite\|postgres)`, a negative or non-finite limit, an unknown field, a duplicate key | `ParseBudgets` rejects the file (`ErrBadName`, `ErrBadNumber`); the job fails with no issue |
| Budgets | A unit that differs from the measurement | `ErrUnitMismatch`; never a pass |
| Plan | An open-issue title that equals the measurement title plus text, or differs in case or spaces | exact string equality; a near match opens a new issue |
| Plan | Issue body text from a measured string | the body holds only the name, scope, numbers, unit and run id that the program formats; `runID` must match `[0-9]+` |
| Workflow | Title or body in a shell command | the step reads `plan.json` with `jq -r` into variables and passes them as arguments to `gh`; no interpolation of `${{ }}` into `run:` text |
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
