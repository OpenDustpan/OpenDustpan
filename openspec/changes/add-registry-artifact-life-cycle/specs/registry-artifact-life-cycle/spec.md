## ADDED Requirements

### Requirement: retireVersion

When a publisher retires a published version through retireVersion, the server SHALL set its state to retired, hide it from new resolution, keep it for exact pinned requests, and write one audit entry plus one version event in the same transaction.

#### Scenario: Pinned retired provider version installs
- **WHEN** The tool requests a retired provider version with an exact pinned version
- **THEN** The server serves the archive and the version read names the retired state.

#### Scenario: Pinned retired module version installs
- **WHEN** The tool requests a retired module version with an exact pinned version
- **THEN** The server serves the module files and the version read names the retired state.

#### Scenario: retireVersion writes audit and event
- **WHEN** A publisher retires a provider or module version with a reason of at most 500 characters
- **THEN** The audit log holds one entry and one version event that name the actor, the stable version ID, the reason, and the new state.

#### Scenario: Empty reason refused
- **WHEN** A publisher sends a retire with an empty reason
- **THEN** The server refuses the change without state change.

#### Scenario: Reason of 501 refused
- **WHEN** A publisher sends a reason of 501 characters
- **THEN** The server refuses the change without state change.

#### Scenario: Un-retire restores resolution
- **WHEN** A publisher moves a retired version to published
- **THEN** The server sets the state to published and writes one audit entry plus one version event.

### Requirement: revokeVersion

When a publisher revokes a version through revokeVersion, the server SHALL set its state to revoked and refuse each download and resolution of that version.

#### Scenario: Revoked provider version refused
- **WHEN** The tool requests a revoked provider version by pin, range, or latest
- **THEN** The server answers 404 with error code registry.not-found and a message that names the revoked state.

#### Scenario: Revoked module version refused
- **WHEN** The tool requests a revoked module version
- **THEN** The server answers 404 with error code registry.not-found and a message that names the revoked state.

#### Scenario: revokeVersion writes audit and event
- **WHEN** A publisher revokes a published or retired version
- **THEN** The audit log holds one entry and one version event that name the actor, the stable version ID, the reason, and the new state.

#### Scenario: Revoked mirror and sums refused
- **WHEN** The tool reads the network mirror for a revoked version
- **THEN** The server omits the version from the mirror index and refuses its archive, signed URL, and SHA256SUMS entry.

### Requirement: retired-hidden-from-resolution

While a registry version is retired, the server SHALL hide it from each unpinned resolution and latest selection.

#### Scenario: Retired hidden from unpinned resolution
- **WHEN** The tool requests a version range or latest that only a retired version could fill
- **THEN** The server omits the retired version from the candidate list.

#### Scenario: Retired shown in version read with state
- **WHEN** A reader lists versions or reads one retired version
- **THEN** The list and the version read show the retired state and the reason.

#### Scenario: Retired hidden from mirror index
- **WHEN** The tool reads the mirror index without an exact pin
- **THEN** The server omits the retired version from the mirror index for unpinned resolution.

### Requirement: life-cycle-state-change

When a publisher sends a state change with a state outside the allowlist of published, retired, and revoked, the server SHALL reject it without change.

#### Scenario: Illegal move refused
- **WHEN** A publisher requests a move outside published to retired, published to revoked, retired to revoked, or retired to published
- **THEN** The server answers 409 with the NEW error code registry.lifecycle-illegal and changes nothing.

#### Scenario: Range pin of retired refused
- **WHEN** The tool requests a retired version with a version range
- **THEN** The server refuses the request as unpinned resolution.

#### Scenario: Move out of revoked refused
- **WHEN** A publisher requests any move from revoked
- **THEN** The server answers 409 with the NEW error code registry.lifecycle-illegal and keeps the revoked state.

#### Scenario: Same state refused
- **WHEN** A publisher sends the current state as the new state
- **THEN** The server answers 409 with the NEW error code registry.lifecycle-illegal and changes nothing.

#### Scenario: Missing version not found
- **WHEN** A publisher sends a state change for a version ID that has no row
- **THEN** The server answers 404 with error code registry.not-found.

#### Scenario: Unauthorized caller denied
- **WHEN** A caller without the NEW action registry:manage-versions on the namespace sends a state change
- **THEN** The server denies the change without state change.

#### Scenario: Concurrent change loses cleanly
- **WHEN** Two publishers send a state change for one version with the same old state
- **THEN** The server commits the first change and answers 409 with the NEW error code registry.lifecycle-illegal for the second.
