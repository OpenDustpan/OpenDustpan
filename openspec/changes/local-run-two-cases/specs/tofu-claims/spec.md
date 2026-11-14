## ADDED Requirements

### Requirement: local-overlap-retry

When the claim of a local wrapper run overlaps a running claim, the server SHALL refuse the claim and return the blocking claim identifier with a retry hint.

#### Scenario: overlap waits then applies on the new base
- **WHEN** the local change overlaps a running claim
- **THEN** the wrapper makes no provider call until the blocking claim ends, then discards the old plan, plans again on the new state version, and applies, and the claim record shows a start time after the end time of the blocking claim

### Requirement: local-disjoint-accept

When the claim of a local wrapper run is disjoint from each running claim, the server SHALL accept the claim and serve the base state version named by the claim.

#### Scenario: disjoint local claim applies side by side
- **WHEN** the local change is disjoint from each running claim
- **THEN** its apply period overlaps the running apply in the provider log, the history shows the claim of each state version, and no resource of the two changes is lost

### Requirement: local-retry-state

Where a local wrapper waits on a blocking claim, the server SHALL return the blocking claim identifier, the claim times, and the retry hint on each refusal.

#### Scenario: waiting wrappers spread load
- **WHEN** two local runs wait on the same blocking claim
- **THEN** each wrapper waits with exponential backoff with jitter and honors the retry hint and the 503 Retry-After

#### Scenario: timeout exits without provider call
- **WHEN** the timeout ends before the blocking claim ends
- **THEN** the wrapper exits with an error and makes no provider call

#### Scenario: progress stays visible
- **WHEN** the wrapper waits on a blocking claim
- **THEN** the wrapper shows the blocking claim identifier, the retry count, and the remaining timeout on each retry

### Requirement: local-replan-new-base

When the blocking claim ends, the server SHALL serve the new state version as the base for the next register attempt of the waiting wrapper.

#### Scenario: old plan is discarded
- **WHEN** the blocking claim ends and the wrapper holds a plan from the old base
- **THEN** the wrapper makes a new plan on the new state version before any provider write

#### Scenario: replan meets a new blocker
- **WHEN** the new register attempt overlaps another running claim
- **THEN** the wrapper re-enters the wait loop until the new blocking claim ends or until the timeout ends, with no provider call before accept

### Requirement: local-auth-refuse

When the caller of a local wrapper register lacks write on the workspace, the server SHALL refuse the register with 403 naming the missing action and the workspace object.

#### Scenario: denied caller exits at once
- **WHEN** the caller lacks write on the workspace
- **THEN** the wrapper exits at once with no retry and no provider call

### Requirement: local-expired-fence-refuse

When an upload of a local wrapper carries an expired claim or a stale fence, the server SHALL refuse the upload and keep the current state version unchanged.

#### Scenario: expired claim stops the apply
- **WHEN** the held claim expires or the fence refuses the upload mid-apply
- **THEN** the wrapper stops, discards the claim, and makes no further provider call

#### Scenario: server gap during apply fails closed
- **WHEN** a 503 with Retry-After arrives during the local apply
- **THEN** the wrapper stops without marking success and makes no further provider call until a new accepted claim exists

### Requirement: local-interrupt-release

When a local wrapper ends its wait or its held claim before success, the server SHALL leave no wrapper claim held after the release path ends.

#### Scenario: interrupt during wait holds nothing
- **WHEN** the person interrupts the wrapper during the wait
- **THEN** the waiting request ends with no claim held and no provider call

#### Scenario: interrupt after accept releases the claim
- **WHEN** the person interrupts the wrapper after accept and before the apply ends
- **THEN** the wrapper releases the held claim through the release path of two-disjoint-claims and makes no further provider call
