## ADDED Requirements

### Requirement: The setting access.installationGrantsReachNamespaces

The settings API SHALL list the key `access.installationGrantsReachNamespaces` at the installation level, with the kind boolean and the default `true`. A value other than `true` or `false` SHALL be refused with 400. A change SHALL write one audit entry with the old and the new value.

#### Scenario: Default in the list
- **WHEN** a holder of `server:read-settings` lists the settings of an installation with no value
- **THEN** the entry shows the value `true` and the source `default`

#### Scenario: Change to false
- **WHEN** a holder of `server:manage-settings` patches the value to `false` with an `Idempotency-Key`
- **THEN** the answer shows `false` with the source `database`, and the audit log has one entry with both values

#### Scenario: Value that does not parse
- **WHEN** a patch sets the value to `maybe`
- **THEN** the answer is 400 and the value does not change

### Requirement: The setting is protected

A change to `true` SHALL need a fresh step and SHALL count as a change that reduces security. The flag, the variable or the file key SHALL lock the value, and a PATCH of a locked key SHALL answer 409. A restore SHALL keep the live value.

#### Scenario: Change to true needs a fresh step
- **WHEN** a session with no fresh step patches the value from `false` to `true`
- **THEN** the answer is 403 `mfa.fresh-step-required`, and the value stays `false`

#### Scenario: Static lock
- **WHEN** the file sets `access.installationGrantsReachNamespaces: false`
- **THEN** the entry shows `locked` with the file as the layer, and a PATCH answers 409

#### Scenario: Restore keeps the value
- **WHEN** a restore reads a backup that holds the value `true` while the live value is `false`
- **THEN** the live value stays `false`
