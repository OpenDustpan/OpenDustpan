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
The short name of a tool, as a label in the console, the API and the docs: "Pulumi" or "OpenTofu". In a claim of compatibility, or where the text means the program, write "the Pulumi CLI". Never write "Pulumi" alone where a reader can think of Pulumi Cloud or of the company.
_Avoid_: "Pulumi CLI" as a label, "Pulumi Cloud" for the Pulumi CLI, "Terraform" for OpenTofu

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

**Import**:
The act of moving the state of a unit from a source into OpenDustpan. It moves history and secrets where the tool can export them. The source stays unchanged. An import is not the Pulumi update of kind `import`. That update records one `pulumi stack import` command. An import is also not the Pulumi command `pulumi import`.
_Avoid_: Migration (it means a SQL schema migration), transfer, onboarding

**Source**:
The backend or product that holds the state of a unit before an import. Examples: a Pulumi service backend, an OpenTofu backend or HCP Terraform.
_Avoid_: Origin, legacy backend

**Backup**:
The copy of the SQL data of a whole installation with the blob references that the data names. A backup in the native format loads only into the same dialect and schema version. A backup in the portable format loads into either dialect (ADR 42).
_Avoid_: Dump, snapshot

**Portable format**:
The dialect-neutral format of the SQL part of a backup: one compressed file of JSON rows for each table (ADR 42). The other format is the native format of the dialect (ADR 30).
_Avoid_: Export format, dump format

**Maintenance mode**:
The state of an installation that answers tool calls with 503 while an operator prepares a restore. The installation API and the probes stay available.
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
A caller whose identity OpenDustpan checked: a person or the owner of an access token.
_Avoid_: User (when the caller can be a machine), actor

**Access token**:
The secret that a tool or the console sends to prove which principal it acts for.
_Avoid_: API key, password

**Action**:
A tool-neutral operation that a principal asks to do, such as read state or write state. Access rules allow or deny actions on namespaces and units.
_Avoid_: Permission, verb, scope

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

**State version**:
One immutable copy of the state of a workspace, with a lineage and a serial.
_Avoid_: Snapshot, revision, checkpoint

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
Encrypted at rest with the data key of a scope. The server seals OpenTofu state and outputs (ADR 41).
_Avoid_: Encrypted (when it can mean the client-side encryption of OpenTofu)

**Secret value**:
A value in state that the tool marks as secret: a Pulumi secret, or an OpenTofu sensitive output or attribute.
_Avoid_: Sensitive value (except for OpenTofu), password

**Masked value**:
A marker that replaces a secret value in a state read. It holds the value path and never the value.
_Avoid_: Redacted value, hidden value

**Value path**:
A JSON Pointer to one value in the state document of one version, as the tool stores it.
_Avoid_: Attribute path (except for OpenTofu), key

**Reveal**:
The call that answers the plain text of one secret value. It needs `unit:read-secrets` and writes an audit entry.
_Avoid_: Decrypt (that is the Pulumi CLI's protocol), unmask, show secrets

**Audit entry**:
An append-only record of one action: the principal, the action, the object, the result and the time.
_Avoid_: Audit log line, event

## Flagged ambiguities

- **Project** has two meanings. Always write "Pulumi project" or "TFE project".
- **Organization** is the word each tool uses for a namespace. Use it only to name a field or command of a tool's protocol.
- **Lease** and **workspace lock** both stop concurrent writes, but they act differently after a crash. Do not use one word for both.
- **Migration** means a SQL schema migration only. For the move of a unit into OpenDustpan, write "import".
- **Version** is ambiguous between a stack's update number, a state version and a software release. Name which one.
