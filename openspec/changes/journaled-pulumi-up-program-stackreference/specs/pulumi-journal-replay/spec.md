## ADDED Requirements

### Requirement: Journaled StackReference up

When a journaled Pulumi up runs a program with a StackReference, the server SHALL store no snapshot mismatch error event after the summary event.

#### Scenario: Journaled up with StackReference
- **WHEN** a journaled up runs the YAML program of the issue
- **THEN** the stored update events hold no error event with snapshot mismatch text after the summary

#### Scenario: Additional secret outputs
- **WHEN** a journaled up runs a StackReference with additionalSecretOutputs outputs
- **THEN** the stored checkpoint keeps the StackReference outputs and secret marks of the engine snapshot

#### Scenario: Preview parity
- **WHEN** a preview runs the same program
- **THEN** the stored events hold no snapshot mismatch error event

#### Scenario: Non-journaled parity
- **WHEN** the same up runs with journaling disabled
- **THEN** the stored checkpoint matches the journaled checkpoint field for field

### Requirement: Secret values stay sealed

While a journaled Pulumi up replays secret outputs, the server SHALL keep each secret value sealed in stored state and out of stored events.

#### Scenario: Secret config value
- **WHEN** a journaled up runs the issue program with a secret config value
- **THEN** the stored state holds the secret sealed and the stored events hold no plain-text secret

#### Scenario: Other secret outputs
- **WHEN** a journaled up runs another program with secret outputs
- **THEN** the stored state holds each secret output sealed after the replay fix

### Requirement: Unknown shape refused

If a journal batch holds an entry that the replayer does not decode, then the server SHALL refuse the batch with journal.batch-invalid.

#### Scenario: Undecodable entry
- **WHEN** a journal batch holds an undecodable entry
- **THEN** the server answers journal.batch-invalid and the stored state of the stack stays unchanged
