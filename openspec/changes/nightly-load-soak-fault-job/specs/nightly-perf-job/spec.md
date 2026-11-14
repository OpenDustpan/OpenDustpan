## ADDED Requirements

### Requirement: nightly-compare-breach

When the nightly job finishes a measurement, the server SHALL compare the measurement with the budget in the NEW file `perf/budgets.json`.

#### Scenario: within budget passes
- **WHEN** the nightly job finishes a measurement within its budget
- **THEN** the job passes and uploads the measurements as an artifact

#### Scenario: over budget fails and files issue
- **WHEN** a measurement is greater than its budget in p50, p95 or max
- **THEN** the job fails and opens one issue with the measurement name, the measured value, the budget and the run id

#### Scenario: stale budget entry warns
- **WHEN** a budget entry names a measurement that the run did not produce
- **THEN** the job logs a warning and does not fail for that entry

### Requirement: nightly-compare-first-run

If a measurement has no budget entry, then the server SHALL treat the run as record-only and upload the measured values without opening a regression issue.

#### Scenario: first run records values
- **WHEN** a measurement has no budget entry in the NEW file `perf/budgets.json`
- **THEN** the job passes, uploads the measurements and files no regression issue

### Requirement: nightly-infra-failure

If the nightly job ends without a measurement, then the server SHALL fail the run without opening a regression issue.

#### Scenario: no measurement means no regression issue
- **WHEN** a job ends without a measurement because a service container, the harness, a timeout, a cancellation or an upload failed
- **THEN** the job fails with no regression issue and the log names the failed container or step

#### Scenario: issue call fails
- **WHEN** a `gh issue` call fails
- **THEN** the job fails, keeps the measurements artifact and opens or closes no issue

### Requirement: nightly-issue-dedup

While a regression issue for a measurement stays open, the server SHALL add a comment on a repeat breach and close the issue on recovery.

#### Scenario: repeat breach comments
- **WHEN** a measurement breaches while its regression issue is open
- **THEN** the job adds a comment with the new numbers and opens no second issue

#### Scenario: recovery closes issue
- **WHEN** a measurement returns within its budget
- **THEN** the job closes the open issue with a recovery comment

### Requirement: nightly-schedule

Where the nightly workflow runs, the server SHALL run the load, soak and Toxiproxy fault jobs on a nightly schedule and on manual dispatch, and never on each pull request.

#### Scenario: nightly and manual dispatch
- **WHEN** the schedule fires or a person runs the manual dispatch command
- **THEN** the load, soak and Toxiproxy fault jobs start on service containers

#### Scenario: not on each pull request
- **WHEN** a pull request runs checks
- **THEN** the load, soak and Toxiproxy fault jobs do not run

#### Scenario: fork pull request
- **WHEN** a pull request comes from a fork
- **THEN** the workflow does not run, uses no secret and writes no budget
