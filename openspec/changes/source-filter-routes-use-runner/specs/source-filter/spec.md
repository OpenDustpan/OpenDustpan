## ADDED Requirements

### Requirement: Separate read action

When a caller sends GET to /api/dustpan/v1/namespaces/{namespace}/source-filter, the server SHALL require the NEW action `namespace:read-source-filter`.

#### Scenario: Runner reader refused
- **WHEN** a caller with only `namespace:read-runners` sends GET to the source-filter route
- **THEN** the server answers 403 with `access.denied` and returns no filter.

#### Scenario: Filter reader allowed
- **WHEN** a caller with the NEW action `namespace:read-source-filter` sends GET to the source-filter route
- **THEN** the server answers 200 with the stored filter through getSourceFilter.

#### Scenario: Manage-only caller refused on read
- **WHEN** a caller with only `namespace:manage-runners` sends GET to the source-filter route
- **THEN** the server answers 403 with `access.denied` and returns no filter.

#### Scenario: Cross-namespace grant refused
- **WHEN** a caller with the NEW action `namespace:read-source-filter` on another namespace sends GET for this namespace
- **THEN** the server answers 403 with `access.denied` and returns no filter.

### Requirement: Separate manage action

When a caller sends PUT to /api/dustpan/v1/namespaces/{namespace}/source-filter, the server SHALL require the NEW action `namespace:manage-source-filter`.

#### Scenario: Runner admin refused
- **WHEN** a caller with only `namespace:manage-runners` sends PUT with protected branches
- **THEN** the server answers 403 with `access.denied` and stores no change.

#### Scenario: Filter manager allowed
- **WHEN** a caller with the NEW action `namespace:manage-source-filter` sends PUT with a valid filter
- **THEN** the server stores the filter through setSourceFilter and answers 200.

#### Scenario: Read-only caller refused on write
- **WHEN** a caller with only the NEW action `namespace:read-source-filter` sends PUT to the source-filter route
- **THEN** the server answers 403 with `access.denied` and stores no change.

### Requirement: Audit action

When a source-filter update succeeds, the server SHALL write an audit entry with the NEW action `namespace:manage-source-filter`.

#### Scenario: Audit names source-filter action
- **WHEN** a caller with the manage action updates the source filter through setSourceFilter
- **THEN** the audit entry names the NEW action `namespace:manage-source-filter` with the actor and the reason.

#### Scenario: Refused change leaves no filter audit write
- **WHEN** a caller without the manage action sends PUT to the source-filter route
- **THEN** the server stores no filter change and writes no success entry for setSourceFilter.

### Requirement: Grant migration

When the server adds the source-filter actions, the server SHALL leave each stored grant unchanged.

#### Scenario: Stored grants do not change
- **WHEN** the server upgrades with the two NEW actions
- **THEN** the stored grant still names its old runner role and gains no new action.

#### Scenario: Administrator previews affected grants
- **WHEN** a namespace administrator calls ANY /api/server/namespaces/{}/access-review for the namespace
- **THEN** the review lists the grants that hold runner actions without the new source-filter actions.
