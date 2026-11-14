## ADDED Requirements

### Requirement: ci-per-workspace-handling

When the server receives workspace requests from one Terragrunt run-all command in team CI, the server SHALL handle each workspace request as an independent unit operation.

#### Scenario: three units apply from one CI job
- **WHEN** one CI job runs terragrunt run-all apply over 3 units with an access token
- **THEN** each of the 3 workspaces holds 1 new state version and 1 history entry, and no change order exists

#### Scenario: one of three units fails in CI
- **WHEN** one CI job runs the command over 3 units and one unit fails
- **THEN** the failed workspace holds no new state version and each other workspace keeps its own result

### Requirement: no-change-order-in-ci

Where Terragrunt run-all runs in team CI, the server SHALL create no change order for the run.

#### Scenario: ci run leaves no multi-unit object
- **WHEN** team CI runs the command over 3 units
- **THEN** the server holds no change order and no discovery job for the run

### Requirement: same-workspace-address

When two Terragrunt units in team CI name the same workspace address, the server SHALL handle both units as requests on one workspace.

#### Scenario: two units name one workspace
- **WHEN** two units name the same workspace address
- **THEN** both requests use one lock and one state lineage

### Requirement: ci-lock-refusal

When two CI steps contend for one workspace lock, the server SHALL refuse the second holder.

#### Scenario: second CI step contends for one locked workspace
- **WHEN** two CI steps lock the same workspace at the same time
- **THEN** the server refuses the second holder and keeps the state lineage unchanged

### Requirement: ci-credential-refusal

When a team CI request carries a missing credential or a credential without a grant on the named workspace, the server SHALL deny the request.

#### Scenario: CI job sends missing credential
- **WHEN** a CI job sends a missing or not valid access token
- **THEN** the server denies the request and reads and writes no state

#### Scenario: token for workspace A used on workspace B
- **WHEN** an access token granted only on workspace A names workspace B
- **THEN** the server denies the request on workspace B
