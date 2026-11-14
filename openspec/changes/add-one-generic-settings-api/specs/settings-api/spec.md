## ADDED Requirements

### Requirement: Read effective settings

When a principal with the read action reads the settings of a scope, the server SHALL answer each declared setting with its effective value, its source layer, its locked flag and the resource revision.

#### Scenario: Read installation settings
- **WHEN** a principal reads the installation settings
- **THEN** each key answers with value, source, locked flag and the revision

#### Scenario: Read namespace settings
- **WHEN** a namespace administrator reads the settings of one namespace
- **THEN** the same fields answer for that namespace only

#### Scenario: Locked key shown
- **WHEN** the static layer locks a key
- **THEN** the read of each scope shows locked true and source static for that key

#### Scenario: Refuse without action
- **WHEN** a principal with no grant reads the namespace settings
- **THEN** the server answers 403 and shows no key

#### Scenario: Reload moves revision
- **WHEN** a reload changes an effective value or a source
- **THEN** the revision moves and an audit entry names the old and new source and value

### Requirement: Atomic patch

When a principal patches a settings resource, the server SHALL apply all database values in one transaction or none.

#### Scenario: Two valid keys
- **WHEN** a principal patches two valid unlocked keys
- **THEN** both change, the revision moves once and each change writes an audit entry with old and new values

#### Scenario: One invalid key
- **WHEN** the second key of a patch is invalid
- **THEN** no key changes and the revision does not move

#### Scenario: Null value
- **WHEN** a patch sets a key to null
- **THEN** the database value is deleted and the effective value is the default

#### Scenario: Audit write fails
- **WHEN** the audit write of a patch fails
- **THEN** the whole patch fails and no key and no revision changes

### Requirement: Locked key

If a patch writes a key that the static layer locks, then the server SHALL refuse the whole patch and name the lock.

#### Scenario: Refuse locked key
- **WHEN** a patch writes a locked key
- **THEN** the server answers 409 that names the lock and changes nothing

### Requirement: Unknown key

If a patch names an undeclared key, then the server SHALL refuse the whole patch with the code settings.unknown-key.

#### Scenario: Refuse unknown key
- **WHEN** a patch names an undeclared key
- **THEN** the server answers 422 that names the key and changes nothing

### Requirement: File-only key

If a patch writes a sensitive key that only an environment variable or a file can set, then the server SHALL refuse the whole patch with a 409.

#### Scenario: SMTP password
- **WHEN** a patch writes the SMTP password
- **THEN** the server answers 409 and changes nothing

### Requirement: Fresh step

When a patch reduces security, the server SHALL require a fresh authentication step before any write.

#### Scenario: Refuse weaker change
- **WHEN** a patch reduces security with no fresh step
- **THEN** the server answers 403 with code mfa.fresh-step-required and changes nothing

#### Scenario: Accept weaker change
- **WHEN** the same patch comes with a fresh step
- **THEN** the change applies and the audit entry is written

#### Scenario: Mixed patch
- **WHEN** a patch holds one weaker key and one stronger key with no fresh step
- **THEN** the server answers 403 and neither key changes

### Requirement: Stale revision

If a patch carries a base revision that is not the current revision, then the server SHALL refuse the patch with a 412.

#### Scenario: Refuse stale patch
- **WHEN** a patch carries an old revision
- **THEN** the server answers 412 with the current revision and changes nothing

#### Scenario: Patch during reload
- **WHEN** a reload moves the revision after the client read it and the client patches with the old revision
- **THEN** the server answers 412 and changes nothing

### Requirement: Idempotency key required

If a patch has no Idempotency-Key header, then the server SHALL refuse the patch.

#### Scenario: Missing key
- **WHEN** a patch of any key comes with no Idempotency-Key header
- **THEN** the server refuses the request and changes nothing

### Requirement: Idempotent retry

When a patch repeats an Idempotency-Key with the same body, the server SHALL answer the stored answer and write no second audit entry.

#### Scenario: Replay
- **WHEN** a client repeats the same key and body
- **THEN** the same answer returns and the revision does not move again

#### Scenario: Changed body
- **WHEN** a client repeats the key with another body
- **THEN** the server refuses the request and changes nothing

#### Scenario: Replay during first request
- **WHEN** a client repeats the key and body while the first request runs
- **THEN** the retry waits and returns the stored answer with one audit entry in total

### Requirement: Audit trail

When a database value changes, the server SHALL write one audit entry for each changed key with the old and the new value.

#### Scenario: Audit entry
- **WHEN** a patch changes one key
- **THEN** one audit entry names the key, the old value, the new value and the principal
