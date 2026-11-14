## ADDED Requirements

### Requirement: resolve-commit-at-start

When a job starts with the trusted-source check on, the server SHALL call ResolveCommitAtStart to resolve the requested revision to a commit from the Git host and store the branch and the resolved commit.

#### Scenario: Head resolution
- **WHEN** a job starts with a branch revision and the setting is on
- **THEN** ResolveCommitAtStart stores the branch, the resolved commit, the repository ID, and the verdict on the job

#### Scenario: Setting off
- **WHEN** the setting is off
- **THEN** the server uses the requested revision as main does

#### Scenario: Tag or raw SHA
- **WHEN** the revision is a tag or raw SHA and the setting is on
- **THEN** the resolved commit equals the requested revision with empty branch and verdict untrusted

### Requirement: trusted-source-verdict

If the Git host cannot report the branch rules, then the server SHALL treat the source as untrusted.

#### Scenario: Host cannot report
- **WHEN** the Git host cannot report branch rules
- **THEN** the verdict is untrusted and protected-branch secrets are denied

#### Scenario: Force push
- **WHEN** a force push happened since the last approved apply
- **THEN** the verdict is untrusted

#### Scenario: Merge around gate
- **WHEN** a merge landed around the gate window
- **THEN** the verdict is untrusted

#### Scenario: Forge timeout or denial
- **WHEN** the forge times out, rate-limits, or denies auth
- **THEN** the start succeeds with verdict untrusted and protected-branch secrets denied
