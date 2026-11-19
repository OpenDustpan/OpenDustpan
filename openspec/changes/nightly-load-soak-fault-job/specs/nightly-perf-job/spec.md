## ADDED Requirements

### Requirement: nightly-compare-breach

When the nightly job finishes a measurement, the nightly job SHALL compare each of its p50, p95 and max values with the same field of the budget entry in `perf/budgets.json` for that name, with zero tolerance.

#### Scenario: within budget passes
- **WHEN** every measured field is at or below its budget field
- **THEN** the job passes and uploads the measurements as an artifact

#### Scenario: over budget fails and files issue
- **WHEN** a measured p50, p95 or max is greater than the budget field of the same name
- **THEN** the job fails and opens one issue with the measurement name, the suite, the measured value, the budget and the run id

#### Scenario: stale budget entry warns
- **WHEN** a budget entry names a measurement or field that the run did not produce
- **THEN** the job logs a warning and does not fail for that entry

#### Scenario: unit mismatch fails closed
- **WHEN** a budget entry names a unit that differs from the unit of the measurement
- **THEN** the job fails with no regression issue and the log names the measurement

#### Scenario: budget with one field
- **WHEN** a budget entry holds only `max` and the measurement has a p50
- **THEN** the p50 is record-only and does not breach

### Requirement: nightly-compare-first-run

If a measurement or a measured field has no budget entry, then the nightly job SHALL treat it as record-only and upload the measured values without opening a regression issue.

#### Scenario: first run records values
- **WHEN** `perf/budgets.json` has no entry for a measurement
- **THEN** the job passes, uploads the measurements and files no regression issue

### Requirement: nightly-infra-failure

If a suite ends without a readable report, then the nightly job SHALL fail the run without opening a regression issue.

#### Scenario: no measurement means no regression issue
- **WHEN** a suite ends without a measurement because a service container, the harness, a timeout, a cancellation, a report that cannot be read or an upload failed
- **THEN** the job fails with no regression issue and the log names the failed container or step

#### Scenario: issue call fails
- **WHEN** a `gh issue` call fails
- **THEN** the job fails, keeps the measurements artifact and opens or closes no further issue

### Requirement: nightly-issue-dedup

While a regression issue for a measurement stays open, the nightly job SHALL add a comment on a repeat breach and close the issue on recovery. The issue title is exactly `[nightly-perf] <name>`, matched by string equality.

#### Scenario: repeat breach comments
- **WHEN** a measurement breaches while its regression issue is open
- **THEN** the job adds a comment with the new numbers and opens no second issue

#### Scenario: recovery closes issue
- **WHEN** a measurement returns within its budget while its issue is open
- **THEN** the job closes the issue with a recovery comment

#### Scenario: near-match title
- **WHEN** an open issue has a title that differs from the exact title in case, spaces or extra text
- **THEN** the job treats it as another issue and opens a new one

### Requirement: nightly-schedule

Where the nightly workflow runs, the nightly job SHALL run the load, soak and Toxiproxy fault suites on a nightly schedule and on manual dispatch, and never on a pull request.

#### Scenario: nightly and manual dispatch
- **WHEN** the schedule fires or a person runs `gh workflow run nightly-perf.yml`
- **THEN** the load, soak and fault jobs start on service containers

#### Scenario: not on a pull request
- **WHEN** a pull request runs checks
- **THEN** the nightly-perf workflow does not run

#### Scenario: fork pull request
- **WHEN** a pull request comes from a fork
- **THEN** the nightly-perf workflow does not run, uses no secret and writes no budget
