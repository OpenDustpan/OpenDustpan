## ADDED Requirements

### Requirement: Pin source

When the scanner runs, the server SHALL read fixed-version pins from unit settings and compare them with private registry releases only.

#### Scenario: Fixed pin in settings scanned
- **WHEN** unit settings hold a fixed provider or module version
- **THEN** the server includes that pin in the scan.

#### Scenario: Branch pin skipped
- **WHEN** unit settings hold a branch name instead of a fixed version
- **THEN** the server creates no change record.

#### Scenario: Public release ignored
- **WHEN** only a public upstream holds a newer version and the private registry holds none
- **THEN** the server creates no change record for that release.

### Requirement: Scan schedule

When the daily scan runs for a namespace, the server SHALL compare fixed-version pins of its units with private registry releases.

#### Scenario: Daily scan finds stale pin
- **WHEN** the daily scan runs and a fixed pin is older than a private registry release
- **THEN** the server creates one change record for that unit stable ID, pin, and new version.

#### Scenario: Overrun skips run
- **WHEN** the prior daily scan for the namespace still runs
- **THEN** the server skips the run and records the skip.

### Requirement: Scan setting

When a caller reads namespace settings, the server SHALL return the effective value of automatic upgrade pull requests.

#### Scenario: Disabled setting skips scan
- **WHEN** automatic upgrade pull requests are turned off for the namespace
- **THEN** the server creates no change record.

#### Scenario: Effective value visible
- **WHEN** a caller reads the namespace settings
- **THEN** the server returns the effective value of automatic upgrade pull requests.

### Requirement: Select newest stable

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

### Requirement: Open once

When two opens carry the same unit stable ID, pin, and new version, the server SHALL keep one open upgrade request for that key.

#### Scenario: Stale pin opens request
- **WHEN** a unit pins a fixed version older than a private registry release
- **THEN** the server opens one pull request on the forge that changes the pin to the newer version.

#### Scenario: Concurrent scans open once
- **WHEN** two scanner runs find the same unit stable ID, pin, and new version at the same time
- **THEN** the server keeps one open upgrade request for that key.

### Requirement: Branch idempotency

When the writer opens a forge request, the server SHALL look up an open request by deterministic branch name before create.

#### Scenario: Crash after forge create stores reference
- **WHEN** the process dies after forge create and the next scan finds the forge request by branch name
- **THEN** the server stores the found reference without opening a second request.

### Requirement: Run plan

When an upgrade pull request opens, the server SHALL start one plan for the changed unit through the milestone 5 plan path.

#### Scenario: Plan starts for changed unit
- **WHEN** an upgrade pull request opens
- **THEN** the server starts one plan for the changed unit through the milestone 5 plan path.

#### Scenario: Plan failure links to request
- **WHEN** the plan for an upgrade pull request fails
- **THEN** the server stores the failure and links it to the upgrade request.

#### Scenario: Request closed before plan end
- **WHEN** the pull request closes before the plan finishes
- **THEN** the server stores the plan result without reopening the pull request.

### Requirement: Report plan status

When the plan for an upgrade request finishes, the server SHALL post the plan result to the forge pull request.

#### Scenario: Plan result posted to forge
- **WHEN** the plan for an upgrade request finishes
- **THEN** the server posts the plan result as a status check on the forge pull request.

### Requirement: Record result

The server SHALL store old version, new version, pull request reference, and plan result for each upgrade request.

#### Scenario: Result stored with versions
- **WHEN** the server opens an upgrade pull request
- **THEN** the server stores old version, new version, pull request reference, and plan result.

### Requirement: Refuse without forge path

When the forge path cannot open a request, the server SHALL store the refusal on the upgrade request and skip open.

#### Scenario: No forge link stored
- **WHEN** the unit has no forge link
- **THEN** the server stores no-forgelink and skips open until a forge link exists.

#### Scenario: Revoked credential stored
- **WHEN** the forge credential is revoked or expired
- **THEN** the server stores forge-credential-invalid and retries on the next scan.

#### Scenario: Rate limit backs off
- **WHEN** the forge answers rate limited
- **THEN** the server stores forge-rate-limited and retries on the next scan.

#### Scenario: Branch collision reused
- **WHEN** the deterministic branch name already exists on the forge
- **THEN** the server reuses the existing branch request without a second open.

#### Scenario: Absent pin refused
- **WHEN** the pin is absent from unit settings at open time
- **THEN** the server stores pin-not-found and skips open.

### Requirement: Close request

When the scheduled poll finds the forge pull request merged or closed, the server SHALL mark the upgrade request closed.

#### Scenario: Merged pull request closes request
- **WHEN** the scheduled poll finds the forge pull request merged
- **THEN** the server marks the upgrade request closed as merged.

#### Scenario: Closed pull request closes request
- **WHEN** the scheduled poll finds the forge pull request closed without merge
- **THEN** the server marks the upgrade request closed without a version change and suppresses reopen for 7 days.

#### Scenario: Hand-changed pin closes request
- **WHEN** the pin in unit settings changes by hand while the upgrade request stays open
- **THEN** the server marks the upgrade request closed as superseded.
