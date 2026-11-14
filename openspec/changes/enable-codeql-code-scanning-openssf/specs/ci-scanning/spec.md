## ADDED Requirements

### Requirement: codeql-workflow

The repository SHALL hold a CodeQL workflow for Go that runs on push, on pull request and on a weekly schedule.

#### Scenario: codeql-workflow-present
- **WHEN** the change is applied
- **THEN** the file `.github/workflows/codeql.yml` runs Go with autobuild and the security-extended queries

#### Scenario: gate-skips-private-and-fork-upload
- **WHEN** a run starts on a private repo without GHAS or on a fork pull request
- **THEN** the SARIF upload step is skipped

### Requirement: scorecard-workflow

The repository SHALL hold an OpenSSF Scorecard workflow that runs on a schedule.

#### Scenario: scorecard-workflow-present
- **WHEN** the change is applied
- **THEN** the file `.github/workflows/scorecard.yml` runs on schedule only with publish_results false

### Requirement: pin-and-permissions

The repository SHALL pin each action of the CodeQL workflow and the Scorecard workflow to a full commit SHA.

#### Scenario: pin-and-permissions-checked
- **WHEN** the CodeQL workflow and the Scorecard workflow are read
- **THEN** each action ref is a full 40-character commit SHA and each job declares least-privilege permissions
