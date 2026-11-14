## ADDED Requirements

### Requirement: Store path sets

The server SHALL store a path set, made by NewPathSet, with an identifier and 1 to 64 glob patterns at one scope.

#### Scenario: Save and read
- **WHEN** a namespace administrator saves a path set net with modules/net/**
- **THEN** the read returns the ID and the patterns and the audit log holds one create entry

### Requirement: Reject invalid path sets

If a saved path set holds zero patterns, more than 64 patterns, a bad identifier or a reused wider scope identifier, then the server SHALL refuse the save with 400.

#### Scenario: Zero patterns
- **WHEN** a save holds zero patterns
- **THEN** the save is refused with 400 that names the bad value and nothing is stored

### Requirement: Refuse a stale write

When a save carries a stale If-Match version, the server SHALL refuse the write with 412.

#### Scenario: Stale version
- **WHEN** a save carries a stale If-Match version
- **THEN** the save is refused with 412 and the stored set is unchanged

### Requirement: Deny without a grant

If a principal without a grant on the scope reads or writes a path set, then the server SHALL refuse the request with 403.

#### Scenario: No grant
- **WHEN** a principal without a grant saves a path set
- **THEN** the write gives 403 and the audit log holds one denial entry

### Requirement: Compute touches

When a change order holds a changed path list, the server SHALL set paths.touches for each in-scope path set true where one changed path matches one pattern.

#### Scenario: Network path
- **WHEN** a change edits modules/net/a.tf with set net as modules/net/**
- **THEN** paths.touches net is true

### Requirement: Compute only

When a change order holds a changed path list, the server SHALL set paths.only for each in-scope path set true only where every changed path matches the set.

#### Scenario: Docs only
- **WHEN** a change edits only paths under docs/** with set docs as docs/**
- **THEN** paths.only docs is true

#### Scenario: Mixed paths
- **WHEN** a change edits one path under docs/** and modules/net/a.tf with set docs as docs/**
- **THEN** paths.only docs is false

### Requirement: Missing path list

If no changed path list exists, then the server SHALL return path facts with available false.

#### Scenario: CLI write
- **WHEN** a change has no changed path list
- **THEN** the path facts carry available false and a built-in template that needs them refuses

### Requirement: Truncated paths

If change.paths was truncated, then the server SHALL return path facts with available false.

#### Scenario: Truncated list
- **WHEN** change.paths was truncated before fact computation
- **THEN** the path facts carry available false
