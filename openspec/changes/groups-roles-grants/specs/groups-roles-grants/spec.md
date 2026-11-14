## ADDED Requirements

### Requirement: Collection writer scope

When a principal with a writer grant on one collection writes a unit of another collection, the server SHALL answer 403.

#### Scenario: Write inside granted collection
- **WHEN** a principal with writer on collection A runs pulumi up on a unit of collection A
- **THEN** the server applies the update and writes an audit entry

#### Scenario: Write outside granted collection
- **WHEN** the same principal runs pulumi up on a unit of collection B
- **THEN** the server answers 403 with code unit.forbidden and changes nothing

#### Scenario: Viewer reads other collection
- **WHEN** a principal with viewer on collection B runs stack export on a unit of collection B
- **THEN** the server answers 200 with sealed secrets masked

#### Scenario: Installation writer reaches collection by default
- **WHEN** a principal with writer on the installation writes a unit of a collection and the reach setting is true
- **THEN** the server applies the write

### Requirement: Principal home

When the installation setting identity.principalHome makes a principal belong to one namespace, the server SHALL refuse each grant to that principal outside its namespace.

#### Scenario: Namespace principal writes home unit
- **WHEN** a principal of namespace acme with writer on a collection of acme writes a unit of that collection
- **THEN** the server applies the update

#### Scenario: Namespace principal refused outside home
- **WHEN** an administrator tries to grant that principal a role on another namespace
- **THEN** the server answers 403 and stores no grant

### Requirement: Installation grant reach

Where the installation setting access.installationGrantsReachNamespaces is false, the server SHALL give no collection action from a grant on the installation.

#### Scenario: Installation grant reaches collection
- **WHEN** the setting is true and a principal with writer on the installation writes a unit of the collection
- **THEN** the server applies the write

#### Scenario: Installation grant does not reach
- **WHEN** the setting is false and the same principal writes the same unit
- **THEN** the server answers 403 with code unit.forbidden

### Requirement: Atomic grant check

When the server writes a unit, the server SHALL read the grant and commit the write in one fenced transaction.

#### Scenario: Revoke between check and write
- **WHEN** the grant ends after the read of the grant and before the commit of the unit write
- **THEN** the server rolls back the write and answers 403 with code grant.revoked

#### Scenario: Grant ends after commit during long run
- **WHEN** the grant ends after the commit while the same pulumi up or tofu apply still runs
- **THEN** the committed write stays and the server refuses each later request of that run with 403

### Requirement: Viewer read and write

When a principal with a viewer grant starts a write update, the server SHALL answer 403.

#### Scenario: Viewer reads state
- **WHEN** a principal with viewer runs pulumi stack export or tofu state pull
- **THEN** the server answers 200 with sealed secrets masked

#### Scenario: Viewer writes
- **WHEN** the same principal runs pulumi up or tofu apply
- **THEN** the server answers 403 with code unit.forbidden and changes nothing

### Requirement: Group holder reach

When a group membership ends, the server SHALL refuse the next request that needs that group grant.

#### Scenario: Member writes through group grant
- **WHEN** a member of a group with writer on a collection runs pulumi up on a unit of that collection
- **THEN** the server applies the update

#### Scenario: Membership ends by administrator
- **WHEN** the membership ends and the same principal retries the write
- **THEN** the server answers 403 on the next request and changes nothing

#### Scenario: Membership ends by directory sync
- **WHEN** a directory sync ends the membership and the same principal retries the write
- **THEN** the server answers 403 on the next request and changes nothing

#### Scenario: Member in another namespace home
- **WHEN** a person acts with the principal of namespace B through a group grant of namespace A
- **THEN** the server refuses the write with 403 because the other principal holds no grant

### Requirement: Granter reach

When a granter lacks reach over the target object or role, the server SHALL refuse the grant through the shared reach function.

#### Scenario: Grant above own reach refused
- **WHEN** a writer tries to grant admin on an object outside its reach
- **THEN** the server answers 403 and stores no grant

#### Scenario: New member above adder reach refused
- **WHEN** an adder without reach over a group grant tries to add a member to that group
- **THEN** the server answers 403 and adds no member

### Requirement: Revocation timing

When a grant ends, the server SHALL refuse the next request that needs that grant with 403.

#### Scenario: Use after revocation
- **WHEN** the grant ends and the same token sends the next CLI request
- **THEN** the server answers 403 with code grant.revoked and changes nothing

#### Scenario: Lock held by revoked principal
- **WHEN** the grant ends mid-run while the principal holds a unit lock
- **THEN** the server keeps the lock and refuses the next write of that principal with 403

#### Scenario: Grant restored before next request
- **WHEN** an administrator grants the role again before the caller retries
- **THEN** the server allows the next request when the fenced transaction time is at or after the re-grant

### Requirement: Refusal codes

When the server refuses a grant check, the server SHALL answer the code of the single refusal list.

#### Scenario: Wrong collection write
- **WHEN** a writer writes a unit outside the granted collection
- **THEN** the server answers 403 with code unit.forbidden

#### Scenario: Viewer write
- **WHEN** a viewer starts pulumi up or tofu apply
- **THEN** the server answers 403 with code unit.forbidden

#### Scenario: Secret read without secret action
- **WHEN** a viewer or planner reads a secret value
- **THEN** the server answers 403 with code secret.forbidden

#### Scenario: Revoked grant use
- **WHEN** a caller uses a token whose only grant ended
- **THEN** the server answers 403 with code grant.revoked

#### Scenario: Export without action
- **WHEN** a caller without the export action asks for the export
- **THEN** the server answers 403 with code access.export-forbidden

### Requirement: Access-review snapshot export

Where a caller asks for the snapshot access-review export of a scope, the server SHALL answer each live grant with holder, role, object, creator, time, and last use.

#### Scenario: Export snapshot
- **WHEN** an allowed caller asks for the snapshot export of a namespace
- **THEN** the server answers each live grant with holder stable ID and name, role, object stable ID and name, creator, time, and last sign-in or token use with Never

#### Scenario: Export lists namespace principals
- **WHEN** a namespace holds grants on its own service principals
- **THEN** the export includes each grant held by a principal of that namespace

#### Scenario: Export lists token managers
- **WHEN** an allowed caller asks for the snapshot export
- **THEN** the export names the principals and groups that hold manage-tokens on each service principal

#### Scenario: Collection caller sees namespace grants
- **WHEN** a caller whose own grant reaches only one collection asks for the namespace export with the export action
- **THEN** the server answers the namespace snapshot with each grant in scope

### Requirement: Access-review history export

Where a caller asks for the history access-review export between two timestamps, the server SHALL answer each grant live in that range.

#### Scenario: Export history
- **WHEN** an allowed caller asks for the history export between two timestamps
- **THEN** the server answers each grant live in the range with holder, role, object, creator, time, last use, and end time when ended

#### Scenario: Bad history range
- **WHEN** the caller gives an end time before the start time
- **THEN** the server answers 400 and changes nothing

### Requirement: Export authorization and audit

When a caller asks for the access-review export, the server SHALL check the read-access action of the scope.

#### Scenario: Namespace caller without export action
- **WHEN** a caller without namespace:read-access asks for the namespace export
- **THEN** the server answers 403 with code access.export-forbidden and writes a refusal audit entry

#### Scenario: Installation caller without export action
- **WHEN** a caller without installation:read-access asks for the installation export
- **THEN** the server answers 403 with code access.export-forbidden and writes a refusal audit entry

#### Scenario: Allowed export
- **WHEN** a caller with the read-access action of the scope asks for the export
- **THEN** the server answers the export and writes an export audit entry

### Requirement: Refusal audit

When the server refuses a CLI request for a missing grant, the server SHALL write a refusal audit entry.

#### Scenario: Refused write
- **WHEN** the server answers 403 to a CLI write outside the granted collection
- **THEN** the server writes an audit entry with principal, action, object and error code

#### Scenario: Refused read and export
- **WHEN** the server answers 403 to a state read or to an export without the export action
- **THEN** the server writes an audit entry with principal, action, object and error code

### Requirement: Non-admin baseline

When a non-admin access token runs the milestone 1 to 3 flows, the server SHALL enforce the same collection grants.

#### Scenario: Non-admin runs milestone flows
- **WHEN** a non-admin token with collection writer and viewer grants runs the milestone 1 to 3 Pulumi and OpenTofu reads and writes
- **THEN** the server applies allowed reads and writes and refuses the rest with 403 and audit entries

#### Scenario: Non-admin secret read
- **WHEN** a viewer reads a secret value
- **THEN** the server answers 403 with code secret.forbidden
