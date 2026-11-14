## ADDED Requirements

### Requirement: Tick

When the 15-minute tick fires, the server SHALL start each due sweep and each due daily state check.

#### Scenario: Tick
- **WHEN** the 15-minute tick fires with due targets and due offsets
- **THEN** each due target starts once and each due namespace records one sweep of kind state

### Requirement: Tick lifecycle triggers

When a unit is soft-deleted, restored, or purged, the server SHALL record a sweep of kind state for its namespace.

#### Scenario: Unit deleted restored or purged
- **WHEN** a unit is soft-deleted, restored, or purged
- **THEN** the server records one sweep of kind state for that namespace

### Requirement: Daily offset

When a namespace reaches its daily offset, the server SHALL record a state check of kind state for that namespace.

#### Scenario: Namespace reaches offset
- **WHEN** a namespace with at least one unit reaches its StateCheckOffset tick
- **THEN** the server records a sweep of kind state with the namespace ID

### Requirement: RunStateCheck

If RunStateCheck runs, then the server SHALL classify contested keys and open finding keys from the resource index.

#### Scenario: RunStateCheck
- **WHEN** RunStateCheck runs against the committed resource index
- **THEN** the server updates left-behind and managed-twice marks from contested keys and open finding keys

### Requirement: RunStateCheck keeps purged finding open

If a purged unit holds the only copy of a finding key, then the server SHALL keep the finding open.

#### Scenario: Purged unit key with no holder
- **WHEN** RunStateCheck finds a key held only by a purged unit
- **THEN** the finding stays open and keeps the unit ID and unit name at delete time

### Requirement: Refuse double start

While a sweep of the same target runs, the server SHALL refuse a second start with the NEW error code `unmanaged.scan-busy`.

#### Scenario: Second start refused
- **WHEN** a start arrives while a sweep of the same target or a state check of the same namespace runs
- **THEN** the server refuses the second start with the NEW error code `unmanaged.scan-busy`

### Requirement: Refuse stale fence

If the namespace fence moved, then the server SHALL refuse the state-check start.

#### Scenario: Fence moved before write
- **WHEN** the namespace fence moved since the tick read it
- **THEN** the server refuses the start and records no sweep

### Requirement: State check route

When a caller with namespace:start-sweep calls the NEW route `POST /api/dustpan/v1/namespaces/{namespace}/state-checks`, the server SHALL record a sweep of kind state.

#### Scenario: Manual start with grant
- **WHEN** a caller with namespace:start-sweep calls the NEW route `POST /api/dustpan/v1/namespaces/{namespace}/state-checks`
- **THEN** the server records a sweep of kind state and answers 202

### Requirement: Audit state check

When a state check ends, the server SHALL append an audit entry with the namespace ID and the result counts.

#### Scenario: State check audited
- **WHEN** a state check ends in success or failure
- **THEN** the audit log holds one entry with namespace ID, kind state, and result counts
