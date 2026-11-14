## ADDED Requirements

### Requirement: Pre-merge apply option

The server SHALL store a per-unit applyTiming value with default post-merge.

#### Scenario: Enable option
- **WHEN** a namespace administrator sets applyTiming pre-merge on a unit
- **THEN** the API reports applyTiming pre-merge as the effective value.

#### Scenario: Default value read
- **WHEN** a unit never sets the value
- **THEN** the API reports applyTiming post-merge as the effective value.

### Requirement: Branch apply on exact head

When a review request is approved on the exact branch head commit with a plan on that commit, the server SHALL start the apply pinned to that commit SHA.

#### Scenario: Approved branch apply
- **WHEN** an approved review request with a plan on the exact head applies on a unit with applyTiming pre-merge
- **THEN** the server starts the apply job pinned to that commit SHA.

### Requirement: Default refusal

When a branch apply is requested on a unit that keeps the default, the server SHALL refuse the request with an error that names applyTiming pre-merge.

#### Scenario: Default unit refusal
- **WHEN** a branch apply is requested on a unit with the default
- **THEN** the server refuses with an error that names applyTiming pre-merge.

### Requirement: Stale approval refusal

When the branch head moves after approval and before the job starts, the server SHALL refuse the apply.

#### Scenario: Moved head refusal
- **WHEN** the branch head moves after approval and before the job starts
- **THEN** the server refuses the apply.

#### Scenario: Missing approval refusal
- **WHEN** a branch apply is requested with no approval on the current head
- **THEN** the server refuses the apply with an error that names the missing approval.

### Requirement: No plan refusal

When a branch apply is requested with no plan on the exact branch head commit, the server SHALL refuse the apply.

#### Scenario: Missing plan refusal
- **WHEN** a branch apply is requested with no plan on the exact head
- **THEN** the server refuses the apply with an error that names the missing plan.

### Requirement: Setting authorization

Where the unit settings change, the server SHALL allow the applyTiming change only to a principal authorized to change unit settings.

#### Scenario: Denied setting change
- **WHEN** a principal without permission changes the value
- **THEN** the server denies the change.

### Requirement: Toggle during run

While an apply job runs, the server SHALL continue that job when the option changes.

#### Scenario: Running job continues
- **WHEN** the option changes while an apply job runs
- **THEN** the server runs that job to its end.

### Requirement: Re-check before first change

When the branch head moves between the start check and the first change, the server SHALL stop the pinned apply job without the change.

#### Scenario: Head moves before first change
- **WHEN** the branch head moves before the first change
- **THEN** the server stops the job without the change.

### Requirement: Forge check failure

If the forge head check fails, then the server SHALL stop the apply without the change.

#### Scenario: Forge outage
- **WHEN** the forge does not answer at the re-check
- **THEN** the server stops the job without the change and writes an audit entry.

### Requirement: One writer

While a unit lock is held, the server SHALL refuse a second apply on that unit as locked.

#### Scenario: Concurrent requests
- **WHEN** two branch apply requests arrive for one unit
- **THEN** one job runs and the other request is refused as locked.
