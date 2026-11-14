## ADDED Requirements

### Requirement: moved-head bound

When the branch head moves during Commit, the server SHALL retry on the new head at most three times.

#### Scenario: moved head retries
- **WHEN** the branch head moves during Commit
- **THEN** the adapter retries the commit on the new head and stops after the third retry

#### Scenario: fourth move
- **WHEN** the head moves a fourth time
- **THEN** the adapter writes no extra commit and returns ErrConflict

### Requirement: write access

When the credential set lacks write access, the server SHALL refuse Commit with ErrRefused.

#### Scenario: missing write grant
- **WHEN** the credential set lacks write access
- **THEN** the adapter refuses with ErrRefused

### Requirement: missing repository

When the repository is missing, the server SHALL refuse Commit and FindCommit with the missing-repository error.

#### Scenario: missing repo on commit
- **WHEN** Commit names a missing repository
- **THEN** the adapter returns the missing-repository error

#### Scenario: missing repo on find
- **WHEN** FindCommit names a missing repository
- **THEN** the adapter returns the missing-repository error

### Requirement: trailer block

When the adapter matches a trailer, the server SHALL match only a trailer block in the last paragraph.

#### Scenario: prose plus trailer
- **WHEN** the message ends with prose plus a trailer
- **THEN** hasTrailer accepts the message only when the last paragraph is a trailer block

### Requirement: app author

When write-back commits through the GitHub adapter, the server SHALL name the GitHub App as the commit author.

#### Scenario: app author on commit
- **WHEN** write-back commits through the adapter
- **THEN** the commit names the GitHub App as author and the person in a Co-authored-by trailer

### Requirement: co-author trailer

When write-back commits through the GitHub adapter, the server SHALL add one Co-authored-by trailer for the person.

#### Scenario: person trailer
- **WHEN** write-back commits through the adapter
- **THEN** the message holds one Co-authored-by trailer for the person

### Requirement: signature wait

While signing waits for the owner decision, the server SHALL state in the adapter doc that the form follows the recommendation.

#### Scenario: signature pending
- **WHEN** a reader checks how the adapter signs
- **THEN** the adapter doc states that signing waits for the owner decision

### Requirement: ancestry no patches

While checking ancestry, the server SHALL read no file patches.

#### Scenario: busy branch ancestry
- **WHEN** the branch holds many files
- **THEN** the ancestry check reads no file patches and returns no false ErrConflict

### Requirement: find limit

If more than 500 commits exist since since, then the server SHALL return ErrUnavailable from FindCommit.

#### Scenario: too many commits
- **WHEN** more than 500 commits exist since since
- **THEN** the adapter returns ErrUnavailable

#### Scenario: exactly 500 commits
- **WHEN** exactly 500 commits exist since since
- **THEN** the adapter returns the commits without ErrUnavailable

### Requirement: since clock

When FindCommit receives since, the server SHALL state the server clock for since and subtract a 5-minute skew margin.

#### Scenario: skew margin
- **WHEN** FindCommit receives since
- **THEN** the adapter subtracts a 5-minute skew margin from since before the search
