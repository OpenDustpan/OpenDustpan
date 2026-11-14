## ADDED Requirements

### Requirement: store-release-reason

When the coordinator calls releaseClaim with a non-empty reason, the server SHALL store the sanitized reason on the claim record.

#### Scenario: releaseClaim with reason
- **WHEN** the coordinator calls releaseClaim on a claim with no open session with the reason image pull error
- **THEN** the claim record holds the sanitized reason and the management read returns it in releaseReason

### Requirement: empty-reason-keeps-prior

When the release reason is empty after trim, the server SHALL keep the stored reason unchanged.

#### Scenario: empty reason
- **WHEN** the coordinator releases with an empty reason
- **THEN** the release is accepted and the prior reason stays

### Requirement: repeated-release-overwrites

When the coordinator releases the same claim again with a non-empty reason, the server SHALL replace the stored reason.

#### Scenario: overwrite
- **WHEN** two releases of the same claim carry different non-empty reasons
- **THEN** the claim holds the latest reason

#### Scenario: takeover
- **WHEN** a takeover makes a new claim
- **THEN** the new claim holds no reason

### Requirement: oversize-reason-refused

If the release reason holds more than 500 runes, then the server SHALL refuse the release with runner.invalid.

#### Scenario: oversize
- **WHEN** the reason holds 501 runes
- **THEN** the server answers runner.invalid and stores nothing

### Requirement: controls-removed

When the release reason holds control characters, the server SHALL store the reason with CR and LF replaced by spaces and other controls removed.

#### Scenario: controls
- **WHEN** the reason holds CR, LF, NUL, ANSI escapes, and U+2028
- **THEN** the stored reason holds spaces and none of NUL, ESC, or U+2028

#### Scenario: invalid UTF-8
- **WHEN** the reason holds invalid UTF-8
- **THEN** the stored reason holds U+FFFD in its place

### Requirement: management-read-returns-reason

When an operator reads a released claim, the server SHALL return the stored reason in releaseReason.

#### Scenario: read
- **WHEN** the operator reads a released claim with a stored reason
- **THEN** the response holds the reason in releaseReason

### Requirement: wrong-handoff-denied

If the handoff does not name the claim, then the server SHALL deny the release with runner.not-found.

#### Scenario: wrong handoff
- **WHEN** the handoff of another claim is used
- **THEN** the server denies it with runner.not-found and stores nothing

#### Scenario: already released
- **WHEN** the same claim is released twice
- **THEN** the second release with a non-empty reason replaces the reason

#### Scenario: concurrent release
- **WHEN** two releases of one claim run at the same time
- **THEN** one release wins and the stored reason equals one of the two reasons

#### Scenario: session open
- **WHEN** the claim already has an open session
- **THEN** the server refuses the release with runner.invalid

### Requirement: store-failure-keeps-state

If the claim store fails during the release, then the server SHALL keep the claim and its reason unchanged.

#### Scenario: store error
- **WHEN** the claim store fails during the release
- **THEN** the release fails and the prior claim state and reason stay
