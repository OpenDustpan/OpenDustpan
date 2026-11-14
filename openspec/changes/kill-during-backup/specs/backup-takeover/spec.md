## ADDED Requirements

### Requirement: ClaimExpiry

When the holder process stops, the server SHALL make the backup claim available to another worker after 30 seconds.

#### Scenario: Holder killed mid-copy
- **WHEN** the suite sends SIGKILL to the holder during a backup of at least 500 MB of blobs and ClaimExpired runs
- **THEN** the backup ends succeeded within the lease period plus 60 seconds and expiryCount is 1

#### Scenario: Slow takeover
- **WHEN** takeover takes longer than the lease period plus 60 seconds but the copy resumes
- **THEN** the backup ends succeeded and the docs record the measured time with its date

### Requirement: CompleteBackup

When a backup ends succeeded after takeover, the server SHALL report it complete to backup check.

#### Scenario: Check complete backup
- **WHEN** the backup ends succeeded after takeover
- **THEN** backup check reports complete

### Requirement: IdempotentResume

When a survivor resumes a killed backup, the server SHALL write only target bytes that match the backup manifest.

#### Scenario: Partial copy resume
- **WHEN** the holder stops with a partial target copy and ResumeCopy runs on the survivor
- **THEN** the target matches the manifest

### Requirement: SurvivorRetry

When a survivor stops before commit, the server SHALL let the next survivor claim after expiry and commit once.

#### Scenario: Survivor stops during takeover
- **WHEN** the first survivor stops before commit and ReclaimAfterCrash runs
- **THEN** the next survivor claims after expiry and the backup ends succeeded or failed with a reason

#### Scenario: Database gap during renewal
- **WHEN** the database is unreachable during renewal
- **THEN** the claim stays running when the gap is shorter than 30 seconds and expires when the gap is longer
