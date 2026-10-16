# OpenDustpan

OpenDustpan is a self-hosted backend for infrastructure-as-code tools. It keeps
the state, history, locks and secrets of every tool behind one set of
namespaces, identities and storage.

## Language

### Shared across tools

**Tool**:
An infrastructure-as-code CLI that keeps its state in OpenDustpan through the remote-backend protocol it already speaks, for example the Pulumi CLI or OpenTofu.
_Avoid_: Client, integration, provider

**Tool name**:
The short name of a tool, as a label in the console, the API and the docs: "Pulumi" or "OpenTofu". In a claim of compatibility, or where the text means the program, write "the Pulumi CLI". Never write "Pulumi" alone where a reader can think of Pulumi Cloud or of the company. A unit reference and stored data hold the lowercase code of the tool instead: `pulumi` or `opentofu`.
_Avoid_: "Pulumi CLI" as a label, "Pulumi Cloud" for the Pulumi CLI, "Terraform" for OpenTofu, tool label

**Namespace**:
The unit of tenancy that owns units, access rules and secrets. Each tool calls it an organization.
_Avoid_: Organization (except when naming a tool's own protocol field), tenant, account, org

**Unit**:
One deployable instance of infrastructure with its own state, in the model of the tool that owns it. A Pulumi stack and an OpenTofu workspace are units.
_Avoid_: Environment, deployment, target

**Unit reference**:
The tool, the namespace and the tool's own name for a unit, written as `<tool>:<namespace>/<name>`, for example `pulumi:acme/web/prod`.
_Avoid_: Unit ID, path, address

**State**:
The record that a tool keeps of the resources it manages for one unit. OpenDustpan stores state and may read it. A read masks secret values (ADR 41).
_Avoid_: State file (except for OpenTofu), snapshot

**Inventory**:
The list of all units in a namespace across all tools, with their last change and status. Each tool keeps it current with its own writes.
_Avoid_: Catalog, registry, index

**Collection**:
A named set of units in one namespace, from any tool, that a team manages. Each unit is in exactly one collection, and collections do not nest (ADR 51). A collection can be the object of a grant.
_Avoid_: Group (that is a set of principals), project, folder, system

**Placement rule**:
A rule of a namespace that matches a Pulumi project and a stack-name pattern. It names the collection of a new stack that the Pulumi CLI makes (ADR 61).
_Avoid_: Routing rule, assignment rule

**Label**:
A key and a value on a unit, for views and filters. A label never grants access and never puts a unit into a collection (ADR 51).
_Avoid_: Tag (except when naming a tool's own field), attribute

**Unit lock**:
The tool-neutral flag in the inventory that shows who holds a unit for a change. The holder is a mutating Pulumi update that runs, or an OpenTofu workspace lock. It names the holder, the time the holder took it, and the tool's own word for the operation. A unit lock is not a status, so a unit can hold one in any status.
_Avoid_: Lease (for the inventory field), unit status

**Import**:
The act of moving the state of a unit from a source into OpenDustpan. It moves history and secrets where the tool can export them. The source stays unchanged. An import is not the Pulumi update of kind `import`. That update records one `pulumi stack import` command. An import is also not the Pulumi command `pulumi import`.
_Avoid_: Migration (it means a SQL schema migration), transfer, onboarding

**Source**:
The backend or product that holds the state of a unit before an import. Examples: a Pulumi service backend, an OpenTofu backend or HCP Terraform.
_Avoid_: Origin, legacy backend

**Installation**:
One OpenDustpan deployment: its SQL store, its blob store and its key-encryption key, with each server process that uses them. The installation API (`/api/server/...`) acts on the whole installation.
_Avoid_: Cluster, instance (one server process), deployment

**Instance**:
One server process of an installation. Each start makes a new instance ID, `ins-` and 20 characters, which the start log shows. The instance ID is the subject of the audit entries that the process writes on its own (ADR 63).
_Avoid_: Node, replica, host

**IP allowlist**:
A list of CIDRs on a namespace. A request that acts on the namespace from outside the list gets 403. The feature is off unless the operator turns it on.
_Avoid_: Whitelist

**Setting**:
One named value that changes how the server works, for example `log-level`. One declaration in `internal/cli/settings.go` gives its flag, its environment variable, its file key and its default (ADR 63).
_Avoid_: Option, parameter, config value

**Layer**:
A source of the value of a setting: `flag`, `env`, `file` or `default`, in that order of precedence (ADR 63 decision 1). The layer that wins gives the effective value.
_Avoid_: Level, tier

**Sensitive setting**:
A setting that can hold a credential: a secret or the database URL. A log line or an audit entry shows it only as `set` or `not set` (ADR 63 decision 6).
_Avoid_: Secret setting, private setting

**Client address**:
The address of the client that sent a request, as the server records it in the request log and in audit entries. Without trusted proxies it is the address of the TCP peer. With `--trusted-proxies` it is the address that a trusted proxy forwarded.
_Avoid_: Remote address, source IP (except for the audit field name `SourceIP` and the wire name `source`)

**Backup**:
The copy of the SQL data of a whole installation with the blob references that the data names. The SQL part is in the portable format. A restore accepts a backup of either dialect and of the same or an older schema version (ADR 42).
_Avoid_: Dump, snapshot

**Portable format**:
The dialect-neutral format of the SQL part of a backup: one compressed file of JSON rows for each table (ADR 42). It is the only backup format. It replaces the native format of ADR 30.
_Avoid_: Export format, dump format

**Restore**:
The installation API operation that replaces the SQL data of an installation with a backup (ADR 38). The server checks the backup before it changes live data, applies it in maintenance mode, and takes a backup of the live data first. It is not the restore of one older version of a unit (ADR 28). It is also not the restore of a deleted unit (ADR 47).
_Avoid_: Recovery, load, import

**Control table**:
A table that a backup leaves out and a restore keeps (ADR 72, decision 5). The live rows stay across the swap. The tables of durable operations are control tables. Their claim tokens only grow, and an older token from a backup would let a stale holder write.

**Deleted unit**:
A unit after a delete through its tool (ADR 47). It leaves every list and its name is free at once. OpenDustpan keeps its state, history, secrets and grants until a purge. The inventory API lists the deleted units of a namespace, and a restore of a deleted unit brings one back with its stable ID.
_Avoid_: Tombstone (except for the stored row), trashed unit, archived unit

**Retention period**:
The time that a deleted unit stays restorable, from the delete to its purge time (`purgeAfter`). `--soft-delete-retention` sets it. The default is 720 hours, and the minimum is one hour.
_Avoid_: Grace period, TTL

**Purge**:
The final delete of a deleted unit (ADR 47). It deletes the rows of the unit, its data key, its deleted inventory entry and its blobs, and ends the grants on it. It cannot be undone. An admin can purge a unit before its retention period ends.
_Avoid_: Hard delete, wipe

**Final delete**:
The same as a purge. Use "purge" in code and in the API, and "final delete" where a reader needs the plain meaning.
_Avoid_: Permanent delete

**Retention sweep**:
The server task that purges each deleted unit whose retention period ended. It runs one time each hour, the first time one hour after the server starts. `POST /api/server/deleted-units/sweep` runs it at once. It also finishes the blob purge records.
_Avoid_: Garbage collection, reaper

**Blob purge record**:
A row that a purge writes for a unit whose blobs may still exist. A save that started before the purge can write a blob after the purge deletes the blobs. The retention sweep deletes the blobs again when the record is older than 24 hours, and then finishes the record. In code this time is `BlobPurgeGrace`. It is not the retention period.
_Avoid_: Tombstone

**Maintenance mode**:
The installation state in which the tool APIs answer 503 with `Retry-After`: the Pulumi, TFE-compatible and inventory APIs. The installation API and the probes stay available. The server enters it for a restore after the checks pass. An administrator can also enter it with `POST /api/server/maintenance/enter`. The other state is `normal`.
_Avoid_: Downtime, freeze

### Regions

**Home region**:
The one region where a namespace accepts writes.
_Avoid_: Primary region, active region

**Standby region**:
A region that holds a replicated copy of a namespace and accepts no writes to it.
_Avoid_: Secondary region, replica region

**Promotion**:
The administrator action that makes a standby region the home region of a namespace.
_Avoid_: Failover (as the name of the action), switchover

**Failover version**:
A number that increases by one at each promotion of a namespace.
_Avoid_: Epoch, generation

**Fence**:
The namespace and failover version that a write carries. A store rejects the write when the failover version is no longer current.
_Avoid_: Token, guard

### Identity

**Principal**:
A caller whose identity OpenDustpan checked: a person, a service or a workload. Most principals belong to the whole installation. A principal of a namespace is the exception.
_Avoid_: User (when the caller can be a machine), actor

**Principal of a namespace**:
A service principal that belongs to one namespace. A holder of `namespace:manage-principals` on the namespace makes it. It can only hold grants on its namespace or on an object inside it, and it cannot be a member of a group. Its subject is unique on the whole installation. A holder of `namespace:manage-principal-tokens` on the namespace makes the tokens of its principals.
_Avoid_: Namespace service account, local principal

**Principal source**:
Where a principal comes from. The API makes a `local` principal. The first sign-in of a person through an OpenID Connect provider makes an `oidc` principal. A directory sync keeps an `ldap` or `scim` principal. The subject of an `oidc` principal is the provider ID and the subject of the identity provider, separated by a colon. So a provider ID must never change after its first sign-in.

**Sign-in provider**:
An OpenID Connect provider through which persons sign in to the console. A provider of the flags or the configuration file has the source `static`, and the API cannot change it. A provider that the API creates has the source `api`. Each provider has an ID that is part of the subject of each person who signs in through it.
_Avoid_: Identity source, IdP connection

**Installation scope**:
The scope of a secret value that belongs to the installation and to no namespace, for example the client secret of a sign-in provider. The server binds its data key to `@server`, a name that no namespace can have.
_Avoid_: Global scope, server namespace

**Access token**:
The secret that a tool or the console sends to prove which principal it acts for.
_Avoid_: API key, password

**Sign-in**:
The step in which a sign-in method, such as an identity provider, proves who a person is. The method only proves the identity. The server then issues its own session.
_Avoid_: Login, log-in (as a noun). The verb is "sign in".

**Session**:
The proof of a sign-in that the console sends back in a cookie. A session belongs to one person. It ends at a sign-out, when an administrator ends it, after a period with no use, and at a maximum length. By default, the period is 15 minutes and the maximum length is 12 hours.
_Avoid_: Login, cookie (the cookie only carries the session)

**Session policy**:
The administrator settings of the console sessions for the installation: the period with no use after which a session ends, and its maximum length. Only a site administrator can change them.

**Personal token**:
An access token that belongs to a person. A person creates it after a sign-in, and it has an expiry (ADR 53).
_Avoid_: PAT, user token

**Token rotation**:
A call that issues a new access token for the same principal and sets an end time for the old one. The old token works until that end time (ADR 53).

**Token policy**:
The administrator settings of the access token life cycle for the installation: the maximum lifetime of a token and the inactivity disable (ADR 53).

**Inactivity disable**:
The job that disables an access token or a user account after a set period with no use. The default period is 90 days. An administrator can change the period or turn the job off (ADR 53).

**Service token**:
An access token that belongs to a principal that is not a person, for a machine that cannot sign in to the console. Workload identity is the first choice for machines (ADR 54).
_Avoid_: API key, robot token, machine token

**Trust rule**:
A rule in one namespace that matches a JWT from an issuer or a TLS client certificate. It names the principal that the match gets (ADR 54).
_Avoid_: Trust policy, federation rule

**Group**:
A named set of principals of the whole installation. A principal can be a member of more than one group (ADR 52).
_Avoid_: Team (except when naming a tool's own protocol field), collection

**Role**:
A named set of actions (ADR 52). A built-in role has a fixed name. An administrator makes a custom role from actions.
_Avoid_: Permission set, policy

**Action family**:
One of four sets of actions that do not overlap: operator, administration, data and delegation. A role on the installation can hold actions of each family. A role on a namespace, a collection or a unit holds data actions only. A role on a service principal holds delegation actions only.
_Avoid_: Permission category, scope

**Grant**:
A record that gives one role to a principal or a group on one object. The object is the installation, a namespace, a collection, a unit or a service principal. A grant on a unit follows the stable ID of the unit, not its name. A revoke ends a grant and keeps its record for the access-review history.
_Avoid_: Binding, assignment, permission

**Token manager**:
A holder of `principal:manage-tokens` on a service principal. It can make, rotate and revoke the tokens of that principal. So it can act as the principal, with every grant that the principal has now or gets later. The built-in role `token-manager` gives this action on one service principal.
_Avoid_: Token owner, delegate

**Access review**:
An export of the grants that give access to one object. It holds the grants on the object and inside it. It also holds the grants with a role that gives a data action or manages access on each object that contains it. A bootstrap admin token has a row too. It is a snapshot at one time, or a history between two times (ADR 52, decision 10). Each export request writes an audit entry.
_Avoid_: Access report, permission audit

**No access**:
The state of an object or a console section that the principal has no grant to use.
_Avoid_: Locked (that is a unit lock), forbidden, disabled

**Action**:
A tool-neutral operation that a principal asks to do, such as read state or write state. Grants allow actions on the installation, namespaces, collections and units. A policy check can deny an action (ADR 17).
_Avoid_: Permission, verb, scope

**Reach**:
The set of actions on objects that a principal can do. It comes from the grants of the principal and the grants of its groups. It also holds the grants of each principal it can act as through an act-as edge. One reach covers another when it holds each of its actions on each of its objects.
_Avoid_: Effective access, permission set

**Act-as edge**:
A right that lets one principal use the authority of another. It has two forms: `principal:manage-tokens` on a principal, and the right to write a trust rule that names a principal.
_Avoid_: Impersonation, delegation

**Protected group**:
A group whose reach holds a sensitive action that is not confined to one namespace. The sensitive actions are access management on the installation, delegation, sign-in settings, the operator family, and identity provider and SCIM configuration. The identity provider cannot add members to it unless the group trusts the identity provider.
_Avoid_: Privileged group, admin group

**Full sync**:
An installation setting in which the installation trusts the identity provider for every group. A membership that the identity provider ends stays ended. It never changes a break-glass account.
_Avoid_: Full import, auto-sync

**Suspended membership**:
A membership of a protected group that the identity provider ended. It gives no rights. The server keeps the record for 30 days so that a site administrator or a break-glass account can restore it.
_Avoid_: Ended membership, disabled membership

**Pinned membership**:
A restored membership that the identity provider does not manage until a person unpins it.
_Avoid_: Frozen membership, locked membership

**Break-glass account**:
An account of one named person that does not depend on the identity provider. The person signs in with a passkey or a hardware key. Each installation has at least two, unless a self-hosted operator disables break-glass.
_Avoid_: Emergency account, break-glass login

**Admin access report**:
A periodic report of who holds each sensitive installation action and through which path. It lists the protected and trusted groups, the break-glass accounts, the suspended memberships, the refused adds, and the changes. A reviewer acknowledges it.
_Avoid_: Access review, audit report

### Pulumi

**Stack**:
The unit of the Pulumi CLI, named `<namespace>/<Pulumi project>/<stack>`.
_Avoid_: Environment, workspace

**Stack ID**:
The stable, opaque identifier of a stack, with the prefix `stk-` (ADR 19). A rename keeps it. A new stack with the name of a deleted stack gets a new Stack ID.
_Avoid_: Unit ID, stack key, UUID

**Pulumi project**:
A Pulumi program. Stack names are unique within one Pulumi project.
_Avoid_: Project (without a qualifier), application

**Update**:
One operation of the Pulumi CLI on a stack that OpenDustpan records, such as `up`, `preview`, `refresh`, `destroy` or `import`.
_Avoid_: Run, deployment, job

**Preview**:
An update that changes no resources. It takes no number in the history of the stack.
_Avoid_: Plan, dry run (except when naming the CLI flag)

**Lease**:
The time-limited right of one Pulumi CLI process to write to the update that it runs.
_Avoid_: Lock

**Checkpoint**:
The state of a stack that the Pulumi CLI saves during and after an update.
_Avoid_: Snapshot, state file

**Delta upload**:
A checkpoint save that sends only text edits on the last saved checkpoint of the update, with the SHA-256 of the result (ADR 37). The server applies the edits and checks the hash. On a conflict it answers 409, and the Pulumi CLI then sends a full save.
_Avoid_: Delta save, checkpoint delta (except when naming the route `checkpointdelta`), patch, diff

**Event**:
One entry in the progress record of an update.
_Avoid_: Log line, message

### OpenTofu

**Workspace**:
The unit of OpenTofu. Workspace names are unique within one namespace.
_Avoid_: Environment, stack

**TFE project**:
A group of workspaces in the TFE-compatible API. It has no effect on names or state.
_Avoid_: Project (without a qualifier), folder

**Workspace lock**:
The lock that one principal holds on a workspace while it changes the state of the workspace. The lock stays until the principal or an administrator releases it.
_Avoid_: Lease

**Override switch**:
A setting of a namespace or of a unit that allows an override (ADR 44, decision 6). Both settings are off by default. A unit setting only narrows the setting of its namespace.
_Avoid_: Flag, toggle

**Forced write**:
A state write that skips the lineage and serial checks, as `tofu state push -force` does. It needs the action `unit:write-forced` and both override switches on.
_Avoid_: Force push, override (alone)

**Unlocked write**:
A state write to a workspace that no principal holds, as `tofu apply -lock=false` does. It needs the action `unit:write-unlocked` and both override switches on.
_Avoid_: Lockless write

**State version**:
One immutable copy of the state of a workspace, with a lineage and a serial.
_Avoid_: Snapshot, revision, checkpoint

**Sensitive marks**:
The value paths that the `json-state` rendering of one state version marks in `sensitive_values`. The server keeps only these paths, never a value from the rendering (ADR 41, decision 9). A masked state read masks each marked path.
_Avoid_: Sensitive values (that is the field of the rendering)

**Lineage**:
The identifier that all state versions of one state history share.
_Avoid_: History ID

**Serial**:
A number that increases with each state version in one lineage.
_Avoid_: Version number, revision

### Secrets

**Scope**:
The namespace and the stable identity of a unit that one data key protects.
_Avoid_: Context, key ID

**Data key**:
The random key that encrypts all secret values of one scope. OpenDustpan stores it only in encrypted form.
_Avoid_: DEK, secret key

**Key-encryption key**:
The key, kept outside the OpenDustpan database, that encrypts data keys.
_Avoid_: Master key, root key, KEK

**Sealed**:
Encrypted at rest with the data key of a scope. The server seals OpenTofu state and outputs (ADR 41). The server seals a state version that an older server wrote at start or after a restore (ADR 41, decision 11). The part of the server that seals and opens state is the sealer.
_Avoid_: Encrypted (when it can mean the client-side encryption of OpenTofu)

**Secret value**:
A value in state that the tool marks as secret: a Pulumi secret, or an OpenTofu sensitive output or attribute.
_Avoid_: Sensitive value (except for OpenTofu), password

**Masked value**:
A marker that replaces a secret value in a state read. It holds the value path and never the value.
_Avoid_: Redacted value, hidden value

**Masked state read**:
A read of the state of one version. It answers the structure and the values, with a masked value in place of each secret value. It also lists the value path of each masked value (ADR 41). It needs `unit:read`. Examples: the resource list, one resource, the stack outputs and the state diff.
_Avoid_: Redacted read, state view

**State diff**:
A masked comparison of two versions of the state of one unit. It names the resources that only one version holds, the changed resources, and the value paths that changed. It holds no value.
_Avoid_: Delta (a delta is a delta upload)

**Value path**:
A JSON Pointer to one value in the state document of one version, as the tool stores it.
_Avoid_: Attribute path (except for OpenTofu), key

**Reveal**:
A request from a person to see the plain text of a secret value. The reveal call answers the plain value at one masked value path of one version (ADR 41, decision 4). It needs `unit:read` and `unit:read-secrets`. Its routes are `POST /api/stacks/{namespace}/{project}/{stack}/state/{version}/reveal` in the Pulumi Cloud-compatible API and `POST /api/v2/state-versions/{state_version_id}/actions/reveal` in the TFE-compatible API. The call writes an audit entry of the kind `reveal` before it answers.
_Avoid_: Decrypt (that is the Pulumi CLI's protocol), unmask, show secrets

**Audit entry**:
An append-only record of one action in a namespace. It holds who acted, the action, the object and the time. It also holds where the request came from, the kind of access and the outcome. The chain of ADR 60, decision 17 is opt-in. When it is on, each entry holds the SHA-256 of its own fields and of the previous entry of its scope.
_Avoid_: Audit log line, event, audit event

**Audit log**:
The port in `pkg/audit` that keeps the audit entries of each namespace and of the installation (scope `@server`). The SQL table is its record. It is fully on or fully off for an installation (ADR 60).
_Avoid_: Audit trail, activity log

**Audit checkpoint**:
An Ed25519 signature over the hash at one sequence of one audit scope. It holds the scope, the sequence, the time and the key ID. The server keeps it in the blob store under `audit-checkpoints/`. It is opt-in and needs the chain on. It is not a **Checkpoint** of the Pulumi CLI: in code and docs outside the audit log, always write "audit checkpoint".
_Avoid_: Signed head, anchor, checkpoint (alone, outside the audit log)

**Log sink**:
An output adapter of the port `pkg/logsink` that delivers structured records to one target: stdout, a file, syslog, a webhook, OTLP logs or object storage. The audit log and the application log use the same port (ADR 60).
_Avoid_: Log target, log destination, exporter

**Kind of access**:
The field of an audit entry that tells kinds of access apart. It has exactly one value from a fixed list. `tool-operation`: a tool needed secrets for its own work, for example a Pulumi decrypt call or an OpenTofu state download. `reveal`: a person asked to see a secret value. The server cannot see who runs a CLI command, so `tofu state pull` writes a `tool-operation` entry. The Pulumi routes `decrypt/log-decryption` and `decrypt/log-batch-decryption` are routes, not kinds. Each records an entry of the kind `reveal`, because the Pulumi CLI calls them when a person asks it to show secrets. ADR 44 adds `override`: a person used an override. ADR 60 adds `change` (an action that changes something) and `audit-read` (a read or an export of the audit log). ADR 52 adds `read-access`: a read of the access-review export of an object.
_Avoid_: Audit type, `log-decryption` as a kind

**Value count**:
The count of secret values that one Pulumi `decrypt` or `batch-decrypt` call gave the tool. Only the `tool-operation` entries of these two routes hold it.
_Avoid_: Secret count

### Hosted service

**Hosted distribution**:
The build of the server and the console that the project runs as its hosted service. It uses the public SDK and the console extension points. It adds only shims for sign-up, onboarding and billing, hosted defaults and limits, and console labels. It is public source and has no feature that the open-source server lacks (ADR 59).
_Avoid_: Fork, enterprise edition, cloud edition

## Flagged ambiguities

- **Account** is only a label of the hosted console for a namespace (ADR 59). The API, the CLI and error text say "namespace". There is no account object in the server.

- **Group** is a set of principals (ADR 52). RFC 4 used "group" for a set of units before the owner chose "collection". A set of units is a collection.
- **Project** has two meanings. Always write "Pulumi project" or "TFE project".
- **Organization** is the word each tool uses for a namespace. Use it only to name a field or command of a tool's protocol.
- **Lease** and **workspace lock** both stop concurrent writes, but they act differently after a crash. Do not use one word for both.
- **Migration** means a SQL schema migration only. For the move of a unit into OpenDustpan, write "import".
- **Restore** has three meanings. A restore of a backup replaces the data of a whole installation (ADR 38). A restore of a version makes one older version of a unit its new current state (ADR 28). A restore of a deleted unit brings the unit back before its purge (ADR 47). Name which one.
- **Lock** names the workspace lock of OpenTofu and the unit lock of the inventory. Code also says "stack lock" for the hold that a mutating Pulumi update has on its stack. Do not call a lease a lock.
- **Delta** and **diff** are different. A delta upload saves a checkpoint. A state diff compares two versions.
- **Version** is ambiguous between a stack's update number, a state version and a software release. Name which one.
- **Break-glass** has two meanings. A break-glass account is a standing account of one named person. A break-glass recovery mode is one of the three ways to recover when the last administrator loses every factor. Name which one.
