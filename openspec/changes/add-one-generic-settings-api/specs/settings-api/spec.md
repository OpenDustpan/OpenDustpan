## ADDED Requirements

### Requirement: Read effective settings

When a principal with the read action reads the settings of a scope, the server SHALL answer each declared setting with its effective value, its source layer, its locked flag and the resource revision.

#### Scenario: Read installation settings
- **WHEN** a principal reads the installation settings
- **THEN** each key answers with value, source, locked flag and the revision

#### Scenario: Refuse without action
- **WHEN** a principal with no grant reads the namespace settings
- **THEN** the server answers 403 and shows no key

### Requirement: Atomic patch

When a principal patches a settings resource, the server SHALL apply all database values in one transaction or none.

#### Scenario: One invalid key
- **WHEN** the second key of a patch is invalid
- **THEN** no key changes and the revision does not move

### Requirement: Stale revision

If a patch carries a revision that is not the current revision, then the server SHALL refuse the patch with 412.

#### Scenario: Old revision
- **WHEN** a patch carries an old revision
- **THEN** the server answers 412 with code settings.revision-stale and changes nothing

### Requirement: Unknown key

If a patch names an undeclared key, then the server SHALL refuse the patch with 422.

#### Scenario: Unknown key
- **WHEN** a patch names a key that no setting declares
- **THEN** the server answers 422 with code settings.key-unknown and changes nothing

### Requirement: Static-only key

If a patch writes a sensitive key that only an environment variable or a file can set, then the server SHALL refuse the patch with 409.

#### Scenario: Sensitive key
- **WHEN** a patch writes the SMTP password
- **THEN** the server answers 409 with code settings.static-only and changes nothing

### Requirement: Locked key

If a patch writes a key that the static layer locks, then the server SHALL refuse the whole patch and name the lock.

#### Scenario: Refuse locked key
- **WHEN** a patch writes a locked key
- **THEN** the server answers 409 that names the lock

### Requirement: Fresh step

When a patch reduces security, the server SHALL require a fresh authentication step before any write.

#### Scenario: Refuse weaker change
- **WHEN** a patch reduces security with no fresh step
- **THEN** the server answers 403 with code mfa.fresh-step-required and changes nothing

#### Scenario: Access token
- **WHEN** a patch reduces security and the caller uses an access token
- **THEN** the server answers 403 with code mfa.fresh-step-required and changes nothing

### Requirement: Idempotency key required

If a patch has no Idempotency-Key header, then the server SHALL refuse the patch.

#### Scenario: Missing key
- **WHEN** a patch comes with no Idempotency-Key header
- **THEN** the server refuses the request and changes nothing

### Requirement: Idempotent replay

When a patch repeats an Idempotency-Key with the same body, the server SHALL answer the stored result and write no second change.

#### Scenario: Replay
- **WHEN** a client repeats a patch with the same key and body
- **THEN** the answer is the same and the revision moves once

#### Scenario: Key reuse
- **WHEN** a client repeats the key with another body
- **THEN** the server refuses the request and changes nothing

### Requirement: Audit trail

When a database value changes, the server SHALL write one audit entry for each changed key with the old and the new value.

#### Scenario: Audit entry
- **WHEN** a patch changes one key
- **THEN** one audit entry names the key, the old value, the new value and the principal

#### Scenario: Masked value
- **WHEN** a changed value is sensitive
- **THEN** the audit entry shows a mask in place of the value
