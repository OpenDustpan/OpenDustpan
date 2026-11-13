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
The unit of tenancy that owns units, access rules and secrets, and the tenant of OpenDustpan Cloud (ADR 215). It has its own fence, primary installation, audit scope, backup, blueprints, profiles, OIDC issuer, settings and namespace key (ADR 218). With `identity.principalHome: namespace`, it also has its own principals, groups and sign-in. Each tool calls it an organization, and the hosted console calls it an account. A namespace name is unique in the installation.
_Avoid_: Organization (except when naming a tool's own protocol field), tenant, account, org

**Namespace state**:
The state of a namespace: `active`, `suspended` (read-only, new work refused), `disabled` (31 days after the suspension) or `deleted` (90 days after the suspension). The write path reads it with the fence (ADR 215 decision 16).
_Avoid_: Namespace status, account status

**Namespace ID**:
The stable, opaque identifier of a namespace, with the prefix `ns-`. The server makes it when it creates the namespace, and it never changes. A new namespace with the name of a purged namespace gets a new Namespace ID. So rows that hold the old ID never reach it.
_Avoid_: Namespace key, org ID, UUID

**Namespace switcher**:
The control in the top bar of the hosted console that changes the namespace that the console shows. The URL carries the namespace.

**Account switcher**:
The control of the hosted console that changes the account. It appears only for a person who is a member of several accounts.

**Unit**:
One deployable instance of infrastructure with its own state, in the model of the tool that owns it. A Pulumi stack and an OpenTofu workspace are units.
_Avoid_: Environment, deployment, target
_Copy says_: "stack" or "workspace". Both together: "stacks and workspaces". "Unit" stays in code and in the API. Never use it in copy.

**Unit reference**:
The tool, the namespace and the tool's own name for a unit, written as `<tool>:<namespace>/<name>`, for example `pulumi:acme/web/prod`.
_Avoid_: Unit ID, path, address
_Copy says_: the name of the stack or the workspace, with its tool and namespace. `<tool>:<namespace>/<name>` stays in the API.

**Manual lock**:
A lock that a person puts on one unit, on one collection or on a whole namespace, with a reason of at most 500 characters. While it covers a unit, a lock request of a tool for that unit fails and names the holder and the reason. No new job of the unit starts, and a queued phase does not dispatch. A phase that already runs runs to its end. It covers each unit that is in the collection or in the namespace now. The holder unlocks it, or a person with `unit:manage` on the object force-unlocks it with an audit entry. A tool force-unlock never ends it. The inventory shows it as a flag that is apart from the unit lock (ADR 213 decision 10).
_Avoid_: Freeze (that is a schedule of a namespace), hold, pause
_Copy says_: "manual lock", on a stack, a workspace, a collection or a namespace. Both stacks and workspaces: "stacks and workspaces". The `unit:lock` and `unit:manage` action names stay in the API.

**State**:
The record that a tool keeps of the resources it manages for one unit. OpenDustpan stores state and may read it. A read masks secret values (ADR 41).
_Avoid_: State file (except for OpenTofu), snapshot

**Inventory**:
The list of all units in a namespace across all tools, with their last change and status. Each tool keeps it current with its own writes.
_Avoid_: Catalog, registry, index
_Copy says_: "Inventory" as the console navbar label for the list across all tools (the one short exception). In other copy: "the stacks and workspaces of a namespace".

**Collection**:
A named set of units in one namespace, from any tool, that a team manages. Each unit is in exactly one collection, and collections do not nest (ADR 51). A collection can be the object of a grant.
_Avoid_: Group (that is a set of principals), project, folder, system
_Copy says_: "collection", with the things it holds: "a collection of stacks and workspaces". Do not use "collection" as a plain word for any other set.

**Placement rule**:
A rule of a namespace that matches a Pulumi project pattern and a stack-name pattern. It names the collection of a new stack that the Pulumi CLI makes (ADR 61). The rules of a namespace are an ordered list, and the first rule that matches wins. A pattern is a glob with `*`, never a regular expression.
_Avoid_: Routing rule, assignment rule

**Sharing rule**:
A rule of the producer namespace that lets the jobs of named units read the root outputs of a producer (ADR 273). Its producer is a unit, a collection or a namespace. Its consumer is a unit, a collection, or the namespace of the producer. It can name a unit or a collection of another namespace, never a whole other namespace, and never a principal. A job token reads the outputs of another unit only when two checks pass. A sharing rule names the unit of the job, and the principal whose grants decide holds `unit:read-outputs` on the producer. That principal is one of three (ADR 273 decision 5). It is the principal that started the job, or the last saver of the trigger that started the job. For an open of the profile block `outputs/opendustpan`, it is the attachment authority. A sharing rule gives a person nothing: a person reads outputs with a grant of `unit:read-outputs`.
_Avoid_: Remote state sharing (except when naming the HCP Terraform feature), output grant, share, output-sharing rule (in new text)
_Copy says_: "sharing rule", with "who can read the outputs" where it helps. The `unit:read-outputs` and `unit:manage-sharing` action names stay in the API.

**Producer**:
The unit whose outputs another unit reads, in the language of output sharing (ADR 273). The producer holds its sharing rules and decides who reads.
_Avoid_: Source unit (in new text about output sharing), upstream

**Consumer**:
The unit that reads the outputs of a producer, in the language of output sharing (ADR 273). A sharing rule names it as a unit, as its collection or as its namespace.
_Avoid_: Dependent (that is the side of a dependency edge), downstream, reader

**Output sharing default**:
The namespace setting `outputSharing.newUnitDefault` that decides the sharing rule of a new unit (ADR 273). The value `nobody` is the default and gives a new unit no rule. The value `namespace` gives it one rule that names its namespace. A change of the setting changes no rule of a unit that exists.
_Avoid_: Sharing policy, default visibility

**Label**:
A key and a value on a unit, for views and filters. A label never grants access and never puts a unit into a collection (ADR 51). A label change needs `unit:edit-labels` and writes no state (ADR 223 decision 11). A key has 1 to 128 characters: letters, digits and `.`, `_`, `:`, `/` and `-`. A value has 0 to 256 characters from the same ASCII set: no space and no other character, as a Kubernetes label value. The label filter of the unit list, the labels of a saved view and every label write accept only this form. A Pulumi stack tag outside this form still shows in the list as a label. Text outside labels, such as documentation and the input of a blueprint web form, can hold any Unicode character.
_Avoid_: Tag (except when naming a tool's own field), attribute

**Unit settings**:
The one record of a unit that holds what a job reads at its start. It holds the code location, the executor, the runner pool, the write-back options, the pull request automation switch and the custom steps. A unit has at most one record, kept by the stable unit ID. A namespace has defaults for the runner pool only, and the default Git host connection belongs to the Git host connections. There is no merge by field across levels. A write names the version that the person read. The settings are not kept in the repository.
_Avoid_: Unit config, unit configuration, unit profile

**Effective value**:
The value of one field of the unit settings that a job uses. It comes from the record of the unit, the defaults of the namespace or the built-in values. Each answer of the unit settings API shows it with its origin.
_Avoid_: Resolved value, final value

**Origin of a value**:
The level that gave an effective value: `unit`, `namespace-default` or `built-in-default`. It answers the question why a job uses a value.
_Avoid_: Source of a value, scope of a value

**Unit state**:
One fact about a unit that the inventory shows as a badge: `in-sync`, `running`, `queued`, `locked`, `awaiting-approval`, `apply-needed`, `drifted`, `failed` or `unknown` (ADR 265). A unit has all the states that apply, and a list shows them all, each with a tooltip. No one state wins. The words and the badge rule are an owner decision. The server derives `locked` at read time from the unit lock, which stays the only source (ADR 265 decision 5). The drift statuses are three of the states.
_Avoid_: Unit status (for the set), winning status, health
_Copy says_: the tool's own words on its own pages. "Approval wait" is the human word for `awaiting-approval`.

**Saved view**:
A name for a query of the unit list, stored on the server and read through the REST API (ADR 266). A saved view holds the query and never its results. A reader runs the query again with the access of the reader, so the view shows only units that the reader can read. A saved view grants no access (ADR 51 decision 7). One person in one namespace owns a view, and a view is private first (ADR 266).
_Avoid_: Filter preset, bookmark, smart collection (a collection holds units by explicit membership, ADR 51)

**Unit lock**:
The tool-neutral flag in the inventory that shows who holds a unit for a change. The holder is a mutating Pulumi update that runs, or an OpenTofu workspace lock. It names the holder, the time the holder took it, and the tool's own word for the operation. A unit lock is not a status, so a unit can hold one in any status.
_Avoid_: Lease (for the inventory field), unit status

**Import**:
The act of moving the state of a unit from a source into OpenDustpan. It moves history and secrets where the tool or the source gives them. The source stays unchanged. Two paths exist. The import tool is a resource of a namespace that the server runs (`unitimport.Import`, ADR 241). The CLI path of the guide uses the CLI of each tool (ADR 29). An import is not the Pulumi update of kind `import`. That update records one `pulumi stack import` command. An import is also not the Pulumi command `pulumi import` or an OpenTofu `import` block. It is not the `import` action of an unmanaged resource finding.
_Avoid_: Migration (it means a SQL schema migration), transfer, onboarding

**Source**:
The backend or product that holds the state of a unit before an import. Examples: a Pulumi service backend, an OpenTofu backend or HCP Terraform.
_Avoid_: Origin, legacy backend

**Source kind**:
The protocol that a source adapter speaks: `tfe-api`, `pulumi-service`, `s3`, `opentofu-http` or `upload` (`unitimport.SourceKind`, ADR 243). A source kind names a protocol, not a product. The docs name the products that each kind works with.
_Avoid_: Source type, provider (that is a block of a profile or a plugin of a tool)

**Job source kind**:
Where the start of a job came from: `api`, `agent`, `self-service`, `forge`, `schedule` or `trigger` (`job.SourceKind`). The server sets it from the caller of `job.Service.Start`. A request to the REST API always starts a job with `api`, and no field of the request changes it.
_Avoid_: Job origin, source type

**Upload source**:
The source kind `upload`. A client sends each version of a unit to the import, oldest first (ADR 241 decision 3). It is the way for a source that the server cannot reach or has no adapter for.
_Avoid_: Manual import, push

**Import unit**:
One unit that an import moves, with its source unit, its target name, its state and its ledger (`unitimport.ImportUnit`, ADR 241 decision 7). A failed import unit with committed versions is partial. A release soft-deletes a failed or partial target unit.
_Avoid_: Import item, import task

**Import ledger**:
The rows of an import unit, one for each source version that it committed, with the source digest and the target history item. A resume starts at the first version with no row (ADR 241 decision 9).
_Avoid_: Import log, checkpoint (that is a Pulumi state document)

**Import plan**:
The result of the dry run of an import (ADR 241 decision 8). It gives each unit with its versions, its bytes and its secrets provider. It puts each part of a unit in one class: verbatim, mapped or lost (ADR 242 decision 1). It also gives the carry-over findings that the server can find before the move.
_Avoid_: Plan (alone), preview (alone)

**Import hold**:
The manual lock that an import puts on each target unit (ADR 241 decision 12). It starts at the create of the unit and ends at the release or the cancel of the import. Only the release or the cancel ends it. While it holds a unit, only the import writes state. Each other route that writes state, renames or deletes the unit answers 423. A Pulumi preview can still run (ADR 244 decision 12).
_Avoid_: Import lock, freeze

**Move check**:
The check that compares moved units with their source (`unitimport.Report`, ADR 244). It compares the state digest, the resource count, the secrets, the history and the source head. It needs a plan with no change, and it gives the carry-over findings. Its scope is one unit or a namespace.
_Avoid_: Migration check, verification (alone), validation

**Carry-over finding**:
A finding of a move check or an import plan. It names a part of a unit that the move did not carry over (ADR 244 decision 6). It has a code, a severity and the command that helps. An example is `carry.reference`.
_Avoid_: Breakage, gap

**Installation**:
One OpenDustpan deployment: its SQL store, its blob store and its key-encryption key, with each server process that uses them. The installation API acts on the whole installation. Its routes are under `/api/dustpan/v1/`, with no `server` word in the path, and its actions have the area `installation:` (ADR 210). The authorization and audit object of the installation keeps the name `@server`. Write `@server` only for that object, and "installation" in each other place. An installation owns its namespaces. A namespace holds collections, and a collection holds units (ADR 248).
_Avoid_: Cluster, instance (one server process), deployment
_Copy says_: "installation", explained once: "one OpenDustpan setup, with its database, its blob store and its key-encryption key". For one program, write "the server".

**Instance**:
One server process of an installation. Each start makes a new instance ID, `ins-` and 20 characters, which the start log shows. The instance ID is the subject of the audit entries that the process writes on its own (ADR 63). Each start also writes the version row of the instance: the instance ID, the version of its binary and the start time. A clean stop deletes the row, and an administrator can delete the row of a process that died. The row is not a lease: nothing renews it, and nothing reads it to decide that an instance is live.
_Avoid_: Node, replica (alone), host
_Copy says_: "server process". Never write "instance" in copy.

**Component**:
A part of the server that a process runs: `api` or `worker` (ADR 249). One process runs one or more components, and a start flag selects the components. All components run by default. A component is a setting of one process. A module switch is a setting of the installation.
_Avoid_: Role (ADR 52 uses it for a set of actions), service, mode, duty

**Installation kind**:
The marker of an installation that says which server command may open its data: `dev` or `production` (ADR 237). The first command that starts on a new database claims it, and no API or command changes it after that. `opendustpan serve` claims `production`. An SDK server claims `production` unless it sets another kind. `opendustpan dev` claims `dev`. Each command refuses a database of the other kind. It is not the installation state (`normal` or maintenance mode).
_Avoid_: Mode, purpose, origin

**Dev installation**:
An installation with the installation kind `dev`, which `opendustpan dev` made for a trial or for CI (ADR 237). Its key-encryption key, and the admin token that `dev` makes, live in its data directory. It has no backup, and no command turns it into a production installation. To keep a unit, a user moves it with the export and import commands of its tool.
_Avoid_: Test installation, sandbox, demo installation

**Dev data**:
The people and the rows that `opendustpan dev` makes: the person `dev:admin` and the data that depends on it, such as its grants, sessions and tokens. Its subject starts with `dev:`, in any ASCII letter case. A production installation holds no dev data (ADR 253).
_Avoid_: Test data, demo data

**Strip**:
The step of a restore that deletes the dev data from the staged data before the swap. It runs on every restore into a production installation. The staged data decides, not the installation kind in the manifest (ADR 253).
_Avoid_: Scrub, sanitize, clean

**IP allowlist**:
A list of CIDRs on a namespace. A request that acts on the namespace from outside the list gets 403. The feature is off unless the operator turns it on.
_Avoid_: Whitelist

**Setting**:
One named value that changes how the server works, for example `log-level`. One declaration in `internal/cli/settings.go` gives its flag, its environment variable, its file key and its default (ADR 63).
_Avoid_: Option, parameter, config value

**Settings resource**:
The one API resource that holds each setting of a scope: `/api/dustpan/v1/settings` for the installation, and `/api/dustpan/v1/namespaces/{namespace}/settings` for a namespace (ADR 212 decision 7). It has one revision and one `ETag`, and a `PATCH` is a JSON Merge Patch. A policy route, such as `settings/session-policy`, is a view of it.
_Avoid_: Config API, settings page (that is the console page)

**Idempotency key**:
The value of the `Idempotency-Key` header that a client sends with a request that changes data (ADR 210 decision 6). The server keeps the first answer for the caller, the route and the key for 24 hours. A retry with the same key and body gets the first answer, and the server does the operation once.
_Avoid_: Request ID (that names one request in the logs), dedup key

**Layer**:
A source of the value of a setting: `flag`, `env`, `file`, `database` or `default`, in that order of precedence (ADR 63 decision 1). The layer that wins gives the effective value.
_Avoid_: Level, tier

**Optional module**:
A feature outside the core that has its own routes, its own schedules or both, for example Self-service or Sweep. It has one declared switch. When the switch is off, its routes answer 404 and it runs no schedules. Its data stays (ADR 211). The core is never off: namespaces, identity, authorization, the audit log, secrets, maintenance mode and backups.
_Avoid_: Feature flag, plugin, add-on, module (alone)

**Capabilities document**:
The answer of `GET /api/dustpan/v1/capabilities`: the version of the tool-neutral API and each optional module with its state (ADR 211). `apiVersion` is public, so the console reads it before the sign-in. The module list needs `caller:read`, so the console reads it after the sign-in. It is not the `/api/capabilities` route of the Pulumi Cloud-compatible API.
_Avoid_: Feature list, handshake, capability (alone)

**API description**:
The machine-readable document that lists the operations of the tool-neutral REST API under `/api/dustpan/v1`. The API clients, the CLI and the providers are generated from it (ADR 264). It does not describe the Pulumi Cloud-compatible API or the TFE-compatible API.
_Avoid_: Swagger file, API spec, OpenAPI spec, schema file

**API client**:
A library for one language that calls the tool-neutral REST API. The client generator makes it from the API description. A person writes only its small transport and the message loop of its adapter in the client conformance suite. Each language has its own repository, for example `client-go`. A client release needs a pass of the client conformance suite. "SDK" in ADR 264 and RFC 99 means an API client.
_Avoid_: SDK (for a client, because the SDK of ADR 35 is the server SDK in `pkg/`), binding, wrapper

**Client generator**:
The program of this project that makes each API client from the API description. It has one parser of the OpenAPI subset that the description uses and one template set for each language. The same description always gives the same bytes.
_Avoid_: codegen (alone), SDK generator, OpenAPI generator (that is a third-party tool)

**Client conformance suite**:
The one suite that runs every API client, through a small adapter for each language, against a real server. It covers each operation, the error shape, pagination, `Idempotency-Key`, `If-Match` and `ETag`, and unknown fields and enum values. It is not the conformance suite of a port.
_Avoid_: SDK tests, client e2e

**Forge**:
A git hosting service with an API, for example GitHub, GitLab or Forgejo. The server uses a forge only through the `Forge` port and a forge connection. Internal term. User-facing text names the service (GitHub, GitLab, Forgejo). Where a generic term is needed, it says Git host.
_Avoid_: VCS provider, SCM, VCS (alone). In user-facing text: forge

**Forge connection**:
The credentials that let the server use one forge for a namespace. With them, the server reads files, takes webhooks, posts results and, if allowed, pushes. The server keeps them sealed, not as a setting. The credentials of a token connection, and the key and event secret of a per-namespace GitHub App, are namespace credentials. The key and event secret of the installation-level GitHub App, the default for GitHub, are installation credentials. The operator sets them, and a connection of a namespace holds no credential set of its own. A namespace can have many forge connections, and one of them is the default (ADR 213). User-facing text names the service ("GitHub connection"). Where a generic term is needed, it says Git host connection.
_Avoid_: VCS integration, VCS provider, app (alone)

**Git host account link**:
A row that maps one account of a Git host to one principal. Only an account that a person linked can run a comment command, and the command runs as that principal with its actions. A person makes the link from a console session. The account proves itself with a signed event of the Git host. The key of the account is the kind of the Git host, the host name and the stable ID that the Git host gives. The login of the account is text for a person to read and is never compared. A link never replaces another link silently. In code: `forgelink.Link`.
_Avoid_: Identity mapping, user mapping, forge link (in user-facing text)

**Git host link request**:
The one-time request that makes a Git host account link. A person in a console session starts it and gets a token one time. The server keeps only the SHA-256 of the token. A signed event of the Git host that carries the token proves the account. The person then accepts the account that proved itself. A request is good for 5 minutes and for one accept. In code: `forgelink.Request`.
_Avoid_: Pairing code, invite

**Review request**:
A proposed change on a forge that people review before a merge: a pull request of GitHub or a merge request of GitLab. One change order carries each review request. The review mode of its units decides which approvals count and who merges (ADR 299).
_Avoid_: Pull request, merge request (except when naming the feature of one forge), PR, MR

**Merge state**:
What the Git host says about whether a review request can merge now: clean, conflict, blocked, behind, unstable, draft or unknown. Only clean means that the Git host lets the review request merge. Unknown means that the Git host did not compute the merge yet, or that the server does not know the value. A caller treats each state except clean as "not mergeable".
_Avoid_: Mergeable (alone)

**Test merge commit**:
The commit that the Git host makes to test the merge of an open review request. It is not on a branch. It changes when the base or the head of the review request moves. The server reads it from the Git host and never makes one.
_Avoid_: Preview merge

**Forge capability**:
One group of operations of the `Forge` port that a forge connection can do, for example `commit`, `review` or `repo-token`. A method outside the capabilities of a connection returns `ErrUnsupported`. The server checks the capabilities that a feature needs when a person names the connection for it.
_Avoid_: Feature (alone), scope (that is a token permission of the forge), permission

**Forge bridge**:
A small process that a team runs next to its version control system. The server cannot reach that system, or the system has no forge API. The bridge does the operations of the `Forge` port on that side, and connects out to the server.
_Avoid_: Proxy, relay, gateway, mirror

**Forge event**:
A signed request that a forge sends to the server: a push, a change of a review request, or a comment. The server checks its signature, and reads the object through the API of the forge before it changes a record. So an event is a hint, never a fact. The server takes it at `/webhooks/{hook ID}`.
_Avoid_: Webhook (that is the setting on the forge, or a sink of notifications), callback, hook (alone)

**Comment command**:
A command that a person writes in a comment of a review request, for example `dustpan plan`, `dustpan apply`, `dustpan unlock` or the redact command (RFC 27, ADR 299). The server reacts to each received command with an emoji that the installation sets, and answers it with a reply comment. `dustpan apply` applies the plan of the current head, and refuses when the head moved since the plan. A comment command never approves a change, and credentials never follow a comment alone.
_Avoid_: Chat command, slash command, bot command

**Review mode**:
The setting of a unit that says where people review a change of the unit and who merges (ADR 299). Its values are merged approvals, OpenDustpan merges, no review requests, and no change orders. It has no default. It cascades from the installation to the namespace, the collection and the unit, and a level can force a value. A review request that touches units with different modes uses the strictest mode.
_Avoid_: Review place, integration mode, workflow

**Binding level**:
What an approval binds to (ADR 299): L0 the commit, L1 the set of operations, L2 the exact plan. The default is L1. Each level resets on what it binds to. A parent scope can forbid weaker levels below it.
_Avoid_: Approval level, signature carry-over (the code deletes that setting)

**Set of operations**:
The resource addresses of a plan of one unit, each with its kind of change (ADR 299). A kind is create, update, delete, or one of the two orders of a replace. Each approval records the set of operations of the revision that the approver saw.
_Avoid_: Change set, operation list

**Approval floor**:
A rule of every binding level (ADR 299). A new delete, a new replace or any operation that no approval holds stops the apply. The apply then needs a new approval with a fresh authentication.
_Avoid_: Minimum approval, safety net

**Plan conflict**:
Two plans whose changed resources and dependency closures the server cannot prove disjoint (ADR 299). It keeps the status of the later change order red. It replaces unit locks for review requests. An eligible approver can accept an overlap, with a reason.
_Avoid_: Lock conflict, merge conflict (that is of the code)

**Declared paths**:
The paths of a unit that a person declares in addition to the file closure that the server computes (ADR 299). They only add files. A unit whose closure is unknown stays pending until a person records an acceptance.
_Avoid_: Watch paths, trigger paths

**Plan comment**:
The one comment of the server on a review request, with a summary of the plan and a link (ADR 299). The server edits it in place. By default it holds counts and resource addresses, never a value. The full diff is opt-in.
_Avoid_: Summary comment, plan output

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

**Namespace backup**:
The copy of the rows and blobs of one namespace in the portable format (ADR 218 decision 4). It holds the namespace key records, wrapped. It holds the grants of the namespace, and the principals and groups whose home is the namespace. It writes item key blobs only to a backup target that the `backups` table names, so a download holds no item keys. It restores only into its own namespace.
_Avoid_: Partial backup, namespace export

**Baseline migration**:
The one migration of each dialect that replaced every migration before it, before the first tag. Until the first tag, a change to the schema edits it in place, and the store refuses a database that another version of it made. Its version is 168. The server refuses a database that records a lower version, and a restore refuses a backup with a lower schema version. Both errors name the baseline.
_Avoid_: Squash, initial migration

**Release record**:
The committed file `internal/releaserecord/shipped.txt` that lists each migration file that a release tag shipped, with its SHA-256 (ADR 325). The release adds its lines in the commit that it tags. It is the one fact in the tree that tells whether a tag shipped. While it lists no file, the checks that hold the shipped migrations and the description version are off, and the baseline collapse can run. From its first line on, the checks run and the collapse is refused.
_Avoid_: Release marker, tag file

**Portable format**:
The dialect-neutral format of the SQL part of a backup: one compressed file of JSON rows for each table (ADR 42). It is the only backup format. It replaces the native format of ADR 30.
_Avoid_: Export format, dump format

**Restore**:
The installation API operation that replaces the SQL data of an installation with a backup (ADR 38). The server checks the backup before it changes live data, applies it in maintenance mode, and takes a backup of the live data first. It is not the restore of one older version of a unit (ADR 28). It is also not the restore of a deleted unit (ADR 47).
_Avoid_: Recovery, load, import

**Store check**:
The user-run deep check of a blob store (ADR 281). The server writes throwaway objects under `store-checks/<check ID>/` of the live blob store or of the backup target. It runs the blob conformance cases on them. It measures the latency, the throughput and the delete rate, reports the store capabilities, and deletes the objects. It is a durable operation with a fixed budget. The report says what one run measured on one store. It is not a service level.
_Avoid_: Benchmark, probe, health check

**Control table**:
A table that a restore does not replace (ADR 72, decision 5). The live rows stay across the swap. The installation, backups, restores and durable operation tables are control tables. The claim tokens of durable operations only grow, and an older token from a backup would let a stale holder write.

**Table list**:
The one declaration of every built-in table: its name, class, columns, row order and the tables that a foreign key needs first (ADR 274). Backup, restore, portable backup and dev data read it. It lives in `internal/store/tables`. The format types of a backup stay in `pkg/backup`.
_Avoid_: Table registry (a registry is also the private registry)

**Store runner**:
The part of a SQL dialect that opens a read and a write on its database (`sqlstore.Runner`, ADR 274). It holds the one writer of SQLite and the maintenance gate. It is not a runner of a job (see Runner).
_Avoid_: Runner (alone: a runner is also the process that claims work for a pool)

**Maintenance gate**:
The lock that a write holds while it runs, so that entering maintenance mode waits for each write in flight (ADR 274). Each dialect holds it in its own way. PostgreSQL uses an advisory lock.

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

**Attempt deadline**:
The time that one write attempt has from its start to the commit of its row: 30 minutes (ADR 281 decision 11). The row names the blob of the attempt. The commit compares the start time with the database clock and refuses a later commit with `blob.ErrAttemptExpired`. A sweep of blobs that no row names waits 1 hour, longer than the deadline. In code this time is `blob.UnnamedGracePeriod`. So a sweep never deletes the blob of an attempt that can still commit.
_Avoid_: Timeout (that is a bound on one request), lease

**Artifact sweep**:
The schedule `job.artifact-sweep` that deletes the plan and log blobs of a job that no row names. It runs one time each hour. It waits the grace period of `blob.UnnamedGracePeriod` after the write of a blob, and it deletes nothing while a backup or a restore runs. It is not the **Orphan cleanup** of key blobs.
_Avoid_: Job reaper, garbage collection

**Blob purge record**:
A row that a purge writes for a unit whose blobs may still exist. A save that started before the purge can write a blob after the purge deletes the blobs. The retention expiry deletes the blobs again when the record is older than 24 hours, and then finishes the record. In code this time is `BlobPurgeGrace`. It is not the retention period. It covers the live blob store only. The tombstone of the purge covers the backup targets (ADR 151).
_Avoid_: Tombstone (that is the shred record of a purged unit)

**Maintenance mode**:
The installation state in which the tool APIs answer 503 with `Retry-After`: the Pulumi, TFE-compatible and inventory APIs. The installation API and the probes stay available. The server enters it for a restore after the checks pass. An administrator can also enter it with `POST /api/dustpan/v1/maintenance/enter`. The other state is `normal`.
_Avoid_: Downtime, freeze

**Forge**:
The Git host where people review and approve changes, such as GitHub, Forgejo or GitLab. A source of the repository model may sit on one.
_Avoid_: Provider, platform
_Copy says_: the name of the host: "GitHub" or "Forgejo". Where the text must cover every kind: "Git host". Never write "forge".

**Review request**:
The change that OpenDustpan sends to a forge for approval, as a pull request or a merge request.
_Avoid_: Change request, PR (alone)
_Copy says_: "pull request" for GitHub and Forgejo, "merge request" for GitLab. Where the text must cover both: "pull request or merge request". Never write "review request".

**Write-back**:
The act of committing an edit that OpenDustpan received through its API to the Git repository that holds the source.
_Avoid_: Sync back, push-back
_Copy says_: "commits to <repo> on GitHub", with the repository and the host named.

**Subject**:
The identifier of a principal that is unique on the whole installation. For a principal that signs in through a provider, it holds the provider ID and the identity provider subject.
_Avoid_: User ID, username
_Copy says_: "the ID of the principal". The API field `subject` keeps its name. Explain it once where it first shows.

**Durable operation**:
A piece of background work that the database keeps. If the server process that runs it stops, another process can claim it and finish it (ADR 72).
_Avoid_: Job (alone), task queue item
_Copy says_: "background task". Name the task in plain words, for example "the backup", not the mechanism. Say the one next step.

### Federation

**Installation name**:
The name that an operator gives an installation with `--installation` (`DUSTPAN_INSTALLATION`). It is `local` by default. The namespace row names its primary installation with it. The server never makes it at random, so a restore into a new database keeps it (ADR 248).
_Avoid_: Region, installation ID, server name

**Primary installation**:
The one installation that owns a namespace and accepts its writes. The namespace row names it. A namespace gets the installation that creates it, and only a failover changes it (ADR 248).
_Avoid_: Home region, home installation, home instance, owning installation, active installation, region (of a namespace)

**Replica installation**:
An installation of a federation that holds an asynchronous copy of a namespace and accepts no writes to it. It answers reads from its copy. With request forwarding on, it forwards each write to the primary installation. Without request forwarding, it answers 421 to a write.
_Avoid_: Standby installation, standby region, secondary, follower

**Federation**:
A set of installations that an operator joins for request forwarding, asynchronous replication of namespaces, or both. A federation has no control home, no lease, no write quorum and no consensus protocol. No write waits for another installation, and a network partition does not stop writes. Each installation keeps its own installation scope (ADR 248).
_Avoid_: Cluster, mesh, shard, region

**Federation member**:
One installation in a federation. Federation is opt-in, on a self-hosted installation and on OpenDustpan Cloud. Without request forwarding, each member has its own public URL.
_Avoid_: Shard, cell, pod, node

**Request forwarding**:
The optional federation behavior in which a member sends a request for a namespace to its primary installation and streams the answer back. It makes one hop only. The primary installation checks the token and the authorization again. It refuses a request with a token that another installation made for its own installation principal.
_Avoid_: Proxying (alone), redirect

**Failover**:
The operator action that makes a replica installation the primary installation of a namespace (ADR 248, ADR 286). A **planned failover** first demotes the old primary installation and loses no change. A **forced failover** does not wait for the old primary installation. The server never fails over on its own: an operator starts each failover. The unit of a failover is the namespace.
_Avoid_: Promotion (that is a step along a promotion path), switchover, automatic failover
_Copy says_: "planned failover" and "forced failover". Say that an operator starts it, and that the server never fails over on its own.

**Divergent tail**:
The changes that a former primary installation wrote to a namespace after its last replicated change and before it applied a forced failover. The former primary installation keeps them readable for an export. The server never merges them into the new primary installation.
_Avoid_: Conflict, lost writes, split brain (as a name of the data)

**Failover version**:
A number that increases by one at each failover of a namespace, and at each restore.
_Avoid_: Epoch, generation
_Copy says_: no name. Say the effect and the one next step: "The namespace changed while this write ran. Run the command again." Where a name is needed, write "the number of changes of the namespace by a failover or a restore".

**Fence**:
The namespace and failover version that a write carries. A store rejects the write when the failover version is no longer current.
_Avoid_: Token, guard
_Copy says_: no name. Say the effect, as for the failover version. Never use "fence" as a plain verb.

### Identity

**Principal**:
A caller whose identity OpenDustpan checked: a person, a service or a workload. Each principal has a principal home: the installation or one namespace (ADR 215).
_Avoid_: User (when the caller can be a machine), actor
_Copy says_: "principal", explained once where it first shows: "a person, a service or a workload".

**Principal kind**:
What a principal is: `person`, `service` or `workload` (ADR 52). Each request carries the kind of its principal, from the record that the authenticator reads (ADR 240). The kind is the kind of the principal record. The request also carries its credential: `session`, `access-token`, `job-token` or `trust-token`. A console session and a personal token give `person`. A service token gives `service`. A token from a trust rule (ADR 54) gives `service`. A trust rule names a service principal of its namespace, as a cloud maps a workload identity to a service account. A job token gives the kind of the principal that started the job. The bootstrap admin token gives no kind, and the server counts it as a person (ADR 240). The audit entry records it as `principalKind`.
_Avoid_: Principal type, account type, token kind (a token kind is `personal` or `service`)

**Principal of a namespace**:
A principal whose home is one namespace. It can only hold grants on its namespace or on an object inside it. It can be a member of a group of its namespace only. A service principal of a namespace is made by a holder of `namespace:manage-principals` (ADR 67). With `identity.principalHome: namespace`, a person who signs in through the identity provider of a namespace is a principal of that namespace (ADR 215). A holder of `namespace:manage-principal-tokens` on the namespace makes the tokens of its service principals.
_Avoid_: Namespace service account, local principal

**Principal home**:
The place where a principal, a group or a custom role belongs: the installation or one namespace (ADR 215). The installation setting `identity.principalHome` chooses the home of new people and groups. A grant never crosses homes: a principal of a namespace holds grants only inside its namespace.
_Avoid_: Principal scope, tenant, realm

**Principal source**:
Where a principal comes from. The API makes a `local` principal. The first sign-in of a person through an OpenID Connect provider makes an `oidc` principal, and through a plain OAuth 2.0 provider an `oauth` principal. A directory sync keeps an `ldap` or `scim` principal. The subject of an `oidc` principal is the provider ID and the subject of the identity provider, separated by a colon. So a provider ID must never change after its first sign-in.

**Sign-in provider**:
A configured identity provider through which persons sign in to the console. Its mode is `oidc` or `oauth`, and the adapters of ADR 321 add `saml`, `cas`, `ldap` and `header`. A plain OAuth 2.0 provider identifies a person by a claim of its user-info answer. A provider of the flags or the configuration file has the source `static`, and the API cannot change it. A provider that the API creates has the source `api`. Each provider has an ID that is part of the subject of each person who signs in through it.
_Avoid_: Identity source, IdP connection

**Installation scope**:
The scope of a secret value that belongs to the installation and to no namespace, for example the client secret of a sign-in provider. The server binds its data key to `@server`, a name that no namespace can have.
_Avoid_: Global scope, server namespace
_Copy says_: "the whole installation".

**Access token**:
The secret that a tool or the console sends to prove which principal it acts for.
_Avoid_: API key, password

**Sign-in**:
The step in which a sign-in method, such as an identity provider, proves who a person is. The method only proves the identity. The server then issues its own session.
_Avoid_: Login, log-in (as a noun). The verb is "sign in".

**Sign-in method**:
A way to prove who a person is. It has one of three shapes. A redirect method sends the browser to the identity provider: OIDC, plain OAuth 2.0, SAML and CAS. A password method checks a password at a directory: LDAP. A request method reads the trusted header of a proxy. The local password and the passkey of a local user are sign-in methods too. Each method returns a sign-in assertion. It never makes a principal, a membership or a session (ADR 320).
_Avoid_: Auth provider, authenticator (the `Authenticator` port resolves access tokens), login method

**Sign-in assertion**:
What a sign-in method returns. It holds the provider ID, the subject at the identity provider, the names and the email. It holds the groups, and whether the method sent groups. It holds the authentication time, and whether the method proves it. It also holds the kind of the method and the factors that OpenDustpan checked itself. In code, `session.Assertion` (ADR 320).
_Avoid_: Identity, claims (alone), SAML assertion (that is one input of the SAML method)

**Group claim**:
The list of groups that an identity provider gives for a person at a sign-in. It is a claim of OIDC or OAuth 2.0, or an attribute of SAML or CAS. It is also the groups of an LDAP directory, or a header of a trusted proxy. A group mapping turns each value into groups. One function applies them to the memberships of the person, with the rules for groups from an identity provider. With no group mapping, it joins no group. It changes only the memberships of its own provider, and never makes a group (ADR 320).
_Avoid_: Role claim, team sync

**Group mapping**:
The stored setting of one identity provider that turns a group value into zero or more groups of the home of the person. It is a sensitive setting. The server never infers it from a group name (ADR 320).
_Avoid_: Group link, claim rule

**Proxy proof**:
The second proof that a request of the trusted header comes from the trusted proxy. The first proof is the address of the peer, which must be in the proxy list of the provider. The second proof is a signed JWT of the proxy, a shared secret header, or a TLS client certificate of the proxy. A header from a trusted address alone is never enough (ADR 321).
_Avoid_: Proxy secret (alone), header auth

**Session**:
The proof of a sign-in that the console holds in two sealed cookies: the access cookie and the refresh cookie (ADR 297). The server keeps no record of a session. A session belongs to one person. It ends at a sign-out in that browser and at a bump of the session generation of the person. It also ends after a period with no refresh, and at a maximum length. By default, the period is 15 minutes and the maximum length is 12 hours.
_Avoid_: Login, cookie (the cookies only carry the session), session record (none exists)

**Access cookie**:
The sealed cookie `__Host-dustpan_access` that authenticates each request of a session. It lives for at most 5 minutes. It holds the principal, the sign-in ID and the deadlines of the session. It also holds the session generation, the restriction, and the time of the sign-in, of the last MFA step and of the last fresh authentication. The thumbprint of the refresh key and the CSRF value are in it too (ADR 297).
_Avoid_: Session cookie, JWT, bearer token

**Refresh cookie**:
The sealed cookie `__Secure-dustpan_refresh` that only the refresh route and the sign-out route open. With a refresh proof, it makes a new access cookie and a new refresh cookie. At a sign-out, it ends the runs of its sign-in. It does nothing else (ADR 297).
_Avoid_: Refresh token (for the cookie of a session), remember-me cookie

**Refresh key**:
The ECDSA P-256 key pair that the console makes with WebCrypto at a sign-in. The private key is non-extractable. The refresh cookie holds the thumbprint of the public key (ADR 297).
_Avoid_: Device key, session key (a session key seals cookies)

**Refresh proof**:
The signed statement, in the shape of a DPoP proof (RFC 9449), that the console sends with each refresh. It names the method, the URL, the time and a server nonce, and the refresh key signs it. The server accepts it for 30 seconds from the time of the nonce, and keeps no list of used proofs (ADR 297).
_Avoid_: DPoP token, signature (alone)

**Sign-in ID**:
The random ID, with the prefix `sin-`, that names one sign-in. The cookies of the session and each audit entry of the session carry it. It binds step-up challenges, WebAuthn ceremonies, runs of Dusty and break-glass reviews. It cannot authenticate a request (ADR 297).
_Avoid_: Session ID

**Session generation**:
A count, one for each principal, that starts at 1. Each cookie of a session carries the generation that its issue read, and works only while it equals the stored count. A sign-out everywhere and an end of the sessions by an administrator add 1 to it. A disable and a change of a password or a factor do the same (ADR 297). One port reads and bumps it: `session.PrincipalStateStore`. Each bump writes the audit entry `session:generation-bumped` with its cause.
_Avoid_: Valid-after time, revocation list, token epoch

**Server nonce**:
A value that the session keys seal: the time of the server and random bits. A refresh proof carries one, and the server measures the window of the proof from its time, not from the clock of the browser. The server stores no nonce (ADR 297).
_Avoid_: DPoP nonce (in code, `DPoP-Nonce` is the header name only)

**Session key**:
A random 256-bit key version, with the prefix `sk-`, that seals the cookies of sessions. The key-encryption key wraps each version. A backup holds none, so a restore makes new session keys and ends every session (ADR 297).
_Avoid_: Cookie secret, signing key

**Session policy**:
The administrator settings of the console sessions for the installation. They are the period with no refresh after which a session ends, the maximum length and the rotation period of the session keys. Only a site administrator can change them.

**Local user**:
A person principal that signs in to the console with no identity provider. It signs in with a user name and a password, with a passkey, or with both. One local user record and the passkeys of the person name the same principal, so both credentials sign in the same person. The provider of its sessions is `local`.
_Avoid_: Local account, built-in user, basic auth user

**Passkey**:
A WebAuthn credential of a person. For a local user it is a sign-in of its own, with user verification. After a password it is a second factor.
_Avoid_: Security key (a security key is one kind of authenticator that holds a passkey), FIDO key

**Pending sign-in**:
The state after a first method, such as a password, and before the second factor. It holds no rights, lives 300 seconds, and becomes void after 5 wrong factors.
_Avoid_: Half session, partial login

**Factor lockout**:
The lock of the second factors of a person after 10 wrong factors in a row, across every pending sign-in, for 30 minutes. A good password does not end it.
_Avoid_: MFA lock, TOTP lock

**Recovery code**:
A one-use code for a sign-in when a person lost each factor. The server shows a set of 10 one time and stores only their digests.
_Avoid_: Backup code

**Reset code**:
A one-use code that an administrator makes for a local user, to set up or reset the password. The server shows it once to the administrator and stores only its digest. It ends after 1 hour by default.
_Avoid_: Temporary password, reset token

**Lockout by name**:
The lock of a user name after wrong passwords in a row. 10 from one source prefix lock the name from that prefix for 30 minutes. 100 from every source lock the name for every source for 30 minutes, doubling for each new lock up to 24 hours. Every instance shares the counts, and an unknown name locks the same as a known one. A passkey sign-in never reads the lock.
_Avoid_: Account lock (the principal stays enabled)

**Demo user**:
The person `dev:demo` that `opendustpan dev --demo` makes. At each start the server makes a new password for it, keeps the password in memory only, and shows it on the sign-in page. It exists only on an installation of the kind dev, and a person cannot change its credentials.
_Avoid_: Guest user, test user

**Personal token**:
An access token that belongs to a person. A person creates it after a sign-in, and it has an expiry (ADR 53).
_Avoid_: PAT, user token

**Token rotation**:
A call that issues a new access token for the same principal and sets an end time for the old one. The old token works until that end time (ADR 53).

**Token policy**:
The administrator settings of the access token life cycle for the installation: the maximum lifetime of a token and the inactivity disable (ADR 53).

**Inactivity disable**:
The job that disables an access token or a user account after a set period with no use. The default period is 90 days. An administrator can change the period or turn the job off (ADR 53).

**Temporary administrator**:
A principal of the kind person that the host command makes for one named person after every administrator is lost. It works until a fixed expiry: at most 24 hours, or the token policy maximum when it is shorter. Then the server disables it. The API marks it with a flag, so the console shows a banner for it. Each step writes an audit entry: the create with its reason, each sign-in, and the expiry. A second host run for the same person is refused: the name must name no principal that exists. It is not a break-glass account: it has no `break_glass` flag, and it cannot sign in through an identity provider (ADR 245).
_Avoid_: Shared admin account, emergency account

**Host recovery**:
The recovery path through the host command `host create-temp-admin`: the operator makes a temporary administrator with a reason, straight through the database. The inactivity disable never ends a temporary administrator that still works, and only the fixed expiry ends it. The command refuses when the recovery mode is `none` or when `DUSTPAN_DISABLE_HOST_RECOVERY` turns it off (ADR 245).
_Avoid_: Break-glass, backdoor

**Service token**:
An access token that belongs to a principal that is not a person, for a machine that cannot sign in to the console. Workload identity is the first choice for machines (ADR 54).
_Avoid_: API key, robot token, machine token

**Trust rule**:
A rule in one namespace that matches a JWT from an issuer or a TLS client certificate. It names the principal that the match gets: a service principal of that namespace (ADR 54).
_Avoid_: Trust policy, federation rule

**Token exchange**:
A request of a workload to a namespace. The workload sends a JWT of its issuer. It gets an access token of the kind `trust` for the principal of the one trust rule that accepts the JWT. The access token lives the exchange lifetime of the token policy, one hour by default (ADR 54, ADR 69).
_Avoid_: Token swap, federation login, OIDC login

**Group**:
A named set of principals of one principal home: the installation or one namespace. A group of a namespace holds principals of that namespace only. A principal can be a member of more than one group of its home (ADR 52, ADR 215).
_Avoid_: Team (except when naming a tool's own protocol field), collection

**Role**:
A named set of actions (ADR 52). A built-in role has a fixed name. An administrator makes a custom role from actions.
_Avoid_: Permission set, policy

**Action family**:
One of four sets of actions that do not overlap: operator, administration, data and delegation. A role on the installation can hold actions of each family. A role on a namespace, a collection or a unit holds data actions only. A role on a service principal holds delegation actions only.
_Avoid_: Permission category, scope
_Copy says_: "kind of action". Never use "scope" for it.

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
_Copy says_: "what the principal can do", with the actions named. Never use "reach" as a plain verb.

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

**Last release**:
The record of the last released lock of a workspace. It holds the lock ID, the holder, the releaser, the time of the release and the kind of release. The kind is "normal" when the holder released the lock, and "forced" after a force-unlock. For 24 hours after the release, the record lets an unlock or a force-unlock that OpenTofu sends again succeed with no change.
_Avoid_: Unlock history, lock log

**Override switch**:
A setting of a namespace or of a unit that allows an override (ADR 44, decision 6). Both settings are off by default. A unit setting only narrows the setting of its namespace.
_Avoid_: Flag, toggle
_Copy says_: "forced-write switch" and "unlocked-write switch". Say where to turn each on. "Override switch" stays in code and in the API.

**Forced write**:
A state write that skips the lineage and serial checks, as `tofu state push -force` does. It needs the action `unit:write-forced` and both override switches on.
_Avoid_: Force push, override (alone)
_Copy says_: "forced write", with the permission (`unit:write-forced`) after the plain words.

**Unlocked write**:
A state write to a workspace that no principal holds, as `tofu apply -lock=false` does. It needs the action `unit:write-unlocked` and both override switches on.
_Avoid_: Lockless write
_Copy says_: "write without the lock", with the permission (`unit:write-unlocked`) after the plain words.

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
_Copy says_: "the secrets of one stack or workspace". Never use "scope" as a plain word for range or limit.

**Data key**:
The random key that encrypts all secret values of one scope. OpenDustpan stores it only in encrypted form.
_Avoid_: DEK, secret key

**Key-encryption key**:
The key, kept outside the OpenDustpan database, at the top of the key hierarchy. It wraps each namespace key version, the data keys of the installation scope and the sign-in secrets of each namespace (ADR 217). It is a key file or a KMS key of the operator.
_Avoid_: Master key, root key, KEK

**Customer key**:
A key in the key management service (KMS) of a customer that wraps the namespace key versions of one namespace (ADR 217). The server never holds it. It is optional. When the customer disables it, the sealed content of the namespace cannot be opened, and the key state of the namespace is `revoked`.
_Avoid_: BYOK key, CMK, master key

**Namespace key**:
The random 256-bit key of one namespace, with the prefix `nk-` and numbered versions. The customer key, when the namespace has one, and the key-encryption key wrap each version. The newest version wraps new data keys and item keys. Older versions only unwrap until a rewrap moves each key to the newest version (ADR 217).
_Avoid_: Namespace data key (a data key has a scope), namespace KEK

**Key state**:
The state of the namespace key of a namespace: `active`, `unavailable` (the KMS cannot answer) or `revoked` (the customer disabled its key). The server keeps it in SQL and reads it at each open of a key (ADR 217).
_Avoid_: Key status, KMS state

**Sealed**:
Encrypted at rest with the data key of a scope, or with the item key of a history item. The server seals each checkpoint blob, event payload and journal batch of a Pulumi update with the item key of the update (ADR 151). It seals the state blob and the outputs document of an OpenTofu state version with the item key of the state version. The part of the server that seals and opens content is the sealer.
_Avoid_: Encrypted (when it can mean the client-side encryption of OpenTofu)
_Copy says_: "encrypted by the server". Do not use "sealed" in copy. It means locked in Vault.
The _Avoid_ rule ("Encrypted") is for names in code. The _Copy says_ rule ("encrypted by the server") is for text that users read.

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
A request from a person to see the plain text of a secret value. The reveal call answers the plain value at one masked value path of one version (ADR 41, decision 4). It needs `unit:read` and `unit:read-secrets`. Its routes are `POST /api/stacks/{namespace}/{project}/{stack}/state/{version}/reveal` in the Pulumi Cloud-compatible API and `POST /api/v2/state-versions/{state_version_id}/actions/reveal` in the TFE-compatible API. The call writes an audit entry of the kind `reveal` before it answers. A state download by a person also writes a `reveal` entry, with the value path of the whole state (ADR 240).
_Avoid_: Decrypt (that is the Pulumi CLI's protocol), unmask, show secrets

**Audit entry**:
An append-only record of one action in a namespace. It holds who acted, the action, the object and the time. It also holds where the request came from, the kind of access and the outcome. The chain of ADR 60, decision 17 is opt-in. When it is on, each entry holds the SHA-256 of its own fields and of the previous entry of its scope.
_Avoid_: Audit log line, event, audit event

**Audit log**:
The port in `pkg/audit` that keeps the audit entries of each namespace and of the installation (scope `@server`). The SQL table is its record. It is fully on or fully off for an installation (ADR 60).
_Avoid_: Audit trail, activity log

**Audit checkpoint**:
An Ed25519 signature over the hash at one sequence of one audit scope. It holds the scope, the sequence, the time and the key ID. The server keeps it in the blob store under `audit-checkpoints/`. Each write attempt stores a new copy under a key with a random attempt part, and the check reads every copy of a sequence. The database never decides which copy counts, and no sweep deletes a copy. It is opt-in and needs the chain on. It is not a **Checkpoint** of the Pulumi CLI: in code and docs outside the audit log, always write "audit checkpoint".
_Avoid_: Signed head, anchor, checkpoint (alone, outside the audit log)

**Log sink**:
An output adapter of the port `pkg/logsink` that delivers structured records to one target: stdout, a file, syslog, a webhook, OTLP logs or object storage. The audit log and the application log use the same port (ADR 60).
_Avoid_: Log target, log destination, exporter
_Copy says_: "log output", with the target: a file, syslog or a webhook. Never write "sink" in copy.

**Notification sink**:
A destination of the notifications of a namespace: an email address, a chat webhook or a signed webhook. The code type is `notify.Sink`, and the API path is `/sinks`. A sink has a kind (`email`, `chat` or `webhook`), a name and a target. Its secrets are secret items of the namespace. They are the signing key of a webhook and the URL of a chat webhook. A sink keeps its kind and its platform. A change of either is a new sink.
_Avoid_: Notification target, notification channel
_Copy says_: "notification destination", and for a chat webhook "Slack channel" or the same pattern for another platform. Never write "sink" in copy.

**Signing key**:
The secret of a webhook sink. The server makes it when a person creates the sink: 32 random bytes, written as 64 hex digits. The server answers it once, in the answer of the create. It signs each request to the webhook with the key (HMAC-SHA256). The receiver checks the signature with the same key. No route answers the key again.
_Avoid_: Webhook secret, signing secret (the code uses "signing secret" for the value of a key)

**Test message**:
The one fixed message that the test route of a sink sends to the sink. It has the notification type `sink.test` and says that no event happened. The answer of the route gives the class of the result and a fixed text. It never gives the URL or the answer of the receiver.
_Avoid_: Ping, health check

**Chat platform**:
A chat service that OpenDustpan posts messages to: Slack, Microsoft Teams, Mattermost or Matrix (ADR 262). The code type is `chat.Platform`, and the wire value is the lower-case name in the field `platform`. `chatops.Request.Platform` takes this type later.
_Avoid_: Tool (for a chat service, because a tool is an infrastructure-as-code CLI), provider, integration

**Chat sink**:
A notification sink of the kind `chat`. It posts a message to one chat platform. The field `platform` names it, and the person who creates the sink gives it. The first form posts to an incoming webhook URL, which is a secret item of the scope. A read of the sink shows its platform, its name and the host of the URL, and whether a URL exists. One sender serves every chat sink, and a renderer for each platform builds the request (RFC 98).
_Avoid_: Chat connection (for a webhook sink)
_Copy says_: "Slack channel" or "Slack webhook" for a sink that posts to a webhook URL, and the same pattern for the other platforms. "Slack connection" stays the word for the later connection to a Slack app. Never write "sink" in copy.

**Kind of access**:
The field of an audit entry that tells kinds of access apart. It has exactly one value from a fixed list. `tool-operation`: a tool needed secrets for its own work, for example a Pulumi decrypt call or an OpenTofu state download. `reveal`: a person asked to see a secret value. The principal kind decides the kind of a state download, never the declared channel (ADR 240). A download with a session or an access token of a principal of the kind `person` writes `reveal`. This includes `tofu state pull` with a personal token or the bootstrap admin token. A download with a service token, a token from a trust rule or a job token writes `tool-operation`. The Pulumi routes `decrypt/log-decryption` and `decrypt/log-batch-decryption` are routes, not kinds. Each records an entry of the kind `reveal`, because the Pulumi CLI calls them when a person asks it to show secrets. ADR 44 adds `override`: a person used an override. ADR 60 adds `change` (an action that changes something) and `audit-read` (a read or an export of the audit log). ADR 52 adds `read-access`: a read of the access-review export of an object.
_Avoid_: Audit type, `log-decryption` as a kind

**Value count**:
The count of secret values that one Pulumi `decrypt` or `batch-decrypt` call gave the tool. Only the `tool-operation` entries of these two routes hold it.
_Avoid_: Secret count

### Hosted service

**OpenDustpan Cloud**:
The hosted service that the project runs. It is the build of the server and the console with shims for sign-up, onboarding and billing, hosted defaults and limits, and console labels. It is public source and has no feature that the open-source server lacks (ADR 59, ADR 77).
_Avoid_: Fork, enterprise edition, hosted distribution, cloud edition (as an edition with its own features)

**Storage limit**:
The fair-use limit on the stored bytes of a namespace (ADR 220 decision 13). Its setting is `limits.storageBytes`. At the limit, the server refuses the start of new work, never work in progress.
_Avoid_: Quota (the self-service quota is another concept), storage quota

### Resources and self-service

**Unmanaged resource**:
A cloud resource that does not have exactly one unit that manages it. It has three kinds. **Not in any unit**: the state of no unit holds the resource, and only a cloud sweep can find it. **Left behind**: only deleted units hold its resource key. **Managed twice**: two or more live units hold its resource key, from one tool or from both (ADR 163).
_Avoid_: Orphan (that is a stored file), ghost resource, shadow resource

**Drift**:
A difference between the state of a unit and the real resources that the state names. A change outside the tool causes it, for example a manual edit in a cloud console. A cloud resource that no unit manages is not drift. It is an unmanaged resource (ADR 163).
_Avoid_: Out-of-band change (except to name a cause), configuration drift

**Drift check**:
A job with the operation `refresh` and one phase, `plan`, that finds drift (ADR 263). It changes no resource and writes no state version. OpenTofu runs a refresh-only plan and the Pulumi CLI runs a refresh preview. It starts on its own only when a drift schedule is on, and a person can start it at any time. Remediation is a separate job (ADR 258).
_Avoid_: Drift run (older ADRs only), drift scan (a scan is a Sweep), health assessment
_Copy says_: "drift check" for both tools. On the page of one tool, that tool's own words: "refresh-only plan" for OpenTofu and "refresh preview" for the Pulumi CLI.

**Drift result**:
What one finished drift check reports: the unit, the job and the time. It also lists the address of each resource that differs. It holds no attribute value. A unit keeps its latest drift result. The **drift status** follows from it: `drifted`, `in-sync` or `unknown`. The status `drifted` is one badge of the unit among others.
_Avoid_: Drift report, drift diff, drift state (a state is the record of a tool)

**Drift schedule**:
The setting that turns on drift checks. A unit, a collection or a namespace has it. It is off by default. The frequency is a preset or a custom cron expression. The most specific setting wins, in the order unit, collection, namespace. With no setting, no drift check starts on its own (ADR 263).
_Avoid_: Drift policy (a policy alone names no object), drift trigger

**Auto-dedrift**:
The automatic remediation of a drift. The server starts a job that brings a drifted unit back to its code. A **drift result** starts this job. It is not the **drift check**. It is available and opt-in, and it is off by default. A namespace can turn it off. The opt-out of a namespace always wins over an opt-in at another object. The job meets the same gates, approvals, freezes and access limits as a job that a person starts. The audit log records it as automatic (ADR 358).
_Avoid_: Auto-remediation, self-healing, auto-fix

**Trigger**:
A rule of one unit that starts a plan of that unit on its own (ADR 267). It has one kind: `review-request-push`, `schedule` or `follows`. A trigger starts a plan and nothing else. The job passes through the job service, so the gates, the approval rules, the freezes and the manual locks apply. A trigger has no pool: the job takes the runner pool of the unit settings.
_Avoid_: Run trigger (except for the TFE-compatible object of that name), webhook (an inbound webhook is another idea), automation, hook
_Copy says_: "trigger". On the page of one tool, that tool's own words: "plan" for OpenTofu and "preview" for the Pulumi CLI.

**Last saver**:
The person who saved a trigger last. The server sets it from the sign-in of the caller at each save, and a client never sends it. Each start that a trigger makes checks that the last saver still holds the actions of a start. The read of an output by a job that a trigger started uses the current grants of the last saver. A last saver whose principal is missing, deleted or disabled gets nothing. A trigger whose last saver lost access needs a new save.
_Avoid_: Owner of the trigger, creator
_Copy says_: "last saver".

**Namespace service principal**:
The built-in principal of a namespace for a start that no person makes. Its subject is `system:namespace-service:<namespace>`. It holds `unit:run-plan` on the units of its own namespace and no other action. It has no token and no session, and no grant changes what it holds. It appears as the subject in the audit entry of a start by a trigger.
_Avoid_: Trigger user, robot, bot account
_Copy says_: "namespace service principal".

**Follows trigger**:
A trigger of the kind `follows`. It names a source unit. After the source applies, a plan of the unit of the trigger starts. A unit of one tool can follow a unit of another tool. The server refuses a cycle of follows triggers when a person saves one. A follows trigger is a declared dependency edge (ADR 153).
_Avoid_: Downstream run, chained run, pipeline, dependency (an edge that a read of outputs makes is a dependency, ADR 153)

**Registry release**:
One published version of an OpenTofu provider or module in a namespace, with its archives, their SHA-256 hashes and the record of who published it. It never changes after the publish (RFC 101). A module release holds one archive and no signature.
_Avoid_: Package, artifact (that is a plan or a log of a job)

**Registry address**:
The name of a provider or module in a namespace: the namespace, the kind and the name, with the system for a module. The host of the address is the host of the server (RFC 101).
_Avoid_: Source address (that is the field of OpenTofu), package name

**Trusted signing key**:
A public OpenPGP key that a namespace registers for its registry. A provider release is published only when a trusted signing key of the namespace made the signature of its checksums file. The server takes the fingerprint from the key. If a namespace deletes the key, the registry stops serving the releases that it signed.
_Avoid_: Signing certificate, GPG key (the format is OpenPGP), trusted publisher

**Staged file**:
A file of an upload that the server holds under a staging key until a publish moves it. It is not readable through the registry. A publish measures it again, checks it, and copies it to its content key. A staged file that no publish moved is deleted after 24 hours.
_Avoid_: Temporary file, pending upload

**Self-service**:
The request gateway of OpenDustpan, with its routes under `/api/dustpan/v1/self-service/` (ADR 210, ADR 286). A principal sends a self-service request to it, for example for a unit from a blueprint. Write it with a capital S as the name of the feature and the menu item. The private registry is a separate feature with its own menu item, **Registry**.
_Avoid_: Catalog (as the name of the feature), service catalog, marketplace, portal, inventory, registry (that is the private registry)

**Self-service request**:
A declarative JSON document that a principal sends to Self-service. It states the result that the principal wants, not the steps to get it.
_Avoid_: Catalog request, order, ticket, form

**Blueprint**:
A program that a platform team offers for self-service, with an input schema, platform values and a version. Units made from it are normal stacks or workspaces.
_Avoid_: Template (Pulumi templates copy code), no-code module, product, offering, golden path

**Promotion path**:
An ordered list of 2 to 10 units of one namespace, from dev to prod, along which a change moves (RFC 55, ADR 286). A unit is in at most one promotion path.
_Avoid_: Lane, environment (that is a profile of the Pulumi CLI), pipeline, stage

**Promote**:
To move a change one step along a promotion path. The change copies the blueprint version and the marked answers of one unit to the next unit of the path, or the applied commit. It is a normal change of the next unit, with all its gates. One such change is a **promotion**. The server never promotes on its own.
_Avoid_: Advance, deploy, sync
_Copy says_: "promote" for a step along a promotion path only. For an installation, write "failover". For a signing key, write "activate".

**Comparison**:
The side-by-side read of two units, for example two steps of a promotion path (RFC 55). It stores nothing. The server marks each part that the caller cannot read `hidden`.
_Avoid_: Diff (a state diff compares two versions of one unit), delta

### Jobs and runners

**Job**:
Work that OpenDustpan asks an executor to do on one unit (ADR 100 decision 19). Its unit is a unit reference with the stable unit ID set, so a rename of the unit keeps its jobs. A change to many units is many jobs. A job has one of four tool-neutral operations: `plan`, `apply`, `destroy` or `refresh`. Each tool maps them to its own commands, and user-facing text keeps the words of the tool. A job is a list of phases. The server fixes the spec of a job at start. A later change of the unit settings or of the branch does not change it.
_Avoid_: Task, deployment, run (without a tool), operation (alone: name the operation of the job or the durable operation)

**Job purpose**:
The field `purpose` of the job spec. It says why a job runs when its operation alone does not say. `drift-check` is the only value. The server fixes it at start, and it selects the phase list of the operation: a drift check has the one phase `plan`. A job with no purpose has the phases of its operation. The purpose never changes what the policy sees: the policy gets the spec with the purpose.
_Avoid_: Job type, job kind (a kind names a phase or a durable operation)

**Executor**:
The way a job runs: write-back or runners (ADR 100 decision 19). The port `job.Executor` names the kind, `write-back` or `runner`, and gives the phases of a spec (ADR 101 decision 1). It also undoes what a job left open after a cancel, a rejection or an expiry. Both executors call the same progress ports, `job.Claimer` and `job.Recorder`. So both meet the same rules for claims, cancel, steps, logs and plans.
_Avoid_: Launcher (that is the program of a runner that starts each step)

**Phase**:
The work that one executor does with one claim (ADR 100 decision 4). A phase has one of four kinds. `plan` holds runner steps that change nothing, and the stored plan. `apply` holds runner steps that change resources or write state. `deliver` is the commit of a write-back job. `verify` comes after the apply phase of a runner apply job and of a runner destroy job. It holds runner steps that change nothing and check the change (ADR 148 decision 3). A job has a `verify` phase only when the unit settings name its steps. The executor gives the phases at start, and they do not change after. Each phase is one durable operation with the start key `<job ID>/<phase index>`. A takeover is a new claim on the same operation. Between two phases, no executor holds the job, so a wait for a person never keeps a runner busy.
_Avoid_: Step (a step is one argument list that a runner starts in a phase), stage

**Gate**:
A stored rule at one decision point (ADR 224). Its condition is the body of one Cedar policy over the fact document of the decision point. Its outcome is data: `deny`, `approval:<rule>` or `warn`. Its mode is `enforce` or `evaluate`. Its `onError` is `fail` (the default) or `skip`, and an error of a gate with `fail` stops the change. A gate ID is unique in its scope, and its Cedar policy ID is `<scope key>/<id>`. A gate can be final (see Final gate). A gate has a scope: the installation, a namespace, a collection or a unit. The gates of a unit key on its stable unit ID, not on its name (ADR 317). A rename and a move between collections keep them. A new unit with the name of a deleted unit has none. A blueprint version or a profile can also hold gates. Any `deny` wins. Else the change waits for each named approval rule. Else each `warn` gives a warning, and a warning stops auto-apply. Else the change goes on. Any Cedar error of a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`. A gate is stored in the server and configured through the API, never in a repository file. A lock, a freeze, a switch and the agent controls are fixed controls, not gates.
_Avoid_: Policy (alone), check, approval gate (the requirements of a change come from approval rules), guardrail

**Approval wait**:
The state of a change that waits for the approvals of its requirements (ADR 225 decision 8). The change is a job, a change order, a profile draft or an access request. The job state value is `awaiting-approval`. A phase dispatches only when each requirement has its count of signatures. The signatures come from distinct people. A rejection by an eligible approver of any requirement rejects the change. A change that waits longer than 7 days expires, and an approval rule can set its own timeout of 1 hour to 30 days. For a write-back job, the wait comes before the commit. The review request then still needs the approvals of the forge (ADR 103 decision 3).
_Avoid_: Gate (that is the stored rule), pending approval

**Confirmation step**:
A fixed control before each phase that changes, for a job of an agent in approve-first mode or with no mode (ADR 103 decision 2). It is not a gate: no stored rule gives it or takes it away. A write-back job of an agent has it in front of its deliver phase in every mode (ADR 128 decision 4). Only the person who asked the agent can pass it. That person gives one confirmation in a request of their own with no agent. The request comes from an interactive console session, never from an access token (ADR 128 decision 5). It has a fresh authentication. That is a new MFA step where built-in MFA covers the person. Otherwise, it is a new authentication at the SSO identity provider of the person. Its maximum age is the fresh-authentication window (`freshAuthSeconds`, ADR 227). A confirmation with no fresh authentication fails with `ErrAuthStale`. The confirmation binds to the plan when a plan phase comes before. A gate never gives it and never takes it away. `job.Service.Confirm` records it and needs the start action of the job, not `job:approve`. An agent cannot give a confirmation (ADR 103 decision 7). ADR 222 decision 8 gives the name. ADRs 103 and 128 call it the confirmation gate.
_Avoid_: Confirmation gate (older name), approval (an approval fills a requirement of an approval rule), consent

**Request-changes thread**:
A thread on a job that a blocking comment opens (ADR 128 decision 11). A reviewer with `job:approve` opens it, or an agent of such a reviewer. The person who started the job cannot open one on a job that they started. While a thread is open, the job model accepts no approve verdict and no confirmation of the job (`job.ErrChangesRequested`). The person who opened the thread, or an approver who may dismiss, resolves it, each in a request with no agent. An approver who may dismiss can record an approve verdict on a requirement of the job. The state of the requirement does not matter. That needs `job:approve` and the approvers of its approval rule. That approver is never the person who started the job, and never an agent. The person who started the job never resolves it, and an agent never resolves it. An agent never rejects a job: a blocking comment is the way for an agent to stop one. A change order has its own request-changes threads (ADR 193). A job with a change order origin gets no job thread (ADR 190 decision 16).
_Avoid_: rejection (a rejection ends the job), review (alone: that is the review request of the forge)

**Dismissal**:
The resolve of a request-changes thread by a person other than the person who opened it (ADR 128 decision 14). A dismissal needs a reason, and writes an audit entry with that reason.
_Avoid_: Override (that is a break-glass or a forced write)

**Agent mode**:
The mode that a user sets for the agent that acts for them: approve-first, review-request or direct (ADR 103 decision 12). `job.CheckAgent` holds the rule at start. Approve-first is the default: each phase that changes gets a confirmation step. In review-request mode, the agent changes a unit only through a write-back job that delivers as a review request. Its deliver phase still has a confirmation step (ADR 128 decision 4). In direct mode, a runner job of the agent changes a unit with no confirmation. A write-back job pushes after the confirmation of its delivery. Direct mode needs three switches, each off by default: one of the installation, one of the namespace of the unit and one of the user. A runner job that only plans needs no mode, and it starts in every mode.
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

**Plan reserve**:
The first of the two writes that store a plan (`job.RecordStore.ReservePlan`). In one transaction it records the plan row and the record of its item key. The plan has no part and nobody can read it. The parts seal after the reserve, because the sealer reads the committed record of the key. The second write, `PutPlan`, adds the parts. A plan reserve that nobody fills stays as a row with no part, and the retention sweep deletes it.
_Avoid_: Draft plan, pending plan

**Chunk reserve and `confirm`**:
The two writes around the blob of a log chunk (`ReserveLogChunk` and `ConfirmLogChunk`). The reserve records the sequence number, the SHA-256 and the size of the chunk before its blob exists. The second write counts the chunk in the log after the blob is stored. A crash between the two leaves no counted chunk without a blob. A retry with the same sequence number and the same hash changes nothing. Each write attempt stores its blob under a key of its own, `<sequence number>-<attempt ID>`, and the second write records that key in the row. The attempt whose second write commits first wins. An attempt that loses deletes only its own blob. Another hash for a reserved sequence number is a conflict.
_Avoid_: Two-phase commit (the blob store is not part of a transaction), staging

**Step key**:
The name of the record of one step under one claim (`job.StepKey`). It has three parts: the phase of the job, the token of the claim and the name of the step. A holder with another claim token has another key. So the records of two holders of one phase stay apart.
_Avoid_: Step ID

**Log version**:
One numbered copy of the log of a step, with an ID that has the prefix `jlv-`. A step has one log version at first. A log replacement makes a new version and keeps the old one until its retention ends. One item key seals all chunks of one log version.
_Avoid_: Revision, log copy

**Log chunk**:
One part of a log version, up to `job.MaxLogChunk` bytes, with a sequence number that starts at 0. Each chunk is one sealed blob. A reader joins the chunks in the order of their sequence numbers. A closed log version has the SHA-256 and the size of its whole plaintext, which the reader checks at the end.
_Avoid_: Log line, log page, segment

**Break-glass (job)**:
A reason in a start request that lets a job start during an active freeze (ADR 103 decision 6). Without it, the start fails with `ErrFrozen`. It needs `namespace:break-freeze` on the namespace. That action is in another family than `namespace:manage-freezes`, so a principal that can end a freeze does not also get break-glass. The job keeps the break-glass and the freeze ID. The audit entry of the start has the kind `override`. An agent cannot ask for break-glass (ADR 103 decision 7).
_Avoid_: Freeze override, bypass

**Job token**:
An access token that the server makes for the steps of a runner phase, at the claim of the phase (ADR 100 decision 20). It is a token of the principal that started the job. The server limits it to the unit of the job, and it has the expiry of the claim. It reads the outputs of another unit only when two checks pass (ADR 213 decision 1, ADR 273 decision 5). A sharing rule names the unit of the job, and the principal whose grants decide holds `unit:read-outputs` on the producer. That principal is the principal that started the job, or the last saver of the trigger that started the job. For an open of the profile block `outputs/opendustpan`, it is the attachment authority. The token of a job that only plans decrypts the secrets of its own unit only when the principal that started the job has `unit:read-secrets`. It also decrypts them when the commit comes from a protected branch. It writes only its lock or lease and the events of the plan (ADR 213 decision 2). It carries the job-ID claim. The launcher gives it to the tool (ADR 133, ADR 134). OpenTofu gets it through its credentials helper, and the Pulumi CLI through a file of mode 0600. It never goes in an environment variable. A job token of a job that ended authenticates nothing.
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

**Job proxy**:
A proxy between one runner job and the network (ADR 327). It runs next to each job of a unit with proxy rules. A proxy rule must allow the host, the method and the path of a request. Else the job proxy refuses the request. Only the job proxy holds the key of the CA of the job. It applies the outbound guard to each rule host, also through an upstream proxy. How it adds a credential, so that the job never holds that credential, is a proposal (RFC 16).
_Avoid_: Egress proxy, egress gateway, sidecar (as the name of the feature), firewall

**Proxy rule**:
One allow line of the job proxy (ADR 327). It matches the host, the method and the path of a request, with `*` and `**` in a path pattern. A proxy rule only allows. A request that no proxy rule allows is refused. Always write "proxy rule", not "rule" alone.
_Avoid_: Egress rule (the egress words are for the outbound guard and the egress allowlist), request rule, ACL, network policy (that is Kubernetes' word), permission

**Proxy rule set**:
The set of proxy rules of a unit (ADR 327). The job start renders their templates once. The document format and the scopes that hold a proxy rule set are a proposal (RFC 16).
_Avoid_: Egress policy, proxy policy, proxy config

**Upstream proxy**:
A proxy of the team's network that carries the outbound connections of the job proxy (ADR 327). The team runs it, and it applies its own rules. OpenDustpan does not configure them. How the job proxy finds it and its credential is a proposal (RFC 16).
_Avoid_: Egress proxy, corporate proxy (alone), parent proxy, next hop

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
The act that ends a runner (ADR 133 decision 8). A principal with `namespace:manage-runners` retires a runner with `POST /api/dustpan/v1/namespaces/{namespace}/runners/{runner}/retire` (ADR 138 decision 6). A retired runner authenticates nothing. The retire expires the claims of the runner in the same transaction, and the takeover rules apply. It also ends every job session of the runner, so each later request of such a session fails with `retired`. The record stays for the audit log, with the status `retired` (ADR 130 decision 9). A join again does not apply to a retired runner. To retire is not to drain: a draining runner only claims no new work.
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
The code of one commit of one repository in the blob store (ADR 135 decision 1). It holds the raw git pack with the commit and every tree and blob that the commit reaches. It also holds the allowed submodules and Git LFS objects. Its key is the namespace, the forge connection, the repository, the commit and the hash of the source filter. Two jobs on one commit with one source filter share one snapshot. At the start of a runner job, the server gets the pack in a durable operation of the kind `source.fetch`. The launcher reads the snapshot through its job session and checks it before it writes a file. So a runner never connects to the forge for the code (ADR 135 decision 3). The server deletes its blobs 7 days after the end of the last job that names it. A backup does not hold them (ADR 135 decision 9).
_Avoid_: Snapshot (alone), source (alone: that is the backend before an import), clone, checkout (the files that the launcher writes from a snapshot)

**Source filter**:
The setting of a namespace that decides which submodules and Git LFS objects a source snapshot holds (`source.Filter`, ADR 135 decision 6). ADR 226 decision 9 gives the name. It is a fixed control, not a gate. It is empty by default, so a snapshot holds no submodule and no Git LFS object. `submodules` and `largeFiles` list `https` URL patterns, at most 64 in each list. `failOnDenied` fails the `source.fetch` operation when the commit has a submodule or a pointer that the filter does not allow. Its `protectedBranches` give the fact `source.protected` (ADR 213 decision 2). A change of the filter needs `namespace:manage-runners`. A change of `submodules`, `largeFiles` or `failOnDenied` makes a new snapshot key. A change of `protectedBranches` alone does not. The routes are `GET` and `PUT /api/dustpan/v1/namespaces/{namespace}/source-filter`.
_Avoid_: Source policy, submodule allowlist

### Gates and approvals

**Decision point**:
A fixed moment where the server evaluates the gates of a change (ADR 224 decision 2). There are six. `change.propose` runs before a plan, `change.planned` after the plan and its facts, and `change.apply` before each phase that changes. `profile.save` and `profile.open` run for a profile. `state.write` runs before a write of state from a person's own CLI or CI, and before a tool-neutral state operation. Each has its own fact schema with a version. The decision point is the Cedar action of each gate policy. A new decision point needs a new ADR.
_Avoid_: Hook, stage, policy point, recheck point

**Fact**:
A typed value that the server produces before a decision point (ADR 226). Examples are a plan count, a cost delta and the kind of the source. A fact never decides: a gate decides. A missing fact is a typed value: its group has `available` set to `false`, and each built-in gate fails closed on it. A fact never holds a secret value. An external check gives facts too.
_Avoid_: Input (alone), signal, attribute (that is a Cedar term for a field)

**Fact document**:
The facts of one decision point for one unit, in the shape of the fact schema of that point (ADR 226 decision 2). It is the Cedar context of the request. The decision record holds it and its SHA-256.
_Avoid_: Policy input

**Approval rule**:
Data that says who may approve and how many people must approve (ADR 225 decision 1). It names approvers (groups, users and `unitOwners`), a count of 1 to 10, `authorMayApprove` (default `false`), `freshAuthSeconds` and `timeoutSeconds`. A gate names it in its outcome as `approval:<rule>`. The server resolves the rule ID at the scope of the gate, then at each wider scope, never at a narrower one. One rule gives one requirement. `unitOwners` reads only the context at the scope of the rule or at a wider scope. Only a person in a session approves: never an agent, a service principal or a token. The rule stores the ID of each approver group. The group name is only a label.
_Avoid_: Approval policy, quorum

**Approval default**:
The approval rule that a namespace, a collection or a unit sets for each change below it, with no gate (ADR 225 decision 4). With none set, the default is off and no change needs an approval. Each default holds on its own, and its `unless` exempts a change from that default only. Defaults that name no approvers merge into one requirement `default`, with the highest count and the shortest fresh window. Any holder of the approve action who is not the author can meet it. A default that names approvers is its own requirement `default/<scope>`.
_Avoid_: Minimum approvals, `MinApprovals`

**Ownership rule**:
A rule stored in the server that names who owns a part of a change (ADR 225 decision 9). It has a scope, a mode, path globs, a Cedar condition `when` over the facts, groups, users, a count and a fresh window. The mode is `enforce` or `evaluate`, and a new rule starts in `evaluate`. It needs no gate. Each rule in `enforce` mode that applies gives one requirement `owners/<id>`. It is inspired by a code owners file, but it is never a repository file.
_Avoid_: Code owners, CODEOWNERS, owner rule

**Access limit**:
One Cedar `forbid` policy on a scope, which the server evaluates after the grants allow an action (ADR 223). A limit only takes away what a grant gives. A `permit` in a limit set is refused at save. A refusal fails with `gate.ErrLimited` and writes a decision record. A session of a break-glass account is not subject to a limit.
_Avoid_: Deny rule, guardrail, condition on a grant

**Evaluate mode**:
The mode of a gate that the server evaluates and records with no effect (ADR 224 decision 7). A new gate and each starter pack start in it. The simulate route replays draft gates over stored decision records before a switch to `enforce` mode.
_Avoid_: Dry run, audit mode, advisory (that is a level of a resource policy report)

**Decision record**:
The record of one evaluation of one decision point for one change, with the ID prefix `gdr-` (ADR 229). It holds the fact document and each gate with its version, mode, match and outcome. It also holds the requirements, the warnings, `autoApply`, the state of each fixed control and the result. Each refusal, each approval wait and each warning names its record.
_Avoid_: Decision log, policy result

**Fixed control**:
A control that refuses on its own, outside the gates (ADR 222 decision 4). The fixed controls are the workspace lock, the manual lock, a freeze with its break-glass and the override switches. They are also the module switches, the quota reservation and the quota counts, the source filter, safe delete and the plan staleness check. The agent controls are fixed controls too: the agent mode, the confirmation step and the secret consent. The decision record shows the state of each fixed control that the server checked.
_Avoid_: Gate, policy

**Final gate**:
A gate with `final: true` (ADR 224 decision 12). A narrower scope cannot save a gate with its ID or an approval rule with the ID that it names. It cannot pass the gate with a break-glass. Each of these fails with `gate.ErrFinal`. The owner of the gate's own scope can make it weaker only with a reason, a fresh authentication and an audit entry. No gate, final or not, can be changed from a narrower scope.
_Avoid_: Locked gate, mandatory gate

**Path set**:
A stored set of glob patterns of a scope, with an ID (ADR 226 decision 13). Go matches the globs against the changed paths, and gives the facts `paths.touches["<set>"]` (at least one changed path matches) and `paths.only["<set>"]` (each changed path matches). Cedar reads the booleans.
_Avoid_: Path filter, path prefix

**Signature carry-over**:
The setting that lets a signature of a change order carry over to a new plan revision (ADR 225 decision 12). The plan digest inputs of the new revision must be identical. It is `off` by default, and can be set to `on` or `forbidden` on a namespace, a collection or a unit. A changed policy, a changed approver set, an expired approval or a revoked signer never carries over. A carried signature shows `carriedFrom`.
_Avoid_: Sticky approval, stale approval

**Fresh authentication**:
A built-in MFA step, or a new authentication at the SSO identity provider. It is no older than the window `freshAuthSeconds` (ADR 227). A person can sign in with a plain OAuth 2.0 provider or a trusted header. For that person, it is a passkey, a TOTP code or an SSH signature. The person enrolled it with OpenDustpan (ADR 301). The step belongs to one session. A recovery code never gives it. The default window is 900 seconds. A window of 0 asks for a fresh authentication at each step. A namespace or an approval rule can only set a shorter window.
_Avoid_: Token freshness, re-auth, step-up (alone)

### Change orders

**Change order**:
An object above jobs that holds the plan of one or more units of one namespace, its approvals, its signatures and its threads (ADR 190). Its ID has the prefix `cho-`. Jobs run under it: a plan job for each unit of each plan revision, and an apply job for each unit of the approved revision. The change order owns the approvals of its jobs, so its jobs get no approval gate. Its states are `planning`, `plan-failed`, `in-review`, `approved`, `applying`, `partially-applied`, `applied`, `closed` and `expired`. A person asks for the apply. An agent can only comment and leave blocking comments on it. Version 1 runs runner jobs only.
_Avoid_: Change request (that is an ITSM record, RFC 51), change set, pull request, merge request, review request (that is of the forge)

**Plan revision**:
The plan of a change order at one time (ADR 191). It has a number, from 1 and one higher for each attach, and one unit plan for each unit. Only the revision with the highest number is current, and the others are superseded. An attach of a new revision voids every signature of the earlier revisions, unless signature carry-over holds. A ready revision has a digest.
_Avoid_: Plan version, patch set, iteration

**Unit plan**:
The part of a plan revision for one unit (ADR 191). It holds the unit reference, the operation (`apply` or `destroy`), the code location with its commit, and the plan job. It also holds the binding of its stored plan: the plan ID, the hashes of its two parts and the state version that it read. In the console, it is the preview of a Pulumi unit or the plan of an OpenTofu unit (ADR 195).
_Avoid_: Unit preview (in shared code), sub-plan

**Plan job**:
A runner job with the operation `plan` that a change order starts for one unit of a plan revision (ADR 190). Its one phase stores one stored plan. It runs as the principal that attached the revision.
_Avoid_: Preview job, dry run

**Apply job**:
A runner job with the operation `apply` or `destroy` that a change order starts for one unit of its approved revision (ADR 190). Its spec names the stored plan of the plan job of the same unit (`job.Spec.Plan`), and it has one phase, of the kind `apply`. A stale plan ends it `failed` with the reason `plan-stale`, before any change.
_Avoid_: Deploy job, run (alone)

**Author (change order)**:
The principal that created a change order, or that attached a plan revision to it (ADR 190 decision 4). An author never approves, requests changes, rejects, opens a blocking thread or resolves one, also when the author holds `changeorder:approve`. An agent is never an author.
_Avoid_: Owner (that is a context field), requester (that is of a self-service request)

**Signature**:
The record that binds one approval to one plan revision of a change order (ADR 191). It holds the approver, the revision and the digest that the approver saw. It also holds the requirement that it counts for, the time and the kind of the credential. A principal has at most one signature on each revision. A new revision voids it, with the cause `new-plan`, unless signature carry-over holds. A loss of access voids it, with the cause `access-lost`. The digest is the SHA-256 of the encoding `cho-digest/1` of the revision.
_Avoid_: Sign-off, stamp, approval (alone: an approval is the verdict)

**Approval requirement**:
The need of one approval rule for one change: a count of signatures by its eligible approvers (ADR 225 decision 3). A gate whose outcome names a rule gives one, and so does an approval default. A change needs each requirement, and one signature fills one slot of one requirement only, by bipartite matching. So the approvals across requirements come from distinct people. With no gate and no approval default, a change has no requirement.
_Avoid_: Quorum, rule (alone)

**Eligible approver**:
A principal that can approve one requirement (ADR 225 decision 7). It holds the approve action of the object (`changeorder:approve`, `job:approve` or `profile:approve`) on each unit, and it is in the approvers of the approval rule. It is an author only when the rule has `authorMayApprove`. It is never an agent.
_Avoid_: Reviewer (alone: any reader can comment)

**Change order review**:
One submit of one reviewer on one plan revision, with an ID with the prefix `cor-` (ADR 192). Its verdict is `approve`, `request-changes`, `comment` or `reject-and-close`. Request changes keeps the change order open and opens at least one request-changes thread. Reject and close ends the change order `closed`. Both need a reason. Its summary is a comment, so a redaction reaches it. An agent records no verdict: it submits its blocking threads in a review of the verdict `comment`.
_Avoid_: Review (alone), sign-off

**Draft review**:
The threads and replies that one reviewer wrote on a change order and did not submit (ADR 193 decision 5). Only its reviewer reads it. The reviewer submits it in one review. A draft write writes no audit entry. No backup holds a draft, and the store deletes it 30 days after its last write.
_Avoid_: Pending review, draft (alone)

**Thread anchor**:
What a thread of a change order is about: the whole plan, 1 to 50 resources, or a change group (ADR 193 decision 3). A resource is the unit ID and the key of the resource in the plan of its tool. The key is the URN for Pulumi, and the resource instance address for OpenTofu. A thread follows its anchor into each new plan revision.
_Avoid_: Target, location, line

**Outdated thread**:
A thread whose anchor does not map in full to the current plan revision (ADR 193 decision 8). A resource of the anchor has no change in the new plan, or its change group is not in it. An outdated request-changes thread still blocks approval until a person resolves it.
_Avoid_: Stale thread, orphan thread

**Cause diff**:
The change of the code that the author of a change order made (ADR 194 decision 2). It is the file diff of each code location from the base commit to the head commit. The review shows it before the effect. The server reads it from the forge at each read and does not store it.
_Avoid_: Source diff, code diff (alone), state diff (that compares two state versions)

**Needs attention**:
The section of the review view of a change order that lists each replacement and each delete, one by one (ADR 194 decision 4). It also lists, as a whole, each unit whose plan the server cannot read. It gives a warning for two units of the change order where one reads the outputs of the other. It comes before every change group. A replacement or a delete is never in a group.
_Avoid_: Dangerous changes, warnings

**Change group**:
Two or more identical in-place changes in the plan revision of a change order (ADR 194 decision 5). Identical means the same tool, type, value paths and values before and after. A change that touches a secret path is never in a change group, so the key covers no secret value. Its key is a content key, not an ID, with the prefix `cog-`. The key stays the same from one revision to the next for the same change. A thread can have a change group as its anchor, of the kind `change-group`. In Go, it is `changeorder.ChangeGroup`.
_Avoid_: Group (alone: that is a set of principals), batch, bucket

**Proposed changes**:
The console heading of a plan of a change order whose units are of more than one tool (ADR 195). For units of one tool, the heading is the word of that tool: "Preview" or "Plan".
_Avoid_: Plan (for a set of Pulumi units), preview (for OpenTofu units), change set

### Profiles

**Profile**:
A named set of config, secrets and credential sources in one namespace, which a run of any tool can read (ADR 200). It has an ID with the prefix `prf-` and a name of one or two segments, for example `aws-prod` or `payments/aws-prod`. Each save makes a new profile revision.
_Avoid_: Environment (except in the paths of the Pulumi Cloud-compatible API), context, config, variable set, vault

**Profile folder**:
The first segment of a profile name of two segments. It groups profiles in views, and the Pulumi CLI sees it as the ESC project (ADR 202 decision 9).
_Avoid_: Project (alone)

**Profile revision**:
One immutable, numbered version of the document of a profile (ADR 202 decision 2). Each revision is a history item with its own item key.
_Avoid_: Version (alone)

**Revision tag**:
A name that points to one profile revision, for example `stable`. The tag `latest` always names the newest revision that is not retracted (ADR 202 decision 3).
_Avoid_: Label (a label is a key and a value for views), version tag

**Entry**:
A map in the `values` of a profile document whose keys start with `$`, for example `$secret`, `$final` or `$type`. It describes one value (ADR 201 decision 6).
_Avoid_: Function, fn

**Provider block**:
A named block under `providers` in a profile document. Its `use` names a profile provider, for example `login/aws` (ADR 201 decision 7).
_Avoid_: Provider (alone), source, integration

**Profile provider**:
An adapter that an open of a profile calls, of the kind `login`, `secrets` or `outputs` (ADR 206). The external adapter is one of them.
_Avoid_: Provider (alone: that is a plugin of OpenTofu or the Pulumi CLI), integration, plugin

**Rotator**:
An adapter that makes a new credential on a schedule and retires the old one (ADR 206 decision 5). A rotator block of a profile names it.
_Avoid_: Rotation provider

**Rotation record**:
The sealed state of one rotator block, with the current, the previous and each `retiring` credential, and the slot of each one. It lives outside the document, so a rollback never brings back a retired credential. The server binds it to a hash of the target inputs of the block, which are static. A change of that hash starts a new record. The old record is `detached` for 30 days, then the server retires its credentials, or keeps it as `orphaned` when a retire fails (ADR 202 decision 8).
_Avoid_: Rotation state (alone)

**Rotator slot**:
One place at the remote for one credential of a rotator block, for example one of the two access keys of an IAM user. The spec of a rotator declares its slots. A rotation writes into a slot that holds no `current` credential. It retires the credential of that slot first, and fails with `ErrConflict` while the grace period of that credential has not ended (ADR 202 decision 8).
_Avoid_: Key slot, version

**Rotation lease**:
The lease in the store that lets one rotation at a time run for one rotator block. It lasts at most 90 minutes. The record of the outcome must carry its lease token. A manual rotation and a scheduled one take the same lease. The rotation writes the state `pending` before it calls the rotator. A failure keeps `pending` until a later rotation or an abandon (ADR 202 decision 8).
_Avoid_: Rotation lock

**Exports**:
The part of a profile document that says what reaches a consumer: `variables`, `files`, `pulumi` and `opentofu` (ADR 201 decision 9).
_Avoid_: Outputs (outputs belong to a unit or a provider block), environment variables (alone)

**Never store**:
The mark of a value that no consumer may store in a plan or in state. The data source of OpenTofu refuses it, and only the ephemeral resource reads it (ADR 201 decision 12, ADR 209 decision 3).
_Avoid_: Ephemeral (alone), transient

**Open**:
The resolve of a profile for one caller: imports, provider blocks, types and checks, with the secret marks (ADR 203). Each open writes one audit entry. An open needs `profile:open`, or a job token for a profile that the job fixed.
_Avoid_: Read (a read of a profile gives sealed secrets), decrypt

**Open session**:
The stored result of an open, with an ID of the prefix `pos-` (ADR 203 decision 3). Only its principal reads it, for at most 12 hours, and each read checks the action of the principal again.
_Avoid_: Session (alone)

**Profile attachment**:
A link from a profile to a namespace, a collection or a unit. The jobs of each unit of the object read the profile (ADR 203 decision 5).
_Avoid_: Binding, assignment (an assignment is a runner term)

**Attachment authority**:
The principal whose actions an open uses. For a direct open, it is the caller. For a job open, it is the principal who made the attachment. For an attachment of Self-service, it is the person who published the blueprint version. The server checks the full set of the attach checks again at each open of a job. These are the job start, each step, each token renewal and each read of a session. A person who passes the attach checks can re-authorize an attachment (ADR 203 decision 4).
_Avoid_: Attachment owner, run-as

**Change rule**:
The rule of a profile that turns each save into a profile draft that needs approvals (ADR 202 decision 6).
_Avoid_: Ruleset, change gate

**Routing change**:
A change of a profile that can change where a provider or rotator block sends secrets. The server decides it from the syntax, with no open. It is one of these: a difference in the text of a `providers` or `rotators` block. A difference in the text of a path of the taint set of a block. A difference in `imports` or `exports`. A rollback, tag move or apply of a draft with such a difference. A change of profile Q can also be a routing change for a referrer of Q. This is true when the referrer follows the changed revision and has a block whose taint set holds a changed path. It needs `profile:open` on the profile and on each such referrer, as well as `profile:write`. The diff compares the evaluated destination hosts, not the text (ADR 202 decision 2).
_Avoid_: Sensitive change, dangerous change

**Taint set**:
Each path that an input of a provider or rotator block reads, directly or through expressions. The paths are in the own tree of the profile that holds the block. The server finds it from the syntax tree. A change to a path of a taint set is a routing change (ADR 202 decision 2).
_Avoid_: Dependencies (alone), inputs (alone)

**Profile draft**:
A proposed revision of a profile under a change rule, with a base revision and approvals. It applies as a new revision after approval (ADR 202 decision 6).
_Avoid_: Change request, draft (alone: a draft review belongs to a change order)

**Open rule**:
The rule of a profile that needs an approved access request before a person opens it (ADR 202 decision 7).
_Avoid_: Open gate, just-in-time access

**Access request**:
A request of a person to open a profile for a time, under an open rule (ADR 202 decision 7). It holds a reason.
_Avoid_: Open request

**Issuer**:
The OIDC issuer of one namespace, at `https://<issuer host>/oidc/<namespace ID>`, with its own signing keys (ADR 204). The issuer host comes from the installation setting `issuerBaseURL`, else from `publicURL`. When the namespace sets `issuerURL`, `iss` is that URL, and the server signs only with a key that the copy check found at that URL. Its paths `/oidc/<namespace ID>/...` are outside the versioned API. Clouds and other systems trust it.
A rotation of the signing keys of the issuer **activates** the `next` key: the `next` key becomes the `active` key (ADR 286). The rotate answer says it in the field `activated`.
_Avoid_: Identity provider (an identity provider signs in people), OIDC provider (alone)

**Issued token**:
A JWT that the issuer of a namespace signs, with the claims of ADR 205. Its `purpose` is `login` or `adapter`. It lives at most 60 minutes.
_Avoid_: ID token, access token (an access token authenticates to OpenDustpan)

**Copy check**:
The check of the two files at the `issuerURL` of a namespace. The discovery document must name the issuer and the JWKS address. The JWKS must hold the key ID of the active key, and of the next key before its activation (ADR 204 decision 3). The server signs only with a key that a copy check found.
_Avoid_: Sync check, key check

**Subject template**:
The list of keys that make the `sub` of an issued token, for example `namespace`, `collection`, `tool`, `unit` and `access` (ADR 205 decision 6).
_Avoid_: Subject attributes, sub format

**External adapter**:
A profile provider or a rotator that a team writes and hosts. The server calls it with an HTTPS `POST` and an issued token with the hash of the body (ADR 208).
_Avoid_: Webhook, plugin, connector

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

**Stop reason**:
The reason that an agent run ended, which the status route of the run gives. Examples are `canceled`, `turn-failed` and `credential-ended` (ADR 120). ADR 129 adds three. `model-quota` is for a used-up plan, quota or credit. `model-rate-limited` is for a wait above 5 minutes. `connection-changed` is for a change of the format, the model or the base address during a run.
_Avoid_: Exit code, end state, status (alone)

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
The one model that an administrator with `installation:manage-agent` connects for the installation (ADR 120 decision 3). Its connection document holds every field. The operator sets each one. The server fills no field with a value for a provider (ADR 129). The only defaults are empty values, the `userAgent` of the release and the `retry-after` header. The credential is a secret item of `@server`. With no model connection, Dusty is off.
_Avoid_: Model config, provider account, API key

**Connection document**:
The JSON document `agent.ConnectionSpec` of a model connection, with `version: 1` (ADR 129 decision 2). It holds the wire format, the base address, the model name, the authentication header and its secret reference, and extra headers. It also holds the fields for tools, the transport, reasoning, caching, the conversation key, errors and rate limits. It never holds a secret value.
_Avoid_: Model config, provider settings, preset

**Wire format**:
The request and answer format of a model API that a model adapter speaks (ADR 129 decision 5). The values are `chat-completions` (the Chat Completions format), `messages` (the Messages format) and `responses` (the Responses format). One adapter serves every server of its wire format.
_Avoid_: API type, provider type, flavor

**Secret item**:
A value that the server seals in a scope with its own data key (ADR 23), and that no route answers again. Examples are the secrets of a sink (ADR 145) and the credential of a model connection (ADR 129 decision 10). A document refers to it by its reference `itm-...`.
_Avoid_: Secret setting, credential field, sensitive setting

**Conversation key**:
The identifier that a model adapter sends with each model call of one conversation (ADR 129 decision 18). It goes in the headers and the body field that the connection document names. It is an HMAC of the ID of the first run of the conversation, so it holds no identity of a person. Some model servers use it to route a prompt cache.
_Avoid_: Session ID, user ID, cache key (alone)

**Secret filter**:
The filter that takes secret-like text out of prompts, tool results and model answers (`agent.SecretFilter`, ADR 129 decision 24). It runs before the transcript records the text. It is always on, for every model. No setting turns it off. It does not change an opaque reasoning part, which the store keeps sealed and no route answers.
_Avoid_: Reply filter, output scrubber, redaction (that is a history action), masking (that is for revealed values)

**Capability probe**:
The durable operation that checks a model connection before the document becomes the connection (ADR 129 decision 29). Its checks are the reach, the authentication, a tool call, a tool result, the transport, the token use, the reasoning parts and the cache. A failure of the first five blocks the connect. The connect route answers at once, and a status route gives the result of each check.
_Avoid_: Health check, connection test, ping

**Outbound guard**:
The rules of the package `pkg/egress` for each outbound request that the server makes to an address that a principal gives. Examples are the delivery of a notification to a webhook and the read of an iCal feed. The rules apply to the URL when a principal saves or tests it. They also apply to the resolved IP address of each connection. So a DNS answer that changes after the check cannot pass them. The adapter `egresshttp` applies them to each connection. The job proxy uses the same rules for its own outbound connections (ADR 327).
_Avoid_: Egress proxy, egress filter, SSRF filter, firewall

**Egress allowlist**:
The list of hosts and CIDR ranges that a model call of the installation can reach (ADR 129 decision 25). A private address or an IP literal needs a CIDR entry with one port, and `http` needs an entry with `"http": true`. No entry overrides the always-denied list of link-local, metadata, unspecified, broadcast and multicast addresses. With no entry, no model call goes out.
_Avoid_: Egress rule, proxy rule (that is for a job), firewall, IP allowlist (that is for requests to a namespace)

**Reaper**:
The durable operation that ends each agent run whose turn ended with no step that ends the run (ADR 120 decision 10). Its kind is `agent.KindReap`, and it runs every minute on one instance. It first deletes each sealed reveal past its hard expiry. It then ends each such run as canceled, or as failed with the stop reason `turn-failed`.
_Avoid_: Retention expiry (that purges deleted units), garbage collector, janitor

**MCP endpoint**:
The route `/mcp` that serves the agent tools over the Streamable HTTP transport of the Model Context Protocol (ADR 123). It offers tools only: no resources, no prompts and no sampling. It needs no model connection. It is off until an administrator sets `mcpEnabled: true`. It answers 404 while it is off or while the audit log is off.
_Avoid_: MCP server (as a separate program), agent API

**Stdio proxy**:
The client command `opendustpan agent mcp` (ADR 123 decision 8). It reads JSON-RPC messages on stdin, forwards each to the MCP endpoint and writes the answers on stdout. It has no tool of its own and no cache. It never opens the database, the blob store or the key-encryption key. Its channel is `mcp-proxy`.
_Avoid_: MCP proxy, CLI proxy, bridge

**Escape hatch**:
The one gated path by which an agent reads secret values in plain text: `read_state` with `secretValues: true` (ADR 124 decision 1). It needs `unit:read-secrets` and each stage of the secret consent that applies. By default it is shut, and each agent tool answers masked values.
_Avoid_: Secret hatch, backdoor, unmask mode

**Secret consent**:
The list of stages that the server checks before an agent reads secret values (`agent.SecretAccess.Check`, ADR 124 decision 2). The stages, in order: the installation stage, the namespace opt-in, the person opt-in, and the consent for one use (outside agents only). It is in the `Toolset`. No setting of a model, a prompt or a client can open it. It is a fixed control, not a gate. ADR 222 decision 8 gives the name. ADR 124 calls it the secret gate.
_Avoid_: Secret gate (older name), secret switch, secrets toggle

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
One entry with content in the history of a unit (ADR 150). It is one Pulumi update with its checkpoints, events and journal entries, or one OpenTofu state version. It can also be one stored plan or one version of a job log, which the job model owns (ADR 152). Shared code names an item by its item owner, its kind and the ID that the tool gives the item. The kinds are an open set that the server registers (ADR 154): `state`, `plan`, `job-log` and `comment` now. The core never parses the item ID. A preview is not a history item.
_Avoid_: Revision, version (alone), history entry

**Item owner**:
The object that a history item belongs to (`history.Owner`, ADR 154). The owner kinds are an open set that the server registers: `unit` for state items, plans and job logs, and `change-order` for comments (ADR 198). The scope of an item key is the namespace and the owner ID. Each owner kind names the object of a read, the action of a redaction and the end of its items.
_Avoid_: Parent, container, unit (alone, when the owner can be a change order)

**Latest state item**:
The state item of a unit that no redaction can target (ADR 150, ADR 151). For a stack, it is the update that holds the checkpoint that `stack export` answers. For a workspace, it is the current state version. It also holds the replay base of each update that has not ended or whose journal the server did not replay yet. A request to redact it answers 409 with `history.ErrLatest`.
_Avoid_: Current item, head

**Item key**:
The random 256-bit key that seals all content of one history item (ADR 151). The server never derives it from another key. The namespace key wraps it (ADR 217). The server writes the wrapped key as the blob `namespaces/<namespace ID>/item-keys/<item key ID>/v<namespace key version>`, and a rewrap writes it as a new blob. SQL keeps only a record of the key. An item key ID has the prefix `ik-`. The Pulumi CLI never sees an item key, and the data key of the unit does not change.
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
The append-only log of each inventory write (ADR 150). Each tool appends one row in the transaction of each inventory write: `created`, `changed`, `renamed`, `deleted`, `restored` or `purged`. A row holds the fields of the inventory entry at that time, the time of the write and a sequence number. The sequence is per namespace. A counter row of the namespace assigns it in the write transaction, so it follows the order of commits (ADR 154). A purge keeps the rows of the unit, which hold names and counts and never state. The server keeps the log forever, and no setting changes this. A rebuild of the inventory cannot rebuild the log in full.
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
The feature that finds unmanaged resources, and one run of it (ADR 163). A sweep is a cloud sweep or a state check. The console shows both kinds together. As the name of the feature, write Sweep. The Go names of `internal/unmanaged` keep "scan", for example `Scan` and `ScanStore`, because they are internal.
_Avoid_: Scan (except in the Go names of `internal/unmanaged`), discovery, crawl, sweep for anything but the Sweep feature (write "retention expiry" or "orphan cleanup")

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
A record that lets the classification of one namespace read the units of another namespace (ADR 163). Two teams can use one cloud account from two namespaces. A link is active only when an administrator of each namespace accepts it with `namespace:accept-link`. The operator cannot make one alone (ADR 216). A link is one of the joins between namespaces (ADR 216 decision 8). A link goes both ways. It holds the stable IDs of the two namespaces, never their names. Each write appends an audit entry. A soft delete of a namespace stops its links, and a purge of the namespace deletes them. Without a link, the classification of a namespace reads the units of that namespace only.
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

**Projection run**:
One run of the durable operation `projection.unit` for one unit (ADR 166). It reads the latest state item of the unit with secret values masked. It writes the output entries of context, the entries of the resource index and the deleted marks of resource context. Each transaction that sets a new latest state item starts a run, unless a run of the unit already waits. So state items that arrive while a run waits coalesce into that run. A run is not part of blame.
_Avoid_: Projection job (a job belongs to the job model), indexer, scan

**Projection table**:
A table that holds only data that the server can compute again from other tables (ADR 166). The resource index is one. A backup leaves the projection tables out. A restore empties them, and the server builds them again for each namespace. Until the build of a namespace ends, its state check and the match of its sweeps wait. So an index that is not complete never changes a finding.
_Avoid_: Cache, derived table, control table (a restore keeps its rows)

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

### Console

**Theme**:
The look of the console that a person selects: `default` or `terminal` (ADR 180). A theme sets the font of the interface and the default of the keyboard settings. The colors come from the color mode and the color scheme.
_Avoid_: Skin, style

**Terminal theme**:
The opt-in theme of the console: IBM Plex Mono for all text, dense, keyboard first, with color schemes. "Terminal" is its only name.
_Avoid_: The name of a terminal emulator or of an editor for it

**Color mode**:
`system`, `light` or `dark`. `system` follows the color scheme of the operating system.
_Avoid_: Theme (for light or dark)

**Color scheme**:
A palette of the Terminal theme, from the Gogh collection, mapped to the color tokens of the console (ADR 180). Its name is a label with credit to its authors, never our name. `opendustpan` is the built-in scheme: the palette of the default theme.
_Avoid_: Theme (for a palette), skin

**Preset**:
Branding that an operator sets for the whole installation, for example the palette of OpenDustpan Cloud. A person never selects a preset, and a theme and a color scheme apply on top of it (ADR 180).
_Avoid_: Scheme, theme (for branding)

**Preferences**:
The settings of the console that a person keeps on the account, on every device (`/api/dustpan/v1/me/preferences`, ADR 180, ADR 210). They are the theme, the color mode, the color schemes of the Terminal theme and the keyboard settings. They also hold the language, the formatting locale and the digits (ADR 181). They hold the clock (`hourCycle`), the date style, the first day of the week and the time zone too. The keyboard settings are shortcut hints, single-key shortcuts and the hint style: `brackets` `[a]`, `angles` `‹a›` or `inverse`. The keyboard settings apply only in the Terminal theme. The server stores only the settings that a person changed, and a field that the person never set has its default. A `PATCH` is a JSON merge patch, and one that changes nothing writes no audit entry. A row belongs to a person, not to a namespace.
_Avoid_: Profile (a profile holds shared config and secrets, ADR 200), user settings

**Home**:
The first screen of a person in the console. It shows, in this order: failed runs, approvals that wait for the person, locks the person holds, and changes since the last visit (ADR 311). It is not the same as **Principal home**.
_Avoid_: Dashboard, landing page, home installation, home region (see **Primary installation**)


**Language** (of a person):
The language of the words that the console shows to a person. It is a BCP 47 tag. The console shows one of the 33 languages of ADR 181 and ADR 231. The language also sets the direction of the page: `ar` and `he` write from right to left. The server checks only the shape of the tag, and the console owns the list. The default is the first match in the browser's list, else `en`. A preference, separate from the formatting locale.
_Avoid_: Locale (for the language)

**Formatting locale**:
The BCP 47 locale that sets how the console shows dates, times, numbers and lists, through the `Intl` APIs (ADR 181). The default is the browser's locale. A person can override the clock (`hourCycle`), the date style, the first day of the week, the time zone and the digits. The digits are Western (`latn`) or Arabic-Indic (`arab`), ADR 181.
_Avoid_: Language (for formats), region

**Message file**:
The file that holds the messages of one language in ICU MessageFormat (ADR 181). The console has one for each language, and the server has one for each language of its emails and chat notices (ADR 234). It marks each high-stakes message, which needs native review before it ships. Each translated message carries its translation status and the hash of its English source (ADR 232).
_Avoid_: Catalog (a common noun for a fixed list), translation table

**Term list**:
The list of terms that stay in English in one language, for example "stack", "workspace", "plan" and "state" (ADR 181). It adds to the shared term list, which holds the names of tools and projects, commands and CLI flags for every language (ADR 233). A check fails a translation that changes a term. A language can ship with a draft list. An engineer whose first language is that language signs it off. The rule: use the word that does not make a user of that language cringe.
_Avoid_: Glossary (that is this file), do-not-translate list

**Pseudo-locale**:
A test language of the console. `en-XA` makes each message longer, adds accents and puts brackets around it (ADR 181). `ar-XB` shows the literal text of each message from right to left and sets the direction to right to left (ADR 181). Its arguments stay as they are. Tests use them to find clipped text, text outside a message file and layout that does not mirror. Release builds do not list them.
_Avoid_: Test language, fake locale

**Translation status**:
How far people checked a translated message or a language: `machine-translated`, `community-verified` or `native-reviewed` (ADR 232). A change to the English source makes a translation stale, and a stale translation counts as `machine-translated`. A language has the highest status that 95% of its messages and all its high-stakes messages hold. The check computes it.
_Avoid_: Quality, completeness (for the status), approved

**Machine-translation note**:
The note for a language with the status `machine-translated` (ADR 232). The console, the translated docs and each notice show it, in that language and in English. The text: "This text was machine translated. Help us fix and check it. The credits page names each person who helps." A report mode next to it gives each string a link to report it. The credits page names the people who translated or checked each language.
_Avoid_: Disclaimer, warning (for the note)

**Product name**:
The name of the application that an operator sets for the installation, `branding.productName` (ADR 235). The default is "OpenDustpan". It reaches the console, emails and chat messages only. Server errors, logs, CLI output and audit entries say "OpenDustpan".
_Avoid_: Brand, white label (for the name)

**Agent name**:
The name of the built-in agent that an operator sets, `branding.agentName` (ADR 235). The default is "Dusty". Text that the agent writes to a person uses it. Logs, the audit log and the API keep the fixed agent ID `dusty`.
_Avoid_: Bot name, persona

**Error code**:
The stable machine code of an API error, for example `workspace.locked` (ADR 182). It is `errorCode` in the camelCase APIs and the JSON:API `code` member in the TFE-compatible API. A code never changes its meaning. The console selects its message by the code and never parses the English text. Not the `code` field of the camelCase error shapes, which is the HTTP status.
_Avoid_: Error type, error ID, status code (for the machine code)

**Error argument**:
A value that the text of an API error names, for example the name of a stack or a limit (ADR 276). The answer sends it as a structured field next to the error code. It is `errorArgs` in the camelCase APIs and `meta.args` in the TFE-compatible API. An argument is an identifier, a quantity or a time (an RFC 3339 string in UTC), and never pre-formatted text. The client formats it.
_Avoid_: Error value, error parameter, error detail

**Uncoded error path**:
An error path of an HTTP adapter that answers without a code of the catalog (ADR 276). The path is a branch of a mapping function, an element of a table of errors or an error body. A mapping function is a classify function, or another function that tests an error and names an HTTP status of 400 or more. The catalog is in `internal/errcode`. The known list of each adapter holds the uncoded error paths of today, with a limit, and it only shrinks.
_Avoid_: Legacy error, untyped error

### Operations

**Phone-home**:
Data about an installation, its use or its users that leaves the installation for a destination that the operator did not name (ADR 74). Examples of such destinations are the project, a vendor and any other third party. Examples are usage counts, feature use, crash reports, install pings and version checks that send data to a project server. The test is the destination: who chose it, and does data leave before the operator names it. Opt-in, opt-out and off by default are all phone-home. The server has no phone-home and no code for it.
_Avoid_: Telemetry (alone: OpenTelemetry uses the same word for metrics, logs and traces), analytics, usage data (alone), tracking
_Copy says_: "phone-home" or "usage reporting".

**Observability**:
The metrics, logs, traces, probes and audit log of an installation, for its operator (ADR 74 decision 4). Observability data goes only to a destination that the operator configures, for example a Prometheus scrape, an OpenTelemetry collector or a log sink. Or it stays in the server, for example the run data of jobs, and the operator reads it there. It is not phone-home.
_Avoid_: Telemetry (alone), monitoring data, diagnostics
_Copy says_: "metrics", "logs" or "traces", with the destination that the operator configures.

**Incident**:
An event in a namespace that disrupts a service, with an ID that has the prefix `inc-` (ADR 147). It has a title, a severity from 1 to 4, a state, the times of each step, a commander and links to units and jobs. It closes only with a root cause, a root-cause category and lessons. A person classifies it, reports it and reviews it. The server never decides any of these.
_Avoid_: Ticket, outage, alert

**Classification**:
The record of a person that says whether an incident is major, with the criteria that the person found met (ADR 147 decision 5). Classifications are append-only. The last one is the current classification. The server never classifies an incident.
_Avoid_: Triage, severity (a severity is a different field)

**Commander**:
The person who leads the response to an incident (ADR 147 decision 1).
_Avoid_: Owner, lead

**Cyber threat record**:
A record of a cyber threat in a namespace, with an ID that has the prefix `thr-` (ADR 148 decision 13). It has a title, a description, the time of detection, links to units and notes. A person records whether the threat is significant, with a reason, and each voluntary notice of the threat to an authority. The server never decides that a threat is significant, and never sends a notice. A person closes the record with a note on the outcome. A cyber threat record is not an incident: the threat did not affect a service.
_Avoid_: Threat (alone), alert, vulnerability

## Flagged ambiguities

- **Copy** is every text that a user reads: the console, `README.md`, `docs/guides/`, `docs/reference/`, `docs/explanation/`, CLI output, and the text of API errors and messages. Copy follows ADR 251. Code, API field names, wire values, metrics and logs keep the glossary terms. A `_Copy says_` line gives the word that copy uses instead.

- **Account** is only a label of the hosted console for a namespace (ADR 59). The API, the CLI and error text say "namespace". There is no account object in the server.

- **Group** is a set of principals (ADR 52). RFC 4 used "group" for a set of units before the owner chose "collection". A set of units is a collection.
- **Project** has two meanings. Always write "Pulumi project" or "TFE project".
- **Organization** is the word each tool uses for a namespace. Use it only to name a field or command of a tool's protocol.
- **Lease** and **workspace lock** both stop concurrent writes, but they act differently after a crash. Do not use one word for both.
- **Lease** names the update lease of the Pulumi CLI, the rotation lease of a rotator block, and a lease of Vault (ADR 207). Name which one.
- **Orphan** means a stored file, for example a blob that no row names. For a cloud resource that no unit manages, or that two units manage, write "unmanaged resource" and name its kind.
- **Catalog** is a common noun for a fixed list, for example the catalog of error codes or the tool catalog of the agent. It is not the name of a feature. The request gateway is Self-service (ADR 286). The list of all units is the inventory.
- **Promotion** is a step along a promotion path only. The action that makes a replica installation the primary installation is a failover. A `next` signing key becomes the active key by an activation (ADR 286).
- **Job** and **operation** are different. A job is work that OpenDustpan asks an executor to do on one unit. Each phase of a job is one durable operation (ADR 100). The operation of a job is `plan`, `apply`, `destroy` or `refresh`. Name which one.
- **Migration** means a SQL schema migration only. For the move of a unit into OpenDustpan, write "import".
- **Restore** has three meanings. A restore of a backup replaces the data of a whole installation (ADR 38). A restore of a version makes one older version of a unit its new current state (ADR 28). A restore of a deleted unit brings the unit back before its purge (ADR 47). Name which one.
- **Lock** names the workspace lock of OpenTofu and the unit lock of the inventory. Code also says "stack lock" for the hold that a mutating Pulumi update has on its stack. Do not call a lease a lock.
- **Delta** and **diff** are different. A delta upload saves a checkpoint. A state diff compares two versions.
- **Version** is ambiguous between a stack's update number, a state version and a software release. Name which one.
- **Break-glass** has four meanings. A break-glass account is a standing account of one named person. A break-glass recovery mode is one of the three ways to recover when the last administrator loses every factor (the setting `--recovery-mode`). Host recovery is the path of the host command, not a break-glass account. A break-glass of a job starts a job during an active freeze (ADR 103). A break-glass for gates in error passes the gates whose result is `error` for one change (ADR 224 decision 13). Name which one.
- **Sweep** names the Sweep feature only: a cloud sweep or a state check. The task that purges deleted units is the retention expiry. The delete of key blobs with no record is the orphan cleanup (ADR 151). The Go name `inventory.Sweeper` is older than this rule.
- **Source** has two meanings. The term **Source** is the backend of a unit before an import. A source snapshot and the source filter name the code of a commit for a runner job (ADR 135). The unit setting that names that code is the code location (ADR 100). Never write "source" alone for the code.
- **Snapshot** is the source snapshot only (ADR 135). Do not use it for state, a backup, a checkpoint or a state version.
- **Session** has three meanings: the console session of a person, a job session of a launcher (ADR 133), and an MCP session (ADR 123). Name which one.
- **Job token** and **handoff token** are different. A job token is the access token of a tool for the unit of a job (ADR 100). A handoff token opens one job session (ADR 133).
- **Executor** and **launcher** are different. An executor is the way a job runs: write-back or runners (ADR 100). The launcher runs the steps of one claim (ADR 134).
- **Plan** names seven things. They are the operation `plan` of a job, the phase kind `plan` and a stored plan. They are also the OpenTofu plan, a plan revision, a unit plan (ADR 195) and the import plan (ADR 241). Name which one.
- **Release** has two meanings: a release of the software, and the release of an import, which ends its import hold (ADR 241 decision 15). Name which one.
- **Signature** has three meanings. A webhook delivery has one (ADR 145), a signed audit segment has one (RFC 71), and a change order has signatures (ADR 191). Name which one.
- **Gate** names only the stored rule at a decision point (ADR 224). The confirmation step and the secret consent of an agent are fixed controls, which older ADRs call the confirmation gate and the secret gate. A CI model check (ADR 70 decision 30) is a check, and "gates" as a verb is "needs".
- **Policy** alone names no object. Say gate, access limit, approval rule, source filter, session policy, token policy or resource policy report.
- **Review** has four meanings: a review request of the forge, a break-glass review, an incident review and a change order review (ADR 192). Name which one.
- **Group** is a set of principals. A change group is a set of identical changes in a change order (ADR 194). Write "change group" for the second.
- **Claim** has two meanings. The durable claim of a phase gives one executor the right to run it. The job-ID claim is a field of a principal that a job token authenticates (ADR 100). Name which one.
- **Approval**, **confirmation** and **consent** are different. An approval fills a requirement of an approval rule (ADR 225). A confirmation passes a confirmation step. A consent allows one secret read of an outside agent (ADR 124).
- **Tool** is an infrastructure-as-code CLI. An agent tool is one of the tools of an agent (ADR 122). Write "agent tool" for the second.
- **Key** has many meanings: a data key, an item key, a resource key, a runner key, a blob key and a tag key. Name which one.
- **Scope** has three meanings: the namespace and unit of a data key, an audit scope, and an account scope of a sweep target. Write "account scope" for the third.
- **Holder** has three meanings: the holder of a lock, the holder of an action, and the holder of a resource key. Name which one.
- **Link** has two meanings: a link in the context of an object, and a namespace link. Write "namespace link" for the second.
- **Module** has three meanings: an OpenTofu module, a Go module and an optional module of the server (ADR 211). Write "optional module" for the third.
- **Capability** has four meanings. They are an upload capability, a capability probe (ADR 129), the capability list of the Pulumi CLI and the capabilities document (ADR 211). Name which one.
- **`@server`** is the name of the authorization and audit object of the installation, and of the installation scope of a secret. It is not an action area and not a path. The action area is `installation:`, and no path of the API holds the word `server` (ADR 210).
- **Layer** is a source of the value of a setting (ADR 63). ADR 161 also says "layer" for one object of the context chain. Name which one.
- **Redaction**, **masking** and **purge** are different. A redaction destroys the content of one history item. Masking hides a secret value in a read and keeps it. A purge ends a whole unit.
- **Provider** has two meanings. A provider of OpenTofu or the Pulumi CLI is a plugin of the tool, for example a provider wrapper (ADR 170). A profile provider is an adapter that an open calls (ADR 206). Write "profile provider" or "provider block" for the second.
- **Environment** is the word of the Pulumi CLI for a profile, in the paths of the Pulumi Cloud-compatible API (ADR 209). Everywhere else, write "profile". An environment variable is a variable of a process.
- **Open session** and the other sessions are different: an open session holds the result of one open of a profile (ADR 203).
- **Access** is also a claim of an issued token: `read`, `write`, `rotate` or `open` (ADR 205). An access token authenticates to OpenDustpan. Name which one.
- **Tag** has two meanings: a tag that a tool sends for a unit, and a revision tag of a profile. The key and value pairs of a profile are labels. Name which one.
- **Retention** has three meanings. The first is the retention period of a deleted unit. The second is that of a stored plan or a job log (ADR 152). The third is that of an incident record or a cyber threat record (ADR 148). Name which one.
