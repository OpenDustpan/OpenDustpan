## ADDED Requirements

### Requirement: Mask local display and upload bytes

When a local wrapper run prints a secret value of its profile, the wrapper SHALL replace the value with the masked value on display and in the upload bytes.

#### Scenario: Secret printed by local apply
- **WHEN** a local wrapper run prints a secret value of its profile
- **THEN** the display and the upload bytes hold the masked value and the plain text appears nowhere in them.

#### Scenario: Split write and stderr
- **WHEN** the secret value splits across write boundaries on stdout and on stderr
- **THEN** the display and the upload bytes hold only the masked value.

#### Scenario: Empty secret list
- **WHEN** the profile open holds no secret value
- **THEN** the run proceeds with an empty mask set.

### Requirement: Fail closed without mask set

If the profile open or the mask set build fails, then the wrapper SHALL start no child.

#### Scenario: Mask set cannot be built
- **WHEN** the profile open or the mask set build fails
- **THEN** the wrapper starts no child and makes no provider call.

#### Scenario: Value the masker cannot hold
- **WHEN** a secret value is too large for the masker
- **THEN** the wrapper starts no child and names the failure.

### Requirement: Flush before upload

When the child of a local wrapper run exits, the wrapper SHALL flush the masker before the upload.

#### Scenario: Child exits
- **WHEN** the child exits
- **THEN** the wrapper flushes the masker and hands only flushed bytes to the upload.

#### Scenario: Upload fails
- **WHEN** the log upload fails after the child exits
- **THEN** the wrapper reports the failure and keeps no unflushed plain text in memory after exit.

### Requirement: Never log credentials

When child output of a local wrapper run holds a credential of the run, the wrapper SHALL exclude the credential from display and from upload bytes.

#### Scenario: Credential in output
- **WHEN** child output holds a credential of the run
- **THEN** the display and the upload bytes hold no credential material.

#### Scenario: Credential copied into a secret value
- **WHEN** the wrapper builds its output from the child and the credential of the run
- **THEN** the credential appears in neither the display nor the upload bytes.
