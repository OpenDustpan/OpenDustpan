## ADDED Requirements

### Requirement: NegotiatedJournalVersion

The release build SHALL answer the start of a mutating Pulumi update with the highest journal version that the CLI offered and that the server supports, when the setting `disable-pulumi-journaling` is `false` and the version gate lets the CLI through.

#### Scenario: Verified CLI journals by default
- **WHEN** a Pulumi CLI from v3.250.0 to v3.268.0 starts an update with the default settings
- **THEN** the start answers a non-zero `journalVersion`, the server accepts its journal batches, and `complete` builds the checkpoint

#### Scenario: Version 2 for a CLI that offers it
- **WHEN** Pulumi CLI v3.268.0 offers journal version 2 at the start of an update
- **THEN** the start answers `journalVersion` 2

#### Scenario: CLI outside the gate
- **WHEN** Pulumi CLI v3.200.0 starts an update with the default settings
- **THEN** the start answers `journalVersion` 0 and the CLI saves checkpoints

### Requirement: JournalingOffSetting

When the setting `disable-pulumi-journaling` is `true`, the server SHALL answer `journalVersion` 0 to each new start.

#### Scenario: Operator turns journaling off
- **WHEN** the operator starts the server with `DUSTPAN_DISABLE_PULUMI_JOURNALING=true` and a verified Pulumi CLI starts an update
- **THEN** the start answers `journalVersion` 0 and the server receives no journal batch

### Requirement: PreviewsAnswerZero

The server SHALL answer `journalVersion` 0 to the start of a preview or a dry run, whatever the CLI offers.

#### Scenario: Preview with an offer
- **WHEN** a verified Pulumi CLI starts a preview update and offers journal version 2
- **THEN** the start answers `journalVersion` 0 and the update row stores journal version 0

### Requirement: SameStateInBothModes

For each Pulumi CLI version in the verified journal list, the same program SHALL give the same normalized `stack export` with journaling on and with journaling off.

#### Scenario: Both modes
- **WHEN** the e2e step runs the same `pulumi up` with journaling on and with journaling off for v3.250.0, v3.262.0 and v3.268.0
- **THEN** the two normalized exports are equal for each CLI

### Requirement: VerifiedRangeMatchesE2E

The oldest and the newest verified CLI version of the journal gate SHALL each be in the verified journal list that the e2e suite runs.

#### Scenario: Newest verified version moves without an e2e run
- **WHEN** the newest verified CLI version of the gate is set to a version that is not in the verified journal list of `e2e/tool.go`
- **THEN** the e2e matrix test fails and names the missing version

### Requirement: CLIVersionStoredAtStart

The server SHALL store the Pulumi CLI release of an update at start, and the manifest of a checkpoint that the server builds at complete SHALL name that release. The server SHALL store an empty value when the start offers no release or the `User-Agent` release is not three dot-separated numbers of at most 32 bytes.

#### Scenario: Verified CLI
- **WHEN** a Pulumi CLI v3.268.0 starts an update and the update completes
- **THEN** the update row stores `3.268.0` and the built manifest names version 3.268.0

#### Scenario: Hostile User-Agent
- **WHEN** a start carries a release of `3.268.0-rc1`, a 200-byte release or a release with a CR character
- **THEN** the update row stores an empty value
