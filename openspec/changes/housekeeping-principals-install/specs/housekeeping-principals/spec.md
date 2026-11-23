## ADDED Requirements

### Requirement: One principal for each housekeeping purpose

The server SHALL make one built-in service principal for each housekeeping purpose at the install, and SHALL write its grants as stored grants with the install as the creator and the install time.

#### Scenario: Listed after the install
- **WHEN** an operator lists the principals of a new installation
- **THEN** each housekeeping purpose has one principal of kind `service` and source `builtin`, and each has stored grants with a creator and a time

### Requirement: Each namespace gets the grants of the housekeeping principals that act inside it

The server SHALL write, in the transaction of each namespace creation, one stored grant on the new namespace for each housekeeping role that acts inside a namespace, with the server as the creator and the creation time, and SHALL write an audit entry for each grant in the chain of the namespace.

#### Scenario: New namespace
- **WHEN** an operator creates a namespace
- **THEN** the namespace has one stored grant of each of the roles `housekeeping-agent-run-reaper`, `housekeeping-history-retention`, `housekeeping-issuer-rotation`, `housekeeping-job-reconcile` and `housekeeping-saved-view-cleanup`, each with a creator and a time

#### Scenario: Revoked grant stays revoked
- **WHEN** an operator revokes one of these grants and then creates another namespace
- **THEN** the revoked grant stays revoked and the new namespace has its own five grants

### Requirement: No role granted on the installation holds a namespace action

The server SHALL NOT give any role that a housekeeping principal holds on `@server` an action that acts inside a namespace, and the purpose `agent-reaper` SHALL hold the role `housekeeping-agent-reveal-sweep` on `@server` and the role `housekeeping-agent-run-reaper` on each namespace.

#### Scenario: Revoke of the namespace grant
- **WHEN** an operator revokes the grant of `housekeeping-agent-run-reaper` on a namespace
- **THEN** the action `namespace:manage-agent-runs` is denied for `housekeeping:agent-reaper` on that namespace, and `server:sweep-agent-reveals` stays allowed on `@server`

#### Scenario: Roles on the installation
- **WHEN** the roles that the install grants on `@server` are listed
- **THEN** none of them holds an action of a namespace area

### Requirement: Each sweep has its own narrow action

The server SHALL define one action for each of the sweeps of artifacts, blobs, checkpoints, name locks, temporary administrators and agent reveals, and the role of each purpose SHALL hold only the action of its sweep.

#### Scenario: Role of the artifact sweep
- **WHEN** an operator reads the role `housekeeping-artifact-sweep`
- **THEN** the role holds `server:sweep-artifacts` and no other action

### Requirement: The reconcile of jobs has its own action

The server SHALL define the action `job:reconcile`, and the role of the reconcile of jobs SHALL hold that action and no start action.

#### Scenario: Role of the reconcile
- **WHEN** an operator reads the role `housekeeping-job-reconcile`
- **THEN** the role holds `job:reconcile` and none of `unit:run-plan`, `unit:run-apply`, `unit:run-destroy`, `unit:write-back` and `job:cancel`

### Requirement: The principal list filters by source

The principal list SHALL keep only the principals of the source in the `source` query parameter, and SHALL count only those rows in its total and its cursor.

#### Scenario: Built-in principals
- **WHEN** an operator lists the principals with `kind=service&source=builtin`
- **THEN** the answer holds only the built-in principals, and the total counts only them

#### Scenario: Source that is not valid
- **WHEN** an operator lists the principals with a `source` that is not valid
- **THEN** the server answers 400

### Requirement: A restore keeps the housekeeping principals of the backup

A portable restore SHALL replace the housekeeping principals and grants of the new baseline with the principals and grants of the backup, so that a grant that was revoked before the backup is still revoked after the restore.

#### Scenario: Restore into a new installation
- **WHEN** an operator restores a portable backup into a new installation
- **THEN** each housekeeping purpose has one principal and the grants that the backup holds, and no row is twice in the installation

#### Scenario: Revoked grant
- **WHEN** an operator restores a backup in which a grant of a housekeeping principal was revoked
- **THEN** the grant is revoked after the restore
