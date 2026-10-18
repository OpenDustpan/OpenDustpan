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

**Minimum binary version**:
The oldest binary version that can use a database schema. A migration that deletes or narrows something sets it, and `schema_migrations` records it with that migration. A binary starts only when its version is at or above the minimum. A build with no release version, for example `dev`, counts as newer than every release.
_Avoid_: Minimum schema version, compatibility level

**Backup**:
The copy of the SQL data of a whole installation with the blob references that the data names. The SQL part is in the portable format. A restore accepts a backup of either dialect and of the same or an older schema version (ADR 42).
_Avoid_: Dump, snapshot

**Baseline migration**:
The one migration of each dialect that replaced every migration before it, before the first tag. Its version is 100. The server refuses a database that records a lower version, and a restore refuses a backup with a lower schema version. Both errors name the baseline.
_Avoid_: Squash, initial migration

**Portable format**:
The dialect-neutral format of the SQL part of a backup: one compressed file of JSON rows for each table (ADR 42). It is the only backup format. It replaces the native format of ADR 30.
_Avoid_: Export format, dump format

**Restore**:
The installation API operation that replaces the SQL data of an installation with a backup (ADR 38). The server checks the backup before it changes live data, applies it in maintenance mode, and takes a backup of the live data first. It is not the restore of one older version of a unit (ADR 28). It is also not the restore of a deleted unit (ADR 47).
_Avoid_: Recovery, load, import

**Control table**:
A table that a restore does not replace (ADR 72, decision 5). The live rows stay across the swap. The installation, backups, restores and durable operation tables are control tables. The claim tokens of durable operations only grow, and an older token from a backup would let a stale holder write.

**Deleted unit**:
A unit after a delete through its tool (ADR 47). It leaves every list and its name is free at once. OpenDustpan keeps its state, history, secrets and grants until a purge. The inventory API lists the deleted units of a namespace, and a restore of a deleted unit brings one back with its stable ID.
_Avoid_: Tombstone (that is the shred record of a purged unit), trashed unit, archived unit

**Retention period**:
The time that a deleted unit stays restorable, from the delete to its purge time (`purgeAfter`). `--soft-delete-retention` sets it. The default is 720 hours, and the minimum is one hour.
_Avoid_: Grace period, TTL

**Purge**:
The final delete of a deleted unit (ADR 47). It deletes the rows of the unit, its data key, its deleted inventory entry and its blobs, and ends the grants on it. It cannot be undone. An admin can purge a unit before its retention period ends.
_Avoid_: Hard delete, wipe

**Final delete**:
The same as a purge. Use "purge" in code and in the API, and "final delete" where a reader needs the plain meaning.
_Avoid_: Permanent delete

**Retention expiry**:
The server task that purges each deleted unit whose retention period ended. It runs one time each hour, the first time one hour after the server starts. `POST /api/server/deleted-units/sweep` runs it at once. It also finishes the blob purge records. It purges without the dependents check, because the retention period was the recovery window of the team (ADR 153). The end of the retention period of a stored plan or a job log is a redaction with the cause `retention` (ADR 152).
_Avoid_: Retention sweep, sweep (that is the Sweep feature), garbage collection, reaper

**Orphan cleanup**:
The task that deletes a key blob with no record (ADR 151 decision 22). It waits a grace period of 24 hours from the write of the blob. So it never deletes the key blob of an item whose transaction has not ended.
_Avoid_: Orphan sweep, sweep (that is the Sweep feature)

**Blob purge record**:
A row that a purge writes for a unit whose blobs may still exist. A save that started before the purge can write a blob after the purge deletes the blobs. The retention expiry deletes the blobs again when the record is older than 24 hours, and then finishes the record. In code this time is `BlobPurgeGrace`. It is not the retention period. It covers the live blob store only. The tombstone of the purge covers the backup targets (ADR 151).
_Avoid_: Tombstone (that is the shred record of a purged unit)

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
Where a principal comes from. The API makes a `local` principal. The first sign-in of a person through an OpenID Connect provider makes an `oidc` principal, and through a plain OAuth 2.0 provider an `oauth` principal. A directory sync keeps an `ldap` or `scim` principal. The subject of an `oidc` principal is the provider ID and the subject of the identity provider, separated by a colon. So a provider ID must never change after its first sign-in.

**Sign-in provider**:
An OpenID Connect provider or a plain OAuth 2.0 provider through which persons sign in to the console. Its mode is `oidc` or `oauth`. A plain OAuth 2.0 provider identifies a person by a claim of its user-info answer. A provider of the flags or the configuration file has the source `static`, and the API cannot change it. A provider that the API creates has the source `api`. Each provider has an ID that is part of the subject of each person who signs in through it.
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

**Journal entry**:
One small record that the Pulumi CLI sends for one resource operation of a journaled update, in batches. The server builds the checkpoint of the update from its journal entries.
_Avoid_: Entry (alone)

**Journal version**:
The number that the start of an update agrees on for journaling. It sets the shape of the journal entries that the Pulumi CLI sends. Version 0 means the CLI saves checkpoints instead of journaling.
_Avoid_: Version (alone)

### OpenTofu

**Workspace**:
The unit of OpenTofu. Workspace names are unique within one namespace.
_Avoid_: Environment, stack

**TFE project**:
A group of workspaces in the TFE-compatible API. It has no effect on names or state.
_Avoid_: Project (without a qualifier), folder

**Plan**:
The OpenTofu record of the changes that an apply makes to a workspace. The Pulumi CLI calls its record of planned changes a preview. A plan that a plan phase of a job keeps is a stored plan (ADR 102).
_Avoid_: Preview (for OpenTofu), dry run, diff (a diff compares two state versions)

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
Encrypted at rest with the data key of a scope, or with the item key of a history item. The server seals each checkpoint blob, event payload and journal batch of a Pulumi update with the item key of the update (ADR 151). It seals the state blob and the outputs document of an OpenTofu state version with the item key of the state version. The part of the server that seals and opens content is the sealer.
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

**OpenDustpan Cloud**:
The hosted service that the project runs. It is the build of the server and the console with shims for sign-up, onboarding and billing, hosted defaults and limits, and console labels. It is public source and has no feature that the open-source server lacks (ADR 59, ADR 77).
_Avoid_: Fork, enterprise edition, hosted distribution, cloud edition (as an edition with its own features)

### Resources and self-service

**Unmanaged resource**:
A cloud resource that does not have exactly one unit that manages it. It has three kinds. **Not in any unit**: the state of no unit holds the resource, and only a cloud sweep can find it. **Left behind**: only deleted units hold its resource key. **Managed twice**: two or more live units hold its resource key, from one tool or from both (ADR 163).
_Avoid_: Orphan (that is a stored file), ghost resource, shadow resource

**Catalog**:
The self-service gateway of OpenDustpan, at the API path `/api/catalog`. A principal sends a Catalog request to it, for example for a unit from a blueprint. Write it with a capital C, as a name.
_Avoid_: Service catalog, marketplace, portal, inventory

**Catalog request**:
A declarative JSON document that a principal sends to the Catalog. It states the result that the principal wants, not the steps to get it.
_Avoid_: Order, ticket, form

**Blueprint**:
A program that a platform team offers for self-service, with an input schema, platform values and a version. Units made from it are normal stacks or workspaces.
_Avoid_: Template (Pulumi templates copy code), no-code module, product, offering, golden path

### Jobs and runners

**Job**:
Work that OpenDustpan asks an executor to do on one unit (ADR 100 decision 19). Its unit is a unit reference with the stable unit ID set, so a rename of the unit keeps its jobs. A change to many units is many jobs. A job has one of four tool-neutral operations: `plan`, `apply`, `destroy` or `refresh`. Each tool maps them to its own commands, and user-facing text keeps the words of the tool. A job is a list of phases. The server fixes the spec of a job at start. A later change of the unit settings or of the branch does not change it.
_Avoid_: Task, deployment, run (without a tool), operation (alone: name the operation of the job or the durable operation)

**Executor**:
The way a job runs: write-back or runners (ADR 100 decision 19). The port `job.Executor` names the kind, `write-back` or `runner`, and gives the phases of a spec (ADR 101 decision 1). It also undoes what a job left open after a cancel, a rejection or an expiry. Both executors call the same progress ports, `job.Claimer` and `job.Recorder`. So both meet the same rules for claims, cancel, steps, logs and plans.
_Avoid_: Launcher (that is the program of a runner that starts each step)

**Phase**:
The work that one executor does with one claim (ADR 100 decision 4). A phase has one of three kinds. `plan` holds runner steps that change nothing, and the stored plan. `apply` holds runner steps that change resources or write state. `deliver` is the commit of a write-back job. The executor gives the phases at start, and they do not change after. Each phase is one durable operation with the start key `<job ID>/<phase index>`. A takeover is a new claim on the same operation. Between two phases, no executor holds the job, so a wait for a person never keeps a runner busy.
_Avoid_: Step (a step is one argument list that a runner starts in a phase), stage

**Gate**:
A condition in front of a phase that must open before the phase dispatches (ADR 103 decision 2). A phase can have more than one gate, and it dispatches only when every gate is open. Until then, the job waits in `awaiting-approval`. One rejection of any gate rejects the job. A job that waits longer than the timeout of a gate expires, and the default timeout is 7 days. A gate has one of two types: an approval gate, which comes from the policy decision, or a confirmation gate. For a write-back job, a gate on the deliver phase waits before the commit. The review request then still needs the approvals of the forge (ADR 103 decision 3).
_Avoid_: Approval (alone: an approval passes an approval gate), check

**Confirmation gate**:
The gate in front of each phase that changes, for a job that an agent started in approve-first mode (ADR 103 decision 2). A write-back job of an agent has it in front of its deliver phase in every mode (ADR 128 decision 4). Only the person who asked the agent can pass it. That person gives one confirmation in a request of their own with no agent. The request comes from an interactive console session, never from an access token (ADR 128 decision 5). It has a fresh authentication. That is a new MFA step where built-in MFA covers the person. Otherwise, it is a new authentication at the SSO identity provider of the person. Its maximum age is the lower of 15 minutes and the token freshness window of the session policy (`job.ConfirmMaxAge`, ADR 128 decision 7). With a window of 0, the maximum age is 15 minutes. A confirmation with no fresh authentication fails with `ErrAuthStale`. The confirmation binds to the plan when a plan phase comes before. A policy never gives it and never takes it away. `job.Service.Confirm` records it and needs the start action of the job, not `job:approve`. An agent cannot give a confirmation (ADR 103 decision 7).
_Avoid_: Approval (an approval passes an approval gate), consent

**Request-changes thread**:
A thread on a job that a blocking comment opens (ADR 128 decision 11). A reviewer with `job:approve` opens it, or an agent of such a reviewer. The person who started the job cannot open one on the own job. While a thread is open, no approve verdict and no confirmation of the job is accepted (`job.ErrChangesRequested`). The person who opened the thread, or another holder of `job:approve`, resolves it, each in a request with no agent. The person who started the job never resolves it, and an agent never resolves it. An agent never rejects a job: a blocking comment is the way for an agent to stop one.
_Avoid_: Change order, rejection (a rejection ends the job), review (alone: that is the review request of the forge)

**Dismissal**:
The resolve of a request-changes thread by a person other than the person who opened it (ADR 128 decision 14). A dismissal needs a reason, and writes an audit entry with that reason.
_Avoid_: Override (that is a break-glass or a forced write)

**Agent mode**:
The mode that a user sets for the agent that acts for them: approve-first, review-request or direct (ADR 103 decision 12). `job.CheckAgent` holds the rule at start. Approve-first is the default: each phase that changes gets a confirmation gate. In review-request mode, the agent changes a unit only through a write-back job that delivers as a review request. Its deliver phase still has a confirmation gate (ADR 128 decision 4). In direct mode, a runner job of the agent changes a unit with no confirmation. A write-back job pushes after the confirmation of its delivery. Direct mode needs three switches, each off by default: one of the installation, one of the namespace of the unit and one of the user. A runner job that only plans needs no mode, and it starts in every mode.
_Avoid_: Autonomy level, agent permission

**Freeze**:
A change freeze on a namespace (ADR 103 decision 4). While it is active, no phase that changes resources, state or the code in git dispatches. Freezes are opt-in. A namespace has no freeze until a principal with `namespace:manage-freezes` creates one. A freeze has a reason, a start and an optional end. Plans still run, and a phase that runs when the freeze starts runs to its end. A queued job waits with the reason `frozen`, and dispatches after the freeze ends. A freeze never refuses a state write, because it stops only the work that OpenDustpan starts (ADR 103 decision 5).
_Avoid_: Maintenance mode (that stops the tool APIs of a whole installation), lock

**Write-back**:
The executor in which OpenDustpan writes a change to git, and the team's existing CI applies it (ADR 100). The change is a direct push or a review request. It runs in the server: each instance claims a deliver phase with a `durable.Runner` (ADR 101 decision 3). It commits through the API of the forge, never with a git client. Each commit has the trailer `Dustpan-Job: <job ID>`. A write-back job does `apply` only, and edits only the config or variable files of the unit. The next final state version of the unit closes the job, after the merge for a review request. A job that sees no such version expires after 14 days by default. OpenDustpan approvals add to the approvals of the forge.
_Avoid_: Push (alone: a direct push is one of the two deliveries)

**Code location**:
The code of a unit that a job runs on, as the unit settings name it (ADR 100 decision 5, ADR 125). A start request gives the code location, and the server resolves it at start with the commit. A later change of the head of the branch does not change the job. Each phase of a job runs on one commit. A write-back edit must be a path in the directory of the code location (ADR 101 decision 4). No part of that directory can be CI configuration. A review request needs a code location for the unit (ADR 125).
_Avoid_: Source (that is the backend of a unit before an import), repository (alone)

**Stored plan**:
The plan that one plan phase of a job keeps, with an ID that has the prefix `pln-` (ADR 102). It has two parts. `plan-display` is the readable JSON plan that the console shows. `plan-apply` is the plan that the apply phase reads, for example an OpenTofu plan file. Only the runner of the apply phase of the same job reads `plan-apply`, and no person can download it. Each part is one blob with a kind, a size, the SHA-256 of the plaintext and a flag `secret`. The plan records the one state version that it read. It does not change after `PutPlan`. Its own item key seals it, and the server keeps it 400 days from its creation by default (ADR 152). A gate can bind approvals to it. A stale plan never applies: the job goes back to its plan phase. A `pulumi preview` from a workstation is not a stored plan (ADR 152 decision 12).
_Avoid_: Plan file (that is the `plan-apply` part for OpenTofu), preview, saved plan

**Break-glass (job)**:
A reason in a start request that lets a job start during an active freeze (ADR 103 decision 6). Without it, the start fails with `ErrFrozen`. It needs `namespace:break-freeze` on the namespace. That action is in another family than `namespace:manage-freezes`, so a principal that can end a freeze does not also get break-glass. The job keeps the break-glass and the freeze ID. The audit entry of the start has the kind `override`. An agent cannot ask for break-glass (ADR 103 decision 7).
_Avoid_: Freeze override, bypass

**Job token**:
An access token that the server makes for the steps of a runner phase, at the claim of the phase (ADR 100 decision 20). It is a token of the principal that started the job. The server limits it to the unit of the job, and it has the expiry of the claim. It carries the job-ID claim. The launcher gives it to the tool (ADR 133, ADR 134). OpenTofu gets it through its credentials helper, and the Pulumi CLI through a file of mode 0600. It never goes in an environment variable. A job token of a job that ended authenticates nothing.
_Avoid_: Runner token (the credential of a runner is the runner key)

**Job-ID claim**:
The one claim that a job token puts on the principal that it authenticates: the ID of the job (ADR 100 decision 20). The `identity` package holds it in `Principal.JobID`, which is empty for every other credential. The claim adds no action. A state write with the job token reports it as `StateVersion.Job`. So the job record and the history of the unit can show which state version the job wrote.
_Avoid_: Claim (alone: a claim is also the durable claim of a phase), job claim

**Runner**:
A long-lived process that the team hosts, of the kind `kubernetes` or `machine` (`runner.Kind`). It registers in one runner pool, claims work for the pool, and starts a launcher for each claim in an isolated place. A runner never runs a step itself, so a runner is a coordinator (ADR 130 decision 1). A runner is not a principal. Its credential is a runner key, and it never calls the REST API (ADR 133 decision 1). Each runner holds one claim at a time by default (ADR 130 decision 6).
_Avoid_: Agent, worker

**Runner pool**:
A set of runners that belongs to one namespace (ADR 101 decision 14). A runner belongs to one pool and claims only the durable operations of the kind `job.runner.<pool ID>`. A runner job names its pool at start, and the pool must belong to the namespace of the unit. Else the start fails with `ErrInvalid`. So a runner of one namespace never runs a job of another. The action `runner:claim` acts on a runner pool, a new object kind of the `identity` package (ADR 103 decision 8).
_Avoid_: Label (labels inside a pool are a later step), runner group

**Pool purpose**:
The kind of work of a runner pool: `jobs` or `sweeps` (`runner.Purpose`). It never changes after the creation of the pool. A change fails with `runner.ErrImmutable` (ADR 130 decision 3). The server takes the durable kinds that a runner claims from its pool, never from the runner. A runner of a pool for jobs claims only `job.runner.<pool ID>`. A runner of a pool for sweeps claims only `unmanaged.sweep.<pool ID>`. A runner job that names a pool for sweeps fails at start with `runner.ErrPurpose`. A pool for sweeps carries a read-only identity, and `Pool.ReadOnly` is true for it. The server cannot check the role of a host (ADR 130 decision 5). The console labels the purpose "Sweeps".
_Avoid_: Pool type, pool mode, sweep pool, job pool

**Coordinator**:
The role of a runner: it claims work and starts one launcher for each claim, and never runs a step itself (ADR 130 decision 1). The Kubernetes coordinator creates one Kubernetes Job for each claim (ADR 136). The machine coordinator starts one sandbox for each claim on its own Linux host (ADR 137). Each replica of the Kubernetes coordinator is a runner of its own, with its own key (ADR 136 decision 8). The ADRs use "the runner (the coordinator)" for one process (ADR 131 decision 1).
_Avoid_: Controller, scheduler, orchestrator

**Isolation**:
The kind of isolated place in which a runner starts its launchers (`runner.Isolation`): `kubernetes`, `firecracker`, `gvisor` or `process`. A runner joins with one isolation kind. A Kubernetes runner uses `kubernetes`, and a machine runner uses one of the other three. A pool lists the isolation kinds that its runners may use. An empty list allows every kind except `process`. A pool must list `process` by name to allow it, and the console shows a warning on such a pool (ADR 130 decision 2). The `process` sandbox has no kernel boundary and is for tests and for a developer machine (ADR 137 decision 2).
_Avoid_: Isolation level, sandbox type, runtime

**Launcher**:
The static binary `dustpan-launcher` that runs the steps of one claim inside the Kubernetes Job or the sandbox (ADR 130 decision 1, ADR 134). Each claim is of one phase or one sweep. It is pure Go with no cgo, for Linux on each `GOARCH` of the release. It needs no shell, no git and no C library from the image of the team. It starts each step as an argument list, with no shell, in its own process group. It opens the job session and checks the source snapshot before the first step. It gives credentials to the steps of the role `tool` only, and masks secret values in each log. It is also the credentials helper of OpenTofu and the git credential helper of the mode `forge`. The runner never holds a credential of a step (ADR 133 decision 13). The release of a runner is the release of its launcher (ADR 132 decision 1).
_Avoid_: Executor (that is write-back or runners, ADR 100 decision 19), runner binary, shim

**Runner protocol**:
The HTTP and JSON protocol under `/runner/v1` for runners and launchers (ADR 131). The runner opens every connection over HTTPS, and the server never connects to a runner. The runner calls the paths of the runner with a proof of its runner key. The launcher calls the paths of the session with a proof of its session key. Each request carries the header `Dustpan-Release`. A server accepts a runner or a launcher of its minor release and of the two minor releases before it (ADR 132 decision 2). The paths are not part of the REST API (ADR 138 decision 6).
_Avoid_: Runner API, agent protocol

**Assignment**:
The work of a phase or of a sweep that the launcher reads (ADR 132 decision 6). It holds the steps, the source, the plan and the settings. Its shape has a version (`runner.AssignmentVersion`). A runner sends the highest version that its launcher reads, and the claim never takes an operation above it. A field that changes what a step does raises the version (ADR 132 decision 7).
_Avoid_: Work (that is the poll answer, which holds no step and no secret, ADR 131 decision 6)

**Join token**:
A secret that lets one runner join one pool one time (ADR 133 decision 2). A principal with `namespace:manage-runners` creates it. It lives 1 hour by default and 7 days at most (`runner.JoinTTL`). The secret starts with `dpj_`, so secret scanners find it. The server shows the secret once and keeps only its SHA-256. A principal can revoke an unused token. A pool has at most 100 open join tokens (ADR 130 decision 8). Only a join token holds the action `runner:join` (ADR 138 decision 1). A second join with one token fails with `token-used`.
_Avoid_: Registration token, enrollment token

**Runner key**:
The credential kind of a runner: an Ed25519 key pair that the runner makes at its join (ADR 133 decisions 1 and 3). The private key never leaves the runner. A runner key holds only `runner:claim` on its pool, and only through the runner protocol. Each request of the runner carries a proof of the key in the header `Dustpan-Proof` (ADR 133 decision 5). A runner key cannot write a step, a log or a plan (ADR 131 decision 1). A key older than 25 days gets `rotateKey` in the heartbeat answer. A key older than 30 days authenticates nothing, and the runner must join again (ADR 133 decision 6).
_Avoid_: Runner token, runner secret, service token (a runner is not a principal)

**Join again**:
A join with a name that the pool already has, of a runner that is not retired (ADR 133 decision 3). It keeps the runner ID and replaces the key. It works only for a runner that is offline or has the status `must-rejoin`. A join again of a runner that still sends heartbeats fails with `runner.ErrRunnerOnline`. In its transaction, it expires each live claim of the runner with no job session, and keeps each claim with one. It also clears the previous key. It writes the audit entry `runner-rejoined` (ADR 138 decision 4). A runner whose key is older than 30 days must join again with a new join token (ADR 133 decision 7).
_Avoid_: Re-register, re-enroll, rejoin (except in the wire values `must-rejoin` and `runner-rejoined`)

**Retire**:
The act that ends a runner (ADR 133 decision 8). A principal with `namespace:manage-runners` retires a runner with `POST /api/runners/{namespace}/runners/{runner}/retire` (ADR 138 decision 6). A retired runner authenticates nothing. The retire expires the claims of the runner in the same transaction, and the takeover rules apply. It also ends every job session of the runner, so each later request of such a session fails with `retired`. The record stays for the audit log, with the status `retired` (ADR 130 decision 9). A join again does not apply to a retired runner. To retire is not to drain: a draining runner only claims no new work.
_Avoid_: Delete (a delete of a pool deletes its runners), deregister, revoke

**Handoff token**:
The credential that a runner gets from the server for one claim and gives to the launcher of that claim (ADR 133 decision 9). The launcher exchanges it one time for a job session. It lives 10 minutes (`runner.HandoffTokenTTL`) and starts with `dph_`. The server keeps only its SHA-256. It never passes through an environment variable, a command-line argument or a file on a disk. A second token for a claim with no session replaces the first. A job token is a different credential: the access token of a tool (ADR 100 decision 20).
_Avoid_: Job token (that is the access token of a tool), launcher token

**Handoff**:
The delivery of a handoff token from the runner to its launcher (`runner.HandoffFor`, ADR 133 decision 10). On Kubernetes, the pod pulls it: the launcher sends its projected service account token, and the coordinator checks it with a TokenReview. In a gVisor container, the launcher reads it on its standard input. In a Firecracker microVM, the launcher reads it from a vsock connection. A sandboxed process reads it one time from an inherited pipe, file descriptor 3. The runner keeps no copy after the handoff. The flag `--handoff` of the launcher names the form: `kubernetes`, `stdin`, `vsock` or `pipe` (ADR 134 decision 3).
_Avoid_: Token injection, bootstrap

**Job session**:
The session that the launcher of one claim opens with its handoff token (ADR 133 decision 11). The launcher makes an Ed25519 key pair in memory and never writes the private key to a disk. In one transaction, the server uses the handoff token, checks that the claim is live, and stores the session. The session lives as long as the claim. Each later request of the launcher carries a proof of the session key. A session key cannot claim work (ADR 131 decision 1). After the session opens, the launcher renews the claim, not the runner (ADR 131 decision 7). A session of a claim that a takeover replaced authenticates nothing. The launcher of a sweep also opens a job session (ADR 131 decision 3).
_Avoid_: Session (alone: that is a console session), launcher session

**Source snapshot**:
The code of one commit of one repository in the blob store (ADR 135 decision 1). It holds the raw git pack with the commit and every tree and blob that the commit reaches. It also holds the allowed submodules and Git LFS objects. Its key is the namespace, the forge connection, the repository, the commit and the hash of the source policy. Two jobs on one commit with one policy share one snapshot. At the start of a runner job, the server gets the pack in a durable operation of the kind `source.fetch`. The launcher reads the snapshot through its job session and checks it before it writes a file. So a runner never connects to the forge for the code (ADR 135 decision 3). The server deletes its blobs 7 days after the end of the last job that names it. A backup does not hold them (ADR 135 decision 9).
_Avoid_: Snapshot (alone), source (alone: that is the backend before an import), clone, checkout (the files that the launcher writes from a snapshot)

**Source policy**:
The setting of a namespace that decides which submodules and Git LFS objects a source snapshot holds (`source.Policy`, ADR 135 decision 6). It is empty by default, so a snapshot holds no submodule and no Git LFS object. `submodules` and `largeFiles` list `https` URL patterns, at most 64 in each list. `failOnDenied` fails the `source.fetch` operation when the commit has a submodule or a pointer that the policy does not allow. A change of the policy needs `namespace:manage-runners` and makes a new snapshot key. The routes are `GET` and `PUT /api/runners/{namespace}/source-policy` (ADR 138 decision 6).
_Avoid_: Submodule allowlist

### Agents

**Dusty**:
The built-in AI agent of OpenDustpan (ADR 120). It is a Go agent loop in each server instance, on durable operations, so a run continues after a restart. It is off until an administrator connects a model. It is not a principal: it holds no grant, no role and no token. No tool of Dusty runs a shell, a command, a script or code that the model writes.
_Avoid_: Assistant, copilot, bot

**Outside agent**:
An AI agent that a team brings, which calls the agent tools through the MCP endpoint or the stdio proxy (ADR 123). It gets the same tools, the same act-as and the same audit log as Dusty. It uses no model of the installation, so it pays for its own tokens.
_Avoid_: External agent, third-party agent, MCP client (that is the program)

**Agent run**:
One request of a person to Dusty, from a prompt to its end (ADR 120 decision 6). The ADRs call it a run. It ends at a final answer, at a limit, at a cancel or when its credential ends. Each run has its own limits, its own cost and a stop reason. A run is a chain of turns, and holds no claim between turns.
_Avoid_: Run (alone, where an update or a job can be meant), session, job, task

**Turn**:
One model call and the tool calls of its answer (ADR 120 decision 8). Each turn is one durable operation of the kind `agent.turn`, with the steps `check`, `model`, `tools` and `advance`, in this order. The `advance` step starts the next turn, or ends the run when the answer was final.
_Avoid_: Step (a turn has steps), iteration, round

**Act-as**:
The rule that an agent run acts as its person, at the time of each call (ADR 120 decision 13). The run records the subject and the credential ID, never the credential. It resolves the credential at the start of each step and again before each tool call. So a run never has more access than its person, and needs no act-as edge (ADR 120 decision 15).
_Avoid_: Impersonation, delegation, agent identity

**Agent tool**:
One of the 12 task-shaped tools that an agent can call (ADR 122). The ADRs call it a tool. Each agent tool declares its action and its object, as an endpoint does (ADR 17). No agent tool takes a command, a script, a query language or code to run.
_Avoid_: Tool (alone, that is an infrastructure-as-code CLI), function, command, skill

**Agent ref**:
The reference to the agent that acts for a person (`agent.Ref`, ADR 125 decision 9). It holds the channel, and the run ID or the MCP session ID. `Ref.Validate` refuses a ref with no ID or with the ID of the other channel. The job model holds the same fields in `job.AgentRef` (ADR 128 decision 1). It records the reference on the job, on each approval and on the decision input. Each audit entry of the job holds it in the agent block (ADR 128 decision 17). A job never names an agent by a name only.
_Avoid_: Agent ID, agent name, agent string

**Channel**:
The field of an audit entry that names where a request came from. ADR 126 adds three values: `agent` for Dusty, `mcp` for the MCP endpoint and `mcp-proxy` for the stdio proxy (ADR 126 decision 2). The client declares the channel. The MCP endpoint accepts only `mcp` and `mcp-proxy`, never `agent` (ADR 123 decision 8).
_Avoid_: Transport, source

**Model connection**:
The one model that an administrator with `server:manage-agent` connects for the installation (ADR 120 decision 3). It holds the adapter, the base address, the model name, the credential and an optional output token limit for each call (ADR 121 decision 9). The credential is a sensitive setting. With no model connection, Dusty is off.
_Avoid_: Model config, provider account, API key

**Reaper**:
The durable operation that ends each agent run whose turn ended with no step that ends the run (ADR 120 decision 10). Its kind is `agent.KindReap`, and it runs every minute on one instance. It first deletes each sealed reveal past its hard expiry. It then ends each such run as canceled, or as failed with the stop reason `turn-failed`.
_Avoid_: Retention expiry (that purges deleted units), garbage collector, janitor

**MCP endpoint**:
The route `/api/agent/mcp` that serves the agent tools over the Streamable HTTP transport of the Model Context Protocol (ADR 123). It offers tools only: no resources, no prompts and no sampling. It needs no model connection. It is off until an administrator sets `mcpEnabled: true`. It answers 404 while it is off or while the audit log is off.
_Avoid_: MCP server (as a separate program), agent API

**Stdio proxy**:
The client command `opendustpan agent mcp` (ADR 123 decision 8). It reads JSON-RPC messages on stdin, forwards each to the MCP endpoint and writes the answers on stdout. It has no tool of its own and no cache. It never opens the database, the blob store or the key-encryption key. Its channel is `mcp-proxy`.
_Avoid_: MCP proxy, CLI proxy, bridge

**Escape hatch**:
The one gated path by which an agent reads secret values in plain text: `read_state` with `secretValues: true` (ADR 124 decision 1). It needs `unit:read-secrets` and each stage of the secret gate that applies. By default it is shut, and each agent tool answers masked values.
_Avoid_: Secret hatch, backdoor, unmask mode

**Secret gate**:
The list of stages that the server checks before an agent reads secret values (`agent.SecretAccess.Check`, ADR 124 decision 2). The stages, in order: the installation stage, the namespace opt-in, the person opt-in, and the consent for one use (outside agents only). It is in the `Toolset`. No setting of a model, a prompt or a client can open it.
_Avoid_: Secret switch, secrets toggle

**Maintenance window**:
The record that groups the instances that start with the secret flag, so that one acceptance covers them all (ADR 124 decision 4). The first instance opens it and puts the installation in maintenance mode, in one transaction. Later instances join it. In code it is `agent.SecretWindow`, in `agent.WindowStore`. The window table is a control table. Maintenance mode is a state of the installation, and the window is the record that puts the installation in that state.
_Avoid_: Maintenance mode (that is the installation state that a window starts)

**Sealed reveal**:
The tool result of a secret read of an agent run (`agent.SealedReveal`, ADR 124 decision 9). The server seals it with a data key of its own, in its own table. The key-encryption key wraps the data key. It opens only for the run, the turn and the call that made it. The next model step puts its values into one request and then shreds it. Its hard expiry is its time plus the time limit of the run (`SealedReveal.ExpiresAt`), so it never outlives its run. The table is not in the backup.
_Avoid_: Reveal (alone, that is a request of a person), secret cache

**Consent**:
The approval that a person gives in the console for one secret read by an outside agent (ADR 123 decision 10, ADR 124 decision 10). The ADRs also say "consent for one use". It holds for one call, one tool, one unit, one token and one MCP session, for 5 minutes. The person approves it with a console session and a fresh MFA step, never through MCP. A second use is refused.
_Avoid_: Approval (that is a job gate), opt-in, permission

### History

**History item**:
One entry with content in the history of a unit (ADR 150). It is one Pulumi update with its checkpoints, events and journal entries, or one OpenTofu state version. It can also be one stored plan or one version of a job log, which the job model owns (ADR 152). Shared code names an item by its unit, its kind (`state`, `plan` or `job-log`) and the ID that the tool gives the item. The core never parses the item ID. A preview is not a history item.
_Avoid_: Revision, version (alone), history entry

**Latest state item**:
The state item of a unit that no redaction can target (ADR 150, ADR 151). For a stack, it is the update that holds the checkpoint that `stack export` answers. For a workspace, it is the current state version. It also holds the replay base of each update that has not ended or whose journal the server did not replay yet. A request to redact it answers 409 with `history.ErrLatest`.
_Avoid_: Current item, head

**Item key**:
The random 256-bit key that seals all content of one history item (ADR 151). The server never derives it from another key. The `Wrapper` wraps it, as it wraps a data key. The server writes the wrapped key one time, as the blob `item-keys/<item key ID>`. SQL keeps only a record of the key. An item key ID has the prefix `ik-`. The Pulumi CLI never sees an item key, and the data key of the unit does not change.
_Avoid_: Version key, derived key, data key (that is the one key of a scope)

**Redaction**:
The act that destroys the content of one old history item and keeps its metadata (ADR 151). It cannot be undone. The server shreds the item key of the item and deletes its content blobs. It keeps the item row, the hashes, the resource change rows, the redaction record (ID prefix `rdx-`) and the audit entry. A person needs `unit:redact` and gives a reason. The end of the retention period of a plan or a job log is also a redaction, with the cause `retention` (ADR 152). No redaction can target the latest state item. A redaction is not masking: a masked state read hides a secret value and keeps it.
_Avoid_: Masking, purge (that ends a whole unit), wipe, permanent delete

**Shred**:
To make an item key unusable, so that nobody can open the content that it sealed (ADR 151). In one fenced transaction, the server marks the key record shredded. From then on, each open of the content fails with `secret.ErrShredded`, on every instance. After the commit, the shred operation deletes the key blob and the content blobs from the live blob store and from each backup target. A redaction shreds the item key of one item. A purge shreds every item key of the unit.
_Avoid_: Wipe, delete (alone)

**Redaction ledger**:
The ledger objects that a backup target holds for each redaction and each purge (ADR 151). Before it deletes blobs in a target, the shred operation writes `redactions/<redaction ID>.json` or `tombstones/<tombstone ID>.json` there. A backup also writes each ledger object that its target does not hold yet. A redaction ledger object names the redaction, the cause, the unit, the item, the item key ID and the blob keys. It also holds the time and the hash of the audit entry, and no reason and no name. `backup check` and a restore treat each blob that the ledger names as an expected gap. A restore applies every redaction and every purge of the ledger.
_Avoid_: Redaction log (that is the port of the redaction records in SQL), shred list

**Tombstone**:
The durable shred record of a purged unit (`history.Tombstone`, ID prefix `tmb-`, ADR 151). A purge writes one, with the cause `purge`, in its fenced transaction. It names the unit, every item key of the unit and the blob key prefixes of its content. It also holds the actor, the time and the audit entry hash. It has one shred target for the live blob store and one for each backup target. It is in a control table, so a restore does not replace it. It can outlive the unit and its namespace. It is not the stored row of a deleted unit.
_Avoid_: Purge record, blob purge record (that is the live blob delete of a purge), deleted unit

**Replay base**:
The checkpoint that a journaled Pulumi update builds on (ADR 151, ADR 43). Some replay bases are part of the latest state item, so no redaction can target them. These are the replay bases of each update that has not ended or whose journal the server did not replay yet. After a replay that fails for good, the replay base stays the latest state item.
_Avoid_: Base checkpoint, parent checkpoint

**Blame**:
The record of which history item changed each resource of a unit, with its actor: the principal, the access token and the job (ADR 150). Each tool computes blame for its own state items. After a state item commits, a durable operation compares it with the previous state item of the same unit. It writes one resource change for each resource that the item changed. A blame cursor on each unit names the last item that the operation compared. Blame lags the write by the time of the operation.
_Avoid_: Attribution, audit (that is the audit log)

**Resource change**:
One row of blame: one resource that one state item created, updated, replaced or deleted (ADR 150). It names the resource by its URN for Pulumi, or by its resource instance address for OpenTofu. A resource with the same name and a new ID is `replaced`. The row holds the change kind and the count of value paths that changed. It never holds a value or the value path of a secret value. Each tool keeps its rows in its own table. A redaction keeps the rows of the item.
_Avoid_: Resource event, resource diff, state diff (that compares two versions)

**Point-in-time view**:
A read of a unit, or of the inventory of a namespace, at a time `at` in Unix milliseconds (ADR 150). For a unit, it answers the masked state read of the state item that was current at that time, with its actor. For a Pulumi stack, the view is per update, not per checkpoint save. The view of a redacted item holds no state, and lists the resources from the resource changes. The inventory view reads the inventory change log. Every view uses the access of the caller now, not the access at the time `at`.
_Avoid_: Snapshot, time travel, historical state

**Inventory change log**:
The append-only log of each inventory write (ADR 150). Each tool appends one row in the transaction of each inventory write: `created`, `changed`, `renamed`, `deleted`, `restored` or `purged`. A row holds the fields of the inventory entry at that time, the time of the write and a sequence number. The store assigns the sequence number in the order of commits. A purge keeps the rows of the unit, which hold names and counts and never state. The server keeps the log forever, and no setting changes this. A rebuild of the inventory cannot rebuild the log in full.
_Avoid_: Inventory history, audit log (that records actions)

**Dependency edge**:
A record that a dependent reads the outputs of a source unit (ADR 153). It holds the source unit, the dependent unit when the server knows it, the principal subject, the kind and the evidence. It also holds the first and the last time seen and a count of reads. The first kind is `output-read`. The evidence is `job`, `lock`, `principal` or `declared`. An edge can link units of two tools or of two namespaces. An edge with the evidence `job`, `lock` or `declared`, last seen within the dependents window, blocks a delete that is not forced. An ambiguous `lock` edge and a `principal` edge never block.
_Avoid_: Reference, link (that is a namespace link or a context link)

**Dependents window**:
The time in which a dependency edge counts, from its last time seen (ADR 153). The default is 90 days, and the setting is `--dependents-window` (`DUSTPAN_DEPENDENTS_WINDOW`). The window must be at least the retention period, or the server refuses to start. So the edge of a deleted dependent stays in the window while a team can restore that dependent. The graph uses the same window (ADR 162).
_Avoid_: Edge TTL, lookback

**Recovery window**:
The time from the delete of a unit to its purge (ADR 153). It is the retention period, and no second period exists. In this window, `unit:restore` brings the unit back with its ID, state, history, secrets, grants and edges. A read of its outputs through its old name answers 404. When no live unit holds the name, the server counts the read on the deleted unit. The deleted units list answers this count and the last time.
_Avoid_: Grace period, undo window

### Context, graph and Sweep

**Sweep**:
The feature that finds unmanaged resources, and one run of it (ADR 163). A sweep is a cloud sweep or a state check. The console shows both kinds together. As the name of the feature, write Sweep. The Go names of `pkg/unmanaged` keep "scan", for example `Scan` and `ScanStore`, because they are internal.
_Avoid_: Scan (except in the Go names of `pkg/unmanaged`), discovery, crawl, sweep for anything but the Sweep feature (write "retention expiry" or "orphan cleanup")

**Cloud sweep**:
A sweep that reads the cloud on a runner of the team, never in the server (ADR 164). A runner of a pool of the purpose `sweeps` claims the sweep and runs the command of its sweep target. The command runs with the workload identity of the runner host. The runner uploads a sweep report, and the server matches it against the resource index. Only a cloud sweep can find a resource that is not in any unit. A sweep is not a job, and the server holds no cloud credential for it.
_Avoid_: Cloud scan, scan job

**Sweep target**:
The namespace data that tells a cloud sweep what to read (`unmanaged.Target`, ADR 164). It holds a name, a runner pool of the purpose `sweeps`, the account scopes, a command, a schedule and a timeout. An account scope is an AWS account, an Azure subscription, a Google Cloud project or a Kubernetes cluster, with optional regions. The schedule is from 1 hour to 30 days, or none. The timeout is at most 24 hours, and 2 hours by default. A target names no credential, and its command holds no secret. A namespace has at most 100 targets.
_Avoid_: Scan target, sweep config

**State check**:
A sweep in the server that finds left-behind and managed-twice resources from the resource index (ADR 163). It needs no runner and no cloud credential. It classifies the contested keys of one namespace and the key of each finding that is not resolved. It writes findings with the evidence `state`. It runs one time each day for each namespace with at least one unit, at a time that a hash of the namespace name gives. It also runs after a soft delete, a restore or a purge of a unit, and on request. It is a sweep of the kind `state`.
_Avoid_: State scan, index scan

**Sweep report**:
The JSON Lines file that the command of a cloud sweep writes to the path in `DUSTPAN_SWEEP_REPORT`. The launcher gives that path to the command (ADR 130 decision 4, ADR 134 decision 6). The launcher uploads the report as a stream with `PUT /runner/v1/session/sweep-report`. Only the session key of the claim of the sweep can upload it, and a runner key cannot (ADR 131 decision 3). The upload carries the header `Dustpan-Content-SHA256`, as a log chunk does (ADR 131 decision 10). The upload ends the claim of the sweep, and a sweep has no finish (ADR 131 decision 4). ADR 164 gives the format and the bounds of the report.
_Avoid_: Sweep result, scan output

**Namespace link**:
A record that lets the classification of one namespace read the units of another namespace (ADR 163). Two teams can use one cloud account from two namespaces. An installation administrator with `server:link-namespaces` makes the link. A link goes both ways. It holds the stable IDs of the two namespaces, never their names. Each write appends an audit entry. A soft delete of a namespace stops its links, and a purge of the namespace deletes them. Without a link, the classification of a namespace reads the units of that namespace only.
_Avoid_: Link (alone), namespace trust, federation

**Context**:
The facts that describe an object for people and agents (ADR 160). A namespace, a collection, a unit, a resource of a unit and a blueprint carry context. Each object has five fields: `summary` (text, 2 KiB), `runbook` (Markdown, 64 KiB), `owners` (up to 20), `onCall` (500 bytes) and `links` (up to 50). A link has a title, an `http` or `https` URL and a kind: `docs`, `runbook`, `dashboard`, `repository` or `other`. The docs of an object are its runbook and its links of the kind `docs`. An owner is `group:<name>`, `principal:<ID>` or other text. A unit also has the `readme` output as a field. The read API resolves context along the chain of the object: the resource, the unit, the blueprint, the collection and the namespace (ADR 161). Context is data, never instructions.
_Avoid_: Metadata, annotations, description, label (that is for views and filters), scope (that is the scope of a data key)

**Reserved output**:
The output `opendustpan` of a unit of any tool, which holds the context of the unit and of its resources (ADR 160). It holds a JSON object, or a string with the JSON of one, with `schemaVersion` 1. The top-level fields describe the unit. The field `resources` describes up to 200 resources of the unit by their key in the state. The encoded value is at most 64 KiB. The parser refuses an unknown field. An output that the tool marks as secret gives no context. For one field of one object, the reserved output wins over the API.
_Avoid_: Context output, metadata output

**Context source**:
Where a context entry comes from: `api` or `output` (ADR 160). A person or a client sets `api` context through the context API. A team sets `output` context in the reserved output of a unit. A namespace, a collection and a blueprint have only the API source. A unit and its resources have both. The API refuses to set a field that the output of the same object sets, with 409 `ErrFieldFromCode`. A durable operation projects the outputs after each new latest state item, so a read never parses state.
_Avoid_: Origin, layer (that is a source of a setting value)

**Provenance**:
The object and the context source of one resolved context value (ADR 160). The read API answers each resolved value with its provenance. It also answers each entry of each source as it is. The owners and the on-call of an object that the caller cannot read still resolve. Their provenance then keeps only the source and the kind of the object, not its name (ADR 161).
_Avoid_: Origin, lineage (that is an OpenTofu state history), source (alone)

**Shadowed value**:
An API context value that the reserved output hides, because the output now sets the same field of the same object (ADR 160). The output wins. The read API lists the API value as shadowed, and the console offers to delete it. The API allows a clear of such a field.
_Avoid_: Overridden value, stale value

**Graph**:
The units and the dependency edges between them (ADR 162). A node is a unit, named by namespace, tool and stable unit ID. An edge is a dependency edge that names a dependent unit and was last seen within the dependents window. An edge that names only a principal is not a graph edge. Owners and collections are facts of a node, not edges. A resource is a node only inside its unit. The graph keeps no records of its own: it reads the dependency edges. A query never passes through a unit that the caller cannot read.
_Avoid_: Topology, dependency map

**Focus view**:
The view of the graph that the console shows on the page of a unit (ADR 162). It shows the focus unit and its neighbors one hop upstream and one hop downstream. It shows up to 12 neighbors in each direction by default, and 50 at most, the newest edges first. It folds the other neighbors, and the units one hop beyond a shown neighbor, into their collections. It reads no further than two hops.
_Avoid_: Neighborhood view, full graph

**Fold**:
The part of the focus view that stands for units of one collection that the view does not show one by one (ADR 162). It names the namespace, the collection and the count of units. For each shown unit, it gives the count of edges to the fold. The console shows a fold as a box with a count, and a click on it opens the units of that collection.
_Avoid_: Group (that is a set of principals), cluster, bucket

**Blast radius**:
The units that a walk of the graph reaches in N hops downstream from one unit, to the units that read its outputs (ADR 162). N is 1 to 5, and 2 by default. An answer holds at most 500 units, and 200 by default. A unit that the caller cannot read counts as hidden, and the walk does not follow its edges. The console shows the blast radius before a destroy or a delete, next to the delete check, with the owners of each unit.
_Avoid_: Impact, dependents (that is one hop downstream)

**Resource index**:
A tool-neutral projection with one entry for each managed resource of the latest state item of each unit (ADR 163). An entry holds the unit, the key of the resource in its state, the type and its resource keys. A data source is never an entry. A durable operation replaces the entries of a unit after each new latest state item, and writes only the difference. A unit has at most 100,000 entries. A unit with client-encrypted state has no entries.
_Avoid_: Inventory (that is the list of units), resource catalog

**Resource key**:
The provider and the cloud ID of one resource, which the resource index uses to find one cloud resource in several units (ADR 163). For AWS, it is the ARN. For Azure, it is the resource ID. For Google Cloud, it is the full resource name. Two keys name the same cloud resource when they are equal after `unmanaged.NormalizeKey`. A resource with no such attribute has no resource key. It is not the key of a resource in its state, the URN or the address.
_Avoid_: Resource ID, cloud ID (alone), URN, address

**Holder**:
A unit whose latest state holds a resource key (ADR 163). The holders of a key for a namespace are units of that namespace and of its live linked namespaces. Two resources of one unit are one holder. A soft-deleted unit stays a holder, marked deleted, until its purge.
_Avoid_: Owner (that is a context field), manager

**Contested key**:
A resource key of a namespace with more than one holder (ADR 163). A unit of the namespace holds it, and another distinct live unit of the namespace or of a linked namespace also holds it. A key that only deleted units hold is also contested. A state check reads the contested keys of a namespace in pages of 1,000 (`Index.Contested`).
_Avoid_: Conflict, duplicate key

**Finding**:
The record of one unmanaged resource key in one namespace (`unmanaged.Finding`, ADR 165). It holds the kind, the resource key, the cloud type, the account scope, the region, the name and the tags from the last sweep. It also holds up to 20 units of the namespace that hold the key, the evidence (`state` or `sweep`) and the status. The status is `open`, `ignored`, `pending` or `resolved`, and `unmanaged.Next` decides each transition. A finding is namespace data, and it outlives the units that it names. Its actions are `import`, `write-code`, `delete`, `choose-owner`, `ignore` and `cancel`. The server never deletes a cloud resource.
_Avoid_: Alert, issue, violation, scan result, orphan

**Ignore rule**:
A rule of a namespace that ignores each new finding that it matches, with its reason (`unmanaged.IgnoreRule`, ADR 165). It selects by provider, account scope, type, a tag key with an optional value, and kinds. Only the provider is required. A type can be exact, or a prefix that ends with `*`. A rule can have an end. A rule never ignores a finding that existed before the rule, and its delete opens no finding. A finding names the rule that ignored it. A namespace has at most 500 rules.
_Avoid_: Suppression, filter, allowlist

## Flagged ambiguities

- **Account** is only a label of the hosted console for a namespace (ADR 59). The API, the CLI and error text say "namespace". There is no account object in the server.

- **Group** is a set of principals (ADR 52). RFC 4 used "group" for a set of units before the owner chose "collection". A set of units is a collection.
- **Project** has two meanings. Always write "Pulumi project" or "TFE project".
- **Organization** is the word each tool uses for a namespace. Use it only to name a field or command of a tool's protocol.
- **Lease** and **workspace lock** both stop concurrent writes, but they act differently after a crash. Do not use one word for both.
- **Orphan** means a stored file, for example a blob that no row names. For a cloud resource that no unit manages, or that two units manage, write "unmanaged resource" and name its kind.
- **Catalog** is the self-service gateway only. The list of all units is the inventory.
- **Job** and **operation** are different. A job is work that OpenDustpan asks an executor to do on one unit. Each phase of a job is one durable operation (ADR 100). The operation of a job is `plan`, `apply`, `destroy` or `refresh`. Name which one.
- **Migration** means a SQL schema migration only. For the move of a unit into OpenDustpan, write "import".
- **Restore** has three meanings. A restore of a backup replaces the data of a whole installation (ADR 38). A restore of a version makes one older version of a unit its new current state (ADR 28). A restore of a deleted unit brings the unit back before its purge (ADR 47). Name which one.
- **Lock** names the workspace lock of OpenTofu and the unit lock of the inventory. Code also says "stack lock" for the hold that a mutating Pulumi update has on its stack. Do not call a lease a lock.
- **Delta** and **diff** are different. A delta upload saves a checkpoint. A state diff compares two versions.
- **Version** is ambiguous between a stack's update number, a state version and a software release. Name which one.
- **Break-glass** has three meanings. A break-glass account is a standing account of one named person. A break-glass recovery mode is one of the three ways to recover when the last administrator loses every factor. A break-glass of a job starts a job during an active freeze (ADR 103). Name which one.
- **Sweep** names the Sweep feature only: a cloud sweep or a state check. The task that purges deleted units is the retention expiry. The delete of key blobs with no record is the orphan cleanup (ADR 151). The Go name `inventory.Sweeper` is older than this rule.
- **Source** has two meanings. The term **Source** is the backend of a unit before an import. A source snapshot and the source policy name the code of a commit for a runner job (ADR 135). The unit setting that names that code is the code location (ADR 100). Never write "source" alone for the code.
- **Snapshot** is the source snapshot only (ADR 135). Do not use it for state, a backup, a checkpoint or a state version.
- **Session** has three meanings: the console session of a person, a job session of a launcher (ADR 133), and an MCP session (ADR 123). Name which one.
- **Job token** and **handoff token** are different. A job token is the access token of a tool for the unit of a job (ADR 100). A handoff token opens one job session (ADR 133).
- **Executor** and **launcher** are different. An executor is the way a job runs: write-back or runners (ADR 100). The launcher runs the steps of one claim (ADR 134).
- **Plan** has four meanings: the operation `plan` of a job, the phase kind `plan`, a stored plan, and the OpenTofu plan. Name which one.
- **Claim** has two meanings. The durable claim of a phase gives one executor the right to run it. The job-ID claim is a field of a principal that a job token authenticates (ADR 100). Name which one.
- **Approval**, **confirmation** and **consent** are different. An approval passes an approval gate (ADR 103). A confirmation passes a confirmation gate. A consent allows one secret read of an outside agent (ADR 124).
- **Tool** is an infrastructure-as-code CLI. An agent tool is one of the tools of an agent (ADR 122). Write "agent tool" for the second.
- **Key** has many meanings: a data key, an item key, a resource key, a runner key, a blob key and a tag key. Name which one.
- **Scope** has three meanings: the namespace and unit of a data key, an audit scope, and an account scope of a sweep target. Write "account scope" for the third.
- **Holder** has three meanings: the holder of a lock, the holder of an action, and the holder of a resource key. Name which one.
- **Link** has two meanings: a link in the context of an object, and a namespace link. Write "namespace link" for the second.
- **Layer** is a source of the value of a setting (ADR 63). ADR 161 also says "layer" for one object of the context chain. Name which one.
- **Redaction**, **masking** and **purge** are different. A redaction destroys the content of one history item. Masking hides a secret value in a read and keeps it. A purge ends a whole unit.
- **Retention** has two meanings: the retention period of a deleted unit, and that of a stored plan or a job log (ADR 152). Name which one.
