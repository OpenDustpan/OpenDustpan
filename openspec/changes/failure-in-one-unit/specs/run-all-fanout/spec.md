## ADDED Requirements

### Requirement: failed-apply-promotes-no-current-state

When an apply job of a fan-out change order fails, the server SHALL keep the prior current state version for its workspace.

#### Scenario: failed unit keeps prior current state
- **WHEN** the apply job for one of the three workspaces fails
- **THEN** the workspace keeps its prior current state version and the job outcome is failed with the failure reason

#### Scenario: crash during apply promotes nothing
- **WHEN** the runner crashes in the middle of a fan-out apply job
- **THEN** the job outcome is failed and the workspace keeps its prior current state version

#### Scenario: cancelled apply promotes nothing
- **WHEN** a fan-out apply job is cancelled before it promotes state
- **THEN** the job outcome is cancelled and the workspace keeps its prior current state version

### Requirement: failure-record-names-unit-and-reason

When an apply job of a fan-out change order fails, the server SHALL write the workspace stable ID and the failure reason on its history entry and its audit entry.

#### Scenario: history and audit name the failed unit
- **WHEN** an apply job of a fan-out change order fails
- **THEN** the history entry and the audit entry name the workspace stable ID and the failure reason

#### Scenario: provider secret is not stored in the record
- **WHEN** the provider failure text holds a secret value
- **THEN** the records carry the failure reason without the secret value

### Requirement: failed-apply-releases-lock

When a fan-out apply job fails or is cancelled, the server SHALL release the workspace lock of its workspace.

#### Scenario: lock releases after failure
- **WHEN** a fan-out apply job fails or is cancelled
- **THEN** the workspace lock is released and a later claim takes it

#### Scenario: lock releases after a second failure
- **WHEN** a later apply job for the same workspace also fails
- **THEN** the workspace lock is released again and the prior current state version stays

### Requirement: other-units-keep-own-outcome

When one apply job of a fan-out change order fails, the server SHALL leave each other apply job to its own outcome under the existing change order rules.

#### Scenario: other units keep their own outcomes
- **WHEN** one apply job of a three-unit fan-out change order fails
- **THEN** each apply job of the other two workspaces keeps its own outcome and its own current state version rule

### Requirement: change-order-reflects-partial-result

When one apply job of a fan-out change order fails, the server SHALL report the per-unit outcomes on the change order under the existing change order rules.

#### Scenario: change order shows partial result
- **WHEN** one apply job of a three-unit fan-out change order fails
- **THEN** the change order lists one failed unit outcome and one outcome for each other unit under the existing change order rules
