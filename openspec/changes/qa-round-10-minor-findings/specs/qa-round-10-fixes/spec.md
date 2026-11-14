## ADDED Requirements

### Requirement: Temp-admin reason hidden text

When a temporary-administrator reason holds U+FE0F, NBSP, or invalid UTF-8, the server SHALL answer 400.

#### Scenario: Reason with variation selector
- **WHEN** a temporary-administrator reason holds U+FE0F
- **THEN** the server answers 400 and stores nothing

### Requirement: Job repository hidden text

When a job location repository holds U+FE0F, NBSP, or invalid UTF-8, the server SHALL answer 400.

#### Scenario: Repository with variation selector
- **WHEN** a job location repository holds U+FE0F
- **THEN** the server answers 400 and stores nothing

### Requirement: Deleted-unit cursor hidden text

When a deleted-unit cursor holds U+FE0F, NBSP, or invalid UTF-8, the server SHALL answer 400.

#### Scenario: Cursor with variation selector
- **WHEN** a deleted-unit cursor holds U+FE0F
- **THEN** the server answers 400

### Requirement: Person name case

When a person name differs from a stored name only in Unicode simple case folding, the server SHALL refuse the duplicate.

#### Scenario: Case-only duplicate refused
- **WHEN** the name Frank is created after frank
- **THEN** the server refuses it and stores nothing

### Requirement: Public lock error

When a lock read finds no manual lock, the server SHALL answer 404 with a public not-found answer.

#### Scenario: Lock read without lock
- **WHEN** a lock read finds no manual lock
- **THEN** the answer holds no storage text

### Requirement: Public drift-start error

When a drift start request is not valid, the server SHALL answer 400 with a public invalid answer.

#### Scenario: Bad drift start
- **WHEN** a drift start request is not valid
- **THEN** the answer holds no inner chain

### Requirement: Registry version echo cap

When a registry request carries a version string longer than 100 characters, the server SHALL echo at most 100 characters.

#### Scenario: Long version capped
- **WHEN** a registry request carries a 400-character version
- **THEN** the echo holds at most 100 characters

### Requirement: Succeeded restore cancel

When a cancel arrives for a succeeded restore, the server SHALL answer 409 with code restore.ended.

#### Scenario: Succeeded restore cancel
- **WHEN** a cancel arrives for a succeeded restore
- **THEN** the server answers 409 with code restore.ended

### Requirement: Failed-from-cancel second cancel

When a cancel arrives for a restore that already failed from a cancel, the server SHALL answer 202 with no state change.

#### Scenario: Second cancel
- **WHEN** a cancel arrives for a restore that failed from a cancel
- **THEN** the server answers 202 with no state change

### Requirement: Body content type

When a namespace create arrives with Content-Type text/plain, the server SHALL answer 415.

#### Scenario: Text plain refused
- **WHEN** a namespace create arrives with Content-Type text/plain
- **THEN** the server answers 415 and creates nothing

### Requirement: Release tag rule

When a pre-release tag is given, the script SHALL refuse the tag and name the stable-only rule.

#### Scenario: Prerelease refused
- **WHEN** the tag v1.2.3-rc1 is given
- **THEN** the script names the stable-only rule

### Requirement: Release record line endings

When a CRLF release record is given, the script SHALL normalize line endings before the check.

#### Scenario: CRLF record checked
- **WHEN** a CRLF release record is given
- **THEN** the check shows no equal-looking diff

### Requirement: Sweep log line

When an artifact sweep deletes zero blobs, the server SHALL log the documented artifact-sweep line.

#### Scenario: Sweep line matches guide
- **WHEN** an artifact sweep deletes zero blobs
- **THEN** the log line and the guide use the same text
