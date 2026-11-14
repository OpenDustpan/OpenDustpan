## ADDED Requirements

### Requirement: root-outputs digest

The server SHALL store one root-outputs digest with each apply-counting final state version.

#### Scenario: digest stored at final write
- **WHEN** an apply-counting final state version completes
- **THEN** the final version carries a root-outputs digest and no secret value leaves the server.

#### Scenario: secret-only change keeps digest
- **WHEN** only a secret output plain value changes
- **THEN** the digest stays equal and no secret value appears in any API answer, log or notification.

#### Scenario: secret mark flip changes digest
- **WHEN** an output flips between secret and plain with the same plain value
- **THEN** the digest differs and followers with when outputs-changed start.

#### Scenario: pulumi shape canonicalizes
- **WHEN** the same root outputs arrive in Pulumi shape
- **THEN** equal root outputs give equal digests.

#### Scenario: opentofu shape canonicalizes
- **WHEN** the same root outputs arrive in OpenTofu shape
- **THEN** equal root outputs give equal digests.

### Requirement: outputs-changed gate

When the source applies, the server SHALL start the plan of a follower with when outputs-changed only when the digest differs from the digest of the immediate previous apply-counting final version of the same source.

#### Scenario: unchanged outputs start nothing
- **WHEN** a source applies with no change to root outputs and a follower sets when outputs-changed
- **THEN** the server starts no plan and records skipped with reason outputs-unchanged.

#### Scenario: changed output starts followers
- **WHEN** a source applies with a changed root output
- **THEN** the server starts one plan for each follower with when outputs-changed.

#### Scenario: missing prior digest starts
- **WHEN** the source has no prior apply-counting final digest
- **THEN** the server treats the outputs as changed and starts one plan for each follower.

#### Scenario: non-apply version keeps prior
- **WHEN** a refresh, preview or destroy version completes
- **THEN** the server keeps the prior apply-counting digest and compares against it at the next apply.

#### Scenario: mixed when values
- **WHEN** a source applies with unchanged outputs and followers use mixed when values
- **THEN** the server starts each follower with when applied and skips each follower with when outputs-changed.

#### Scenario: retry replays same pair
- **WHEN** the dispatch retries or replays after a restart
- **THEN** the server starts at most 1 plan per follower for that source version.

### Requirement: skipped outcome vocabulary

When the server skips a follower for unchanged outputs, the server SHALL record skipped with reason outputs-unchanged.

#### Scenario: skipped outcome shape
- **WHEN** the server skips a follower for unchanged outputs
- **THEN** the outcome names the reason outputs-unchanged with the new and prior source version IDs.

### Requirement: when value

When a person saves a follows trigger, the server SHALL accept only when applied and when outputs-changed.

#### Scenario: unknown when refused
- **WHEN** a person saves a follows trigger with an unknown when value
- **THEN** the server refuses the save with code trigger.invalid.

#### Scenario: default stays applied
- **WHEN** a person saves a follows trigger with no when value
- **THEN** the stored trigger reads back when applied.

#### Scenario: change reads back
- **WHEN** a person changes when from applied to outputs-changed
- **THEN** the stored trigger reads back when outputs-changed.
