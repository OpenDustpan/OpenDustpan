## ADDED Requirements

### Requirement: Unit restore audit entry

When a person restores an earlier state of a stack or workspace by calling restoreUnitState or FinalizeStateVersion, the server SHALL write one audit entry in the same transaction as the restored state.

#### Scenario: Stack restore writes entry
- **WHEN** A person restores a Pulumi stack to an earlier checkpoint by calling restoreUnitState.
- **THEN** The audit log holds one entry with actor, credential kind, stack reference, both versions, the reason when given, and the decision record id or null with reason no-gate-ran.

#### Scenario: Workspace restore writes entry
- **WHEN** A person restores an OpenTofu workspace to an earlier state version by calling FinalizeStateVersion.
- **THEN** The audit log holds one entry with actor, credential kind, workspace reference, both versions, the reason when given, and the decision record id or null with reason no-gate-ran.

#### Scenario: Audit failure rolls back
- **WHEN** The audit write of restoreUnitState or FinalizeStateVersion fails.
- **THEN** The state row is unchanged and the server returns an error.

#### Scenario: Long reason is refused
- **WHEN** A person sends a reason longer than 500 characters.
- **THEN** The server refuses the restore with audit.reason-invalid and writes no state.

#### Scenario: Concurrent restores serialize
- **WHEN** Two persons restore the same unit at once through restoreUnitState.
- **THEN** Each accepted restore has one entry with its own true versions.

#### Scenario: Duplicate restore writes entry
- **WHEN** A person restores the version that is already live through restoreUnitState or FinalizeStateVersion.
- **THEN** The audit log holds one entry with equal versions and outcome duplicate and no new state version.
