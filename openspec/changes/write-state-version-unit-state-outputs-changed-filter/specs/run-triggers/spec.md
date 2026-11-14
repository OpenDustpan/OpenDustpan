## ADDED Requirements

### Requirement: root-outputs digest

The server SHALL store one root-outputs digest with each final state version.

#### Scenario: digest stored at final write
- **WHEN** a final state version completes
- **THEN** the final version carries a root-outputs digest and no secret value leaves the server.

#### Scenario: secret-only change keeps digest
- **WHEN** only a secret output value changes
- **THEN** the digest stays equal and no secret value appears in any API answer, log or notification.

#### Scenario: both tools canonicalize
- **WHEN** the same root outputs arrive in either tool shape
- **THEN** equal root outputs give equal digests for Pulumi and OpenTofu shapes.

### Requirement: outputs-changed gate

When the source applies, the server SHALL start the plan of a follower with when outputs-changed only when the digest differs from the digest of the immediate previous final version of the same source.

#### Scenario: unchanged outputs start nothing
- **WHEN** a source applies with no change to root outputs and a follower sets when outputs-changed
- **THEN** the server starts no plan and records skipped with reason outputs-unchanged.

#### Scenario: changed output starts followers
- **WHEN** a source applies with a changed root output
- **THEN** the server starts one plan for each follower with when outputs-changed.

#### Scenario: missing prior digest starts
- **WHEN** the source has no prior final digest
- **THEN** the server treats the outputs as changed and starts one plan for each follower.

### Requirement: skipped outcome vocabulary

When the server skips a follower for unchanged outputs, the server SHALL record skipped with reason outputs-unchanged.

#### Scenario: skipped outcome shape
- **WHEN** the server skips a follower for unchanged outputs
- **THEN** the outcome names the reason outputs-unchanged with the new and prior source version IDs.
