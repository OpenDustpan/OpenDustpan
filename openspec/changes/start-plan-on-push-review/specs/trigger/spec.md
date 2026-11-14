## ADDED Requirements

### Requirement: Push starts matching plans

When a push reaches a review request, the server SHALL start a plan job for each matching unit at the head commit of the push.

#### Scenario: Push starts plan at head
- **WHEN** A push reaches a review request that targets the repository and branch of the unit code location.
- **THEN** One plan job exists for each matching unit at the head commit with source kind forge.

### Requirement: Intake authentication

When a push delivery fails signature check or repeats a delivery ID, the server SHALL refuse the delivery without a start.

#### Scenario: Spoofed push refused
- **WHEN** A push delivery fails signature check or repeats a delivery ID.
- **THEN** No plan starts and the intake records the refused delivery.

### Requirement: Paths filter

When a push touches no path of the normalized filter, the server SHALL start no plan for that trigger.

#### Scenario: Unrelated push starts nothing
- **WHEN** A push touches no file under the paths filter of the trigger.
- **THEN** No plan job starts for that unit.

### Requirement: Truncated file list

When the forge payload marks the changed-file list as truncated, the server SHALL match every trigger of the same repository and branch and record the truncation on each job.

#### Scenario: Truncation plans with record
- **WHEN** The forge payload marks the changed-file list as truncated.
- **THEN** Plans start within the open-job limit and each job records the truncation.

### Requirement: Default paths

When a trigger is saved with empty paths, the server SHALL store the cleaned working directory of the unit code location as the filter.

#### Scenario: Empty paths default
- **WHEN** A trigger is saved with empty paths.
- **THEN** The stored filter equals the cleaned working directory.

### Requirement: Bad paths refused

When a trigger is saved with an invalid paths pattern, the server SHALL refuse the save.

#### Scenario: Bad pattern refused
- **WHEN** A trigger is saved with an empty, absolute or escaping pattern.
- **THEN** The save is refused and no trigger is stored.

### Requirement: Fork push refused

When a push comes from a fork, the server SHALL refuse the push and write an audit entry.

#### Scenario: Fork push audited
- **WHEN** A push comes from a fork or the fork flag is missing.
- **THEN** No plan starts and an audit entry records the refused push.

### Requirement: Newer push replaces older plan

When a newer push reaches the same review request, the server SHALL replace the older plan of the same trigger.

#### Scenario: Supersede older plan
- **WHEN** A newer push arrives before the older plan finishes.
- **THEN** The older plan is canceled and only the plan at the newer head commit proceeds.

### Requirement: Refused newer start keeps older plan

When the newer push start is refused for the open-job limit, a freeze, a lock, or an approval rule, the server SHALL keep the older plan.

#### Scenario: Older plan kept
- **WHEN** The newer start is refused.
- **THEN** The older plan keeps its state and no cancel runs.

### Requirement: Automatic start record

When a push starts a plan, the server SHALL write an audit entry for the automatic start.

#### Scenario: Start audit written
- **WHEN** A push starts a plan.
- **THEN** An audit entry records the starter, the trigger and the head commit.
