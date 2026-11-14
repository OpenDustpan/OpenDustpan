## ADDED Requirements

### Requirement: Store path sets

The server SHALL store a path set with an identifier and 1 to 64 glob patterns at one scope.

#### Scenario: Save and read a namespace set
- **WHEN** a namespace administrator saves a path set net with modules/net/**
- **THEN** the read returns the ID and the same patterns, and the audit log holds one create entry

#### Scenario: Save races scope delete
- **WHEN** the scope is deleted during a save of a path set
- **THEN** the save fails and stores nothing

### Requirement: Reject invalid path sets

If a saved path set holds zero patterns, more than 64 patterns, a bad identifier, or a reused wider scope identifier, then the server SHALL refuse the save with 400 that names the bad identifier or pattern.

#### Scenario: Zero patterns refused
- **WHEN** a save holds zero patterns, more than 64 patterns, a bad ID, or a reused wider scope ID
- **THEN** the save is refused with 400 that names the bad identifier or pattern and no set is stored

### Requirement: Refuse stale path set write

When a save carries a stale If-Match version, the server SHALL refuse the write with 412.

#### Scenario: Stale version refused
- **WHEN** a save carries a stale If-Match version
- **THEN** the save is refused with 412 and the stored set keeps the newer version

### Requirement: Deny writes without a grant

If a principal without a write grant on the scope saves a path set, then the server SHALL refuse the write with 403.

#### Scenario: Write with no grant
- **WHEN** a principal without a write grant on the scope saves a path set
- **THEN** the write gives 403 and the audit log holds one denial entry

### Requirement: Deny reads without a grant

If a principal without a read grant on the scope reads or lists a path set, then the server SHALL refuse the read with 403.

#### Scenario: Read with no grant
- **WHEN** a principal without a read grant on the scope reads or lists a path set
- **THEN** the read gives 403 and the audit log holds one denial entry

### Requirement: Read sets by stable ID

The server SHALL resolve each stored reference to a path set by its stable identifier.

#### Scenario: Read back after remake
- **WHEN** a set is removed and made again under the same name
- **THEN** gates that name the stable ID resolve to the new set, a narrower scope cannot reuse the removed wider ID, and the old name label does not redirect

### Requirement: Compute touches

When a change order holds a changed path list, the server SHALL set paths.touches for each in-scope path set true where one changed path matches one pattern.

#### Scenario: Touched network path
- **WHEN** a change edits modules/net/a.tf with sets net as modules/net/** and docs as docs/**
- **THEN** paths.touches net is true and paths.touches docs is false

#### Scenario: Empty path list touches nothing
- **WHEN** a change holds an empty changed path list
- **THEN** paths.touches for each set is false

### Requirement: Compute only

When a change order holds a changed path list, the server SHALL set paths.only for each in-scope path set true only where every changed path matches the set.

#### Scenario: Docs only change
- **WHEN** a change edits only paths under docs/** with sets docs as docs/** and net as modules/net/**
- **THEN** paths.only docs is true and paths.only net is false

#### Scenario: Empty path list is not only
- **WHEN** a change holds an empty changed path list
- **THEN** paths.only for each set is false

### Requirement: Missing path list

If no changed path list exists, then the server SHALL return typed missing path facts with available false.

#### Scenario: CLI write with no paths
- **WHEN** a change has no changed path list, as from a CLI write
- **THEN** the path facts carry available false and a built-in template that needs them refuses

#### Scenario: Deleted set version
- **WHEN** a referenced path set version is absent at evaluation because the set was deleted or changed
- **THEN** the path facts for that set carry available false and a built-in template that needs them refuses

### Requirement: Truncate large path lists

When the changed paths would push the fact document above 256 KiB, the server SHALL empty the largest sets of strings largest first until the document fits.

#### Scenario: Truncated change paths
- **WHEN** the changed paths would push the fact document above 256 KiB
- **THEN** the document keeps each count and adds each emptied path to document.truncated

### Requirement: Treat truncated path facts as missing

If change.paths was truncated, then the server SHALL return path facts with available false.

#### Scenario: Truncated tail holds the only match
- **WHEN** change.paths was truncated before fact computation
- **THEN** the path facts carry available false and a built-in template that needs them refuses instead of reporting touches false and only true

### Requirement: Audit path set writes

The server SHALL write an audit entry for each write of a path set.

#### Scenario: Delete a path set
- **WHEN** a namespace administrator deletes a path set
- **THEN** the audit log holds one delete entry with the actor, the scope, and the ID
