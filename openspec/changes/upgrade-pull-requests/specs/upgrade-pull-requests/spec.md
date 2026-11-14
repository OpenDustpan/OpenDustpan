## ADDED Requirements

### Requirement: Scan schedule

When the daily scan runs for a namespace, the server SHALL compare fixed-version pins of its units with registry releases.

#### Scenario: Daily scan finds stale pin
- **WHEN** the daily scan runs and a unit pins a fixed version older than a registry release
- **THEN** the server creates one change record for that unit, pin, and new version.

#### Scenario: Disabled setting skips scan
- **WHEN** automatic upgrade pull requests are turned off for the namespace
- **THEN** the server creates no change record.

### Requirement: Show effective setting

When a caller reads namespace settings, the server SHALL return the effective value of automatic upgrade pull requests.

#### Scenario: Effective value visible
- **WHEN** a caller reads the namespace settings
- **THEN** the server returns the effective value of automatic upgrade pull requests for the namespace.

### Requirement: Select newest stable release

When a pinned version is older than registry releases, the server SHALL select the newest stable release above the pinned version.

#### Scenario: Range pin ignored
- **WHEN** the pin value is a version range
- **THEN** the server creates no change record.

#### Scenario: Prerelease skipped
- **WHEN** the registry holds a newer prerelease and a newer stable release
- **THEN** the server selects the newest stable release above the pinned version.

#### Scenario: Downgrade never opens
- **WHEN** the registry release is older than the pinned version
- **THEN** the server creates no change record.

#### Scenario: Second newer version waits
- **WHEN** a second newer release lands while a request for the first newer release stays open
- **THEN** the server keeps the first open request and creates no second request.

### Requirement: Open upgrade request

When a unit pins a fixed version older than a registry release, the server SHALL open one upgrade pull request for that unit stable ID.

#### Scenario: Stale pin opens request
- **WHEN** a unit pins a fixed version older than a registry release
- **THEN** the server opens one pull request on the forge that changes the pin to the newer version.

#### Scenario: Duplicate pin refused
- **WHEN** a pull request is already open for the same unit stable ID and pin
- **THEN** the server refuses the second open with code version-pinned and names the open pull request.

#### Scenario: Missing release refused
- **WHEN** the selected release leaves the registry before open
- **THEN** the server answers 404 with code not-found.

### Requirement: Refuse concurrent duplicate open

When two opens carry the same unit stable ID, pin, and new version, the server SHALL keep one open upgrade request for that key.

#### Scenario: Concurrent scans open once
- **WHEN** two scanner runs find the same unit stable ID, pin, and new version at the same time
- **THEN** the server keeps one open upgrade request for that key.

#### Scenario: Restart opens no duplicate
- **WHEN** the scanner restarts while an upgrade request for the same unit stable ID, pin, and new version stays open
- **THEN** the server keeps one open upgrade request for that key.

### Requirement: Survive partial failure

When the forge pull request opens and the plan start fails, the server SHALL keep the forge reference and retry only the plan start.

#### Scenario: Plan start fails after forge open
- **WHEN** the forge pull request opens and the plan start fails
- **THEN** the server keeps the forge reference and retries only the plan start on the next scan.

### Requirement: Run plan for upgrade

When an upgrade pull request opens, the server SHALL start one plan for the changed unit through the milestone 5 plan path.

#### Scenario: Plan links to request
- **WHEN** the forge pull request for a pin upgrade opens
- **THEN** the server links the plan result to the upgrade pull request.

#### Scenario: Plan failure links to request
- **WHEN** the plan for an upgrade pull request fails
- **THEN** the server stores the failure and links it to the upgrade pull request.

#### Scenario: Plan timeout links to request
- **WHEN** the plan for an upgrade pull request times out
- **THEN** the server stores the timeout and links it to the upgrade pull request.

#### Scenario: Request closed before plan end
- **WHEN** the pull request closes before the plan finishes
- **THEN** the server stores the plan result without reopening the pull request.

### Requirement: Record upgrade result

The server SHALL store old version, new version, pull request reference, and plan result for each upgrade request.

#### Scenario: Result stored with versions
- **WHEN** the server opens an upgrade pull request
- **THEN** the server stores old version, new version, pull request reference, and plan result.

### Requirement: Record actor and authorization

When the server opens an upgrade pull request, the server SHALL record the namespace service principal, the forge credential reference, and the reason.

#### Scenario: Scanner records actor
- **WHEN** the scanner opens an upgrade pull request
- **THEN** the server stores the namespace service principal, the forge credential reference, and the reason.

#### Scenario: Caller without grant refused
- **WHEN** a caller without unit:manage on the unit stable ID calls the open route
- **THEN** the server answers 403 with code forbidden.

### Requirement: Record forge failure

When the forge connection fails during open, the server SHALL record forge-unreachable on the upgrade request.

#### Scenario: Forge unreachable stored
- **WHEN** the forge connection fails during open of an upgrade pull request
- **THEN** the server answers 502 with code forge-unreachable and stores the failure on the upgrade request.

### Requirement: Close upgrade request

When the scheduled poll finds the forge pull request merged or closed, the server SHALL mark the upgrade request closed.

#### Scenario: Merged pull request closes request
- **WHEN** the scheduled poll finds the forge pull request merged
- **THEN** the server marks the upgrade request closed as merged.

#### Scenario: Closed pull request closes request
- **WHEN** the scheduled poll finds the forge pull request closed without merge
- **THEN** the server marks the upgrade request closed without a version change.

#### Scenario: Hand-changed pin closes request
- **WHEN** the pin changes by hand while the upgrade request stays open
- **THEN** the server marks the upgrade request closed as superseded.
