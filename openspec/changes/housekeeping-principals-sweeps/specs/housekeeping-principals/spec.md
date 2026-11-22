## ADDED Requirements

### Requirement: Each sweep is attributed

Each housekeeping run SHALL authorize its action through the stored grants of its principal, and each audit entry of the run SHALL name that principal.

#### Scenario: Artifact sweep
- **WHEN** the artifact sweep deletes an expired artifact
- **THEN** the audit entry names the housekeeping principal of the artifact sweep, never `@server`

### Requirement: A revoke stops only its purpose

When an operator revokes the grant of one housekeeping principal, the server SHALL refuse only the work of that purpose, write a denial audit entry, and run the other purposes as before.

#### Scenario: Revoke the artifact sweep
- **WHEN** the grant of the artifact sweep principal is revoked
- **THEN** the next artifact sweep changes nothing and writes a denial, and the retention of history runs as before

### Requirement: The reconcile of jobs holds no start action

The reconcile of jobs SHALL authorize each change with the action `job:reconcile` on the unit of the job, and its principal SHALL hold no start action and no `job:cancel`.

#### Scenario: Reconcile principal asks for a start
- **WHEN** the principal of the reconcile asks the authorizer for `unit:run-plan` on a unit
- **THEN** the server denies the action

#### Scenario: Revoked reconcile grant
- **WHEN** the grant of `job:reconcile` of the reconcile principal is revoked
- **THEN** the next reconcile changes no job and writes a denial that names `job:reconcile`

### Requirement: Namespace purposes do not need the installation reach

The housekeeping purposes that act inside a namespace SHALL work through the stored grants on the namespace, also when the installation grant does not reach namespaces.

#### Scenario: Reach setting is false
- **WHEN** `access.installationGrantsReachNamespaces` is `false` and the retention of history runs on a namespace
- **THEN** the redaction is authorized by the grant on that namespace
