# 241. An import is an API resource that the server runs

Status: accepted · 2026-10-19. ADRs 242 to 244 give the other parts. Supersedes in part ADR 29 decisions 1, 5 and 8, and ADR 213 decision 10. Extends ADR 203 decision 1, ADR 224 decision 16, ADR 145 decision 5 and ADR 201 decision 7. Extends the reservations of ADR 115 and the `state.write` fact schema of ADR 226. Answers part of RFC 42 and RFC 66. Accepts RFC 81. The home region amended by ADR 248 (primary installation)

Changed by:

- ADR 248 changes decision 11: resume continues on the new primary
  installation.
- ADR 312 extends: Kubernetes operators as import sources.

## Context

ADR 29 made an import a tested guide. The server got no import engine, no
source credentials and no source adapter. The guide uses the CLI of each
tool, and it loses the kind, the time and the user of each version that it
replays. The Pulumi file backend and the OpenTofu backends move the current
state only.

On 2026-10-17 the project owner decided, with the label "(owner)":

```text
migration tooling, both now: (1) a guide, and (2) an import tool that
brings state AND history from common sources: Pulumi Cloud (works with
the Pulumi CLI), HCP Terraform and TFE-compatible servers, S3 and other
OpenTofu backends; plus (3) the move check: one unit first, then a whole
namespace.
```

In this repository "migration" means a SQL schema migration (GLOSSARY.md).
These ADRs say "import" for the move of a unit into OpenDustpan, "source"
for the backend before the import, and "move check" for the check of a
moved unit.

This ADR records where an import runs, how it gets source credentials, the
import object, and its fence, locks, audit entries, actions and routes.
ADR 242 records what an import moves for each tool, and how it seals state
and secrets. ADR 243 records the source port and its adapters. ADR 244
records the move check, the guide pages and the e2e steps.

A decision of the owner has the label "(owner)". A decision of the product
manager has the label "(product manager)" and always has a "Benchmark:"
line. Each benchmark is a reason for the decision. No benchmark is a claim
that OpenDustpan meets a standard or matches a product.

Facts:

- No tag shipped. ADR 73 applies: no alias and no conversion for a shape
  that no tag shipped.
- Durable operations (ADR 72) survive the stop of an instance. Any instance
  can claim an operation after the claim of another instance expires.
- `pkg/egress` refuses an outbound request to a private, loopback or
  metadata address, unless the egress allowlist of the installation holds
  the address (ADR 129 decision 25). `--egress-public-only` makes the server
  refuse each CIDR entry of that list.
- A manual lock (ADR 213 decision 10) stops each new job and each tool lock
  request on a unit, and names its holder and its reason.
- The idempotency record of a mutating route holds a keyed hash of the
  body, never the body (ADR 210 decision 6).

## Decision

### Where an import runs

1. **The import tool and the guide, both now (owner).** OpenDustpan gets an
   import tool that moves state and history from a source, and the guide of
   ADR 29 stays and grows (ADR 244). Both are user-facing. Each lands with
   its docs page (ADR 214).
2. **An import runs in the server (product manager).** An import is a
   resource of a namespace in the REST API (ADR 17). The server reads the
   source and writes the units in durable operations (ADR 72). The console,
   the `opendustpan import` command and every other client call the same
   routes. No import feature exists only in a CLI.

   The other options and why they lose:
   - A command on the workstation that calls each tool's CLI. This is the
     guide of ADR 29. It cannot keep the original time and actor of a
     version, and it cannot import history from a backend that the CLI
     reads only at its latest version.
   - A job on a runner of the team (ADR 101). An import needs no tool CLI,
     because each source is an HTTP API or an object store. A runner adds a
     pool, a launcher and a claim to a read of an HTTP API.

   Benchmark: GitLab imports a project from GitHub in background jobs of
   the GitLab server, and shows the status of each project. The GitHub
   Enterprise Importer runs each migration on the GitHub side, behind an
   API. `tfmigrate`, `tofu init -migrate-state`, `pulumi stack export` and
   `pulumi stack import`, and the migration tool of HCP Terraform run on the
   workstation of the user. They need no server feature, and they do not
   keep the history of a source.
3. **The upload source for each backend that the server cannot read
   (product manager).** A client can send the state of a unit to an import
   (ADR 243 decision 6), one version at a time, oldest first. The
   `opendustpan import upload` command reads each version with the tool's
   own CLI on the workstation (`tofu state pull`, `pulumi stack export`) and
   sends it. So a source on a network that the server cannot reach, or a
   backend with no adapter (for example GCS, Azure Blob, PostgreSQL,
   Consul or local state), still imports through the same import object,
   with its credentials only on the workstation.

   Benchmark: GitLab imports a project from an export file that the user
   uploads, next to its imports from a remote API.

### Source credentials

4. **An import never stores a source credential (product manager).** The
   import record, the durable operation input, the idempotency record, the
   logs, the metrics, the audit entries and the events never hold a source
   credential or a part of one. A credential reaches the server in one of
   two ways:
   - **A profile of the namespace.** The import names a profile (ADR 200)
     and the path of a login block in it. At each run step, the server opens
     the profile as a direct open of the consumer `import` (ADR 203), and
     the open writes its own audit entry. The consumer `import` behaves like
     `observe`: the credential never leaves the server, and no open session
     stores it. The server checks `profile:open` on the profile for the
     principal of the import at the create, at each `run` and `resume`, and
     at each claim of the durable operation. A failed check pauses the import
     with the reason `import.denied`. The profile can hold a static
     token as a sealed secret, or get a short-lived credential through the
     issuer of the namespace (ADR 204), for example a role at a cloud for an
     S3 bucket. With the issuer, no long-lived credential exists anywhere.
   - **An ephemeral credential.** The `run` and `resume` calls take a
     credential in the request body. The instance that takes the call keeps
     it in memory for that run only. The server never writes it to SQL, to
     the blob store or to any other instance. When the run ends, the
     instance drops it.

   The idempotency record of `run` and `resume` holds a keyed hash of the
   body without its credential field, so the record holds no part of the
   credential.

   An ephemeral credential and a durable operation do not mix. So an import
   with an ephemeral credential runs only on the instance that took the
   call. Its durable operation records the instance. When that instance
   stops or loses its claim, another instance that claims the operation
   finds no credential. It sets the import to `paused` with the reason
   `credential-needed` and does no source request. A `resume` call with the
   credential continues from the last committed version (decision 9).

   Benchmark: the GitHub Enterprise Importer takes the source token in the
   call that starts a migration. GitLab keeps an import token encrypted for
   the duration of the import. Designed against PCI DSS v4.0.1 requirement
   8.6.3 (protect the credentials of system accounts), with no stored copy
   as the first choice. Workload identity federation (ADR 204) follows the
   advice of each large cloud to use short-lived credentials over keys.
5. **The credential has a type and never prints (product manager).** The
   Go type of a credential (ADR 243) has no exported field. Its `Format`,
   `String`, `GoString` and `LogValue` methods answer `[credential]`, so
   every `fmt` verb prints `[credential]`, also `%d`, `%o`, `%b`, `%c` and
   `%U`. Its JSON and text encodings fail. The bytes are behind a func value, so a Credential in an unexported field of another value prints an address, not the bytes. Its `Clear` method writes zeros
   over its bytes, as a best effort: a copy that a request made stays in
   memory until the garbage collector frees it. The canary test of ADR 32 covers each route of this ADR
   and each source adapter.

   Benchmark: the `slog.LogValuer` pattern of the Go standard library for
   values that must not reach a log.
6. **The egress rules check each source address.** Each
   source adapter that the server runs sends its requests through
   `egresshttp`. A private address needs an entry in the egress allowlist
   of the installation. With `--egress-public-only`, the server imports only
   from public addresses, and the upload source (decision 3) is the way for
   a private source. A refused address fails the dry run with the error
   code `import.source-address-refused` and names the allowlist setting.

   - **The address holds no credential.** The address is an absolute URL
     with a host. Its scheme is `https`, or `s3` for the `s3` kind. It has
     no userinfo, no query and no fragment. Else the dry run fails with
     `import.address-invalid` (`unitimport.CheckAddress`). The source of a
     move check (ADR 244 decision 2) follows the same rule.
   - **The address matches the login block.** A login block that an import
     uses names the host of its source in the new block key `sourceHost`
     (ADR 201 decision 7). A change of `sourceHost` is a routing change (ADR
     201 decision 7, ADR 202 decision 2) and needs `profile:open` as well as
     `profile:write`. A login block with no `sourceHost` cannot serve an
     import (`import.source-address-mismatch`). For the `s3` kind,
     `sourceHost` is the S3 endpoint host. The import refuses an address
     with another host with `import.source-address-mismatch`. So a principal
     with `import:run` cannot send the credential of a profile to a host
     that they pick.
   - **Each request, also a URL that the source answers.** A URL in an
     answer of the source (for example a download URL of a state version
     on another host, or the link to the next page) is a new request. It
     goes through `egresshttp` and the same checks. The credential goes
     only to the origin (the scheme, the host and the port) of the address.
     A request to another origin carries no credential. `egresshttp`
     refuses a redirect to another host, so a source that redirects a
     download to object storage on another host can fail. The support
     statement of ADR 244 decision 15 says so.
   - **TLS.** The adapters always check the TLS certificate of the source.
     No option turns the check off. A private certificate authority comes
     through the trust store of the system. `egresshttp` sets
     `Proxy: nil`, so an installation that reaches the internet only
     through an outbound proxy cannot import from a remote source. The
     upload source is then the way.
   - **An installation with more than one tenant (product manager).** The
     egress allowlist covers the whole installation. So on an installation
     with more than one tenant (ADR 215, ADR 220), an entry that one
     feature or one tenant needs is also a source address that each import
     can reach. On such an installation, an import refuses each allowlist
     entry (a host name, a `*.` suffix or a CIDR range), and so reaches
     public addresses only, unless the operator turns on allowlist entries
     for imports with `--import-allow-allowlist-entries`. A refused address
     fails with `import.source-address-refused`.

     Benchmark: GitLab has a setting that refuses requests to the local
     network from webhooks and integrations, and an administrator can
     allow them.

### The import object

7. **The objects.** An **import** belongs to one namespace. It names one
   source (a source kind, an address and the selection of units), the
   credential way of decision 4, the options of ADR 242, and the target
   names. It holds one **import unit** for each unit that it moves. An
   import unit holds a ledger: one row for each source version, with the
   source version ID, the source digest, the target history item ID and
   the time of the commit. An import moves at most 1000 units. A larger
   source needs several imports.

   States of an import: `planning`, `planned`, `running`, `paused`,
   `holding`, `released`, `failed` and `canceled`. States of an import
   unit: `planned`, `skipped`, `running`, `paused`, `imported`, `failed`
   and `canceled`. One transition table in `pkg/unitimport` decides each
   transition, as `job.Next` does for jobs (ADR 100 decision 7).

   **Failed and partial units (product manager).** When each import unit
   ends and at least one unit committed a version, the import goes to
   `holding`, also when other units failed. Each import unit shows its own
   state, its error code and its count of committed versions. A failed
   import unit with committed versions is **partial**. The import goes to
   `failed` only when no import unit committed a version. A release
   (decision 15) soft-deletes each failed or partial target unit (ADR 47),
   and keeps the history of each imported unit.

   Benchmark: GitLab and the GitHub Enterprise Importer show the result of
   each project or repository of a group migration, and the migration ends
   with the failed ones listed, not as one failure.
8. **A dry run comes first (product manager).** A create of an import is
   the dry run. The server reads the source and writes the plan of the
   import: each unit with its target name, its count of versions, its bytes,
   its encryption, and the three classes of ADR 242 decision 1, and each
   finding of the move check that the server can give before the move (ADR
   244 decision 6). The plan also gives the capabilities of the source (ADR
   243 decision 3) and each request that a person cannot see in the source.
   The dry run sends only read requests to the source, and one sample
   decrypt for each unit under the service secrets provider (ADR 242
   decision 8). A sample decrypt is a POST that changes nothing in the
   source. A source that refuses it fails that unit in the plan with
   `import.source-decrypt-refused`, before the run. The dry run writes
   nothing to a unit. The import is then `planned`. A `run` starts the move.
   A `run` on an import whose plan is older than 24 hours answers 409 with
   the error code `import.plan-stale`. A new dry run on the same import
   refreshes the plan.

   Benchmark: `terraform plan` and `pulumi preview` before a change.
   `tfmigrate plan` before `tfmigrate apply`. AWS Database Migration
   Service runs a premigration assessment before a task.
9. **Idempotent and resumable.** The create, `run`, `resume`, `cancel` and
   `release` calls take an `Idempotency-Key` (ADR 210 decision 6). The
   server writes one source version, its target history item and its
   ledger row in one fenced transaction. So a crash leaves a version either
   committed with its ledger row or absent. A `resume` and each claim after
   a crash start at the first version of each import unit with no ledger
   row. When the source still holds a version that the ledger holds, but
   with another digest, the import unit fails with the error code
   `import.source-history-changed`, and nothing more of that unit moves.
   A second `run` of an import that moved everything moves nothing.
10. **Into a new unit only (product manager).** An import creates each
    target unit. A target name that a unit of the namespace already holds,
    deleted units included (ADR 47), makes that import unit `skipped` with
    the error code `import.target-exists`. A unit that the hold of the same
    import covers is not a conflict: the create of the target unit is
    idempotent (decision 12). An import never merges history into a unit
    that has its own history.

    Benchmark: `pulumi stack import` of a full history and the state
    version replay of ADR 29 both need an empty target to keep the order of
    versions. GitLab imports into a new project only.

### Fence, locks and the source

11. **Every write carries the fence.** Each write of an import is a write in
    its namespace. It carries the `namespace.Fence`, and the store checks it
    in the same transaction (ADR 4). A failover of the namespace makes each
    later write of the import fail with the fence error. The import goes to
    `paused` with the reason `fenced`, and a `resume` on the new home
    region continues from the ledger.
12. **The import hold: a manual lock on each target unit.** In the
    transaction that creates a target unit, the import puts a manual lock
    (ADR 213 decision 10) on it. The holder is the principal that ran the
    import, and the reason is `import <import ID>`. So no job starts on the
    unit, no OpenTofu lock request succeeds and no Pulumi update starts
    while the import moves its history. The writes of the import itself do
    not go through a tool lock, so the lock does not stop them.

    - **Only the import ends the hold.** Only the release (decision 15) or
      the cancel (decision 16) of the import deletes its manual lock. The
      lock route answers 409 with the error code `import.hold` for the
      manual lock of an import, also for its holder and for a person with
      `unit:manage`. The message names the import and its `cancel` and
      `release` routes. This changes ADR 213 decision 10 for the manual lock
      of an import only.
    - **The write checks the hold.** `Tool.Write` checks, in its
      transaction, that the hold `import <import ID>` still covers the unit.
      If not, the write fails, and the import unit goes to `paused` with
      the error code `import.hold-removed`.
    - **No other write while the hold covers a unit.** While the hold covers
      a unit, each route that writes state, renames or deletes the unit
      answers 423 and names the import. This covers the unlocked override of
      the HTTP backend (ADR 44, `unit:write-unlocked`), a forced write (ADR
      50), a rollback, a restore, a rename and a delete. `Tool.Write` fails
      on a deleted unit with `import.target-deleted`.
    - **The create is idempotent.** `Tool.Create` creates the unit, puts the
      hold on it, records the unit as the target of its import unit and
      publishes the unit to the inventory (decision 18), in one fenced
      transaction. When the name exists and the hold of the same import
      covers it, `Tool.Create` answers that unit. So a resume after a crash
      between the create and the next step continues with its own target
      and does not skip it.
13. **The import does not change the source by default (product
    manager).** The import sends only read requests to the source. It never
    deletes in the source. For a source that has a lock API, the option
    `lockSource` (off by default) makes the import lock each source unit
    during its move and unlock it after. A lock that the source refuses
    fails that import unit with the error code `import.source-lock-refused`.

    Benchmark: ADR 29 decision 7, and the read-only token that most teams
    can get for a source without an approval.
14. **The writer check.** At the start of each import unit, the server
    records the newest version of the source unit. After the last version
    commits, it reads the newest version again. When the source has new
    versions, the import moves them too, for at most three rounds. When the
    source still changes after three rounds, the import unit fails with the
    error code `import.source-changing`. The message says to stop each
    pipeline that writes to the source.

### Cutover

15. **The source stays the record until a release (product manager).**
    When each import unit ends, the import goes to `holding`. It keeps
    its manual locks. While it is `holding`, a `resume` moves each new
    version of each source unit, so the team can bring the target up to
    date just before the switch. A `resume` while `holding` that moves new
    versions meets the gate again (decision 23) and reserves the quota for
    the new versions (decision 24). A `release` call soft-deletes each
    failed or partial target unit (decision 7), deletes the manual locks,
    settles the quota reservation and sets the import to `released`. After the release, the team points
    its CLIs and runners at OpenDustpan. A `resume` after the release
    answers 409 with the error code `import.released`, because the target
    units now have their own history.

    Benchmark: AWS Database Migration Service copies the full data, then
    copies the ongoing changes until the cutover. GitLab direct transfer
    and the GitHub Enterprise Importer leave the source in place.
16. **Cancel (product manager).** A `cancel` stops the run at the next
    version. It purges each target unit that the import created and never
    released, with its history items and its ledger rows. These units hold
    only copies, and the import did not change the source, so the purge
    loses no data.
    So a new import into the same names does not fail with
    `import.target-exists`. The cancel sets the import to `canceled` and
    deletes its manual locks in one fenced transaction. A durable operation
    then purges the units. A `Tool.Write` in flight then fails with
    `ErrHoldRemoved`, and the step ends with no transition of a canceled
    unit. The cancel frees the quota reservations (decision 24). It never
    changes the source. A `cancel` after a release answers 409 with the
    error code `import.released`.

    Benchmark: a stopped task of AWS Database Migration Service changes
    nothing in the source, and a new task can load into empty tables
    again.

### Audit, inventory and events

17. **Audit entries.** The routes of this ADR write their entries through
    the middleware of ADR 60. The background work writes these entries, of
    the kind `change`, in the audit scope of the namespace:
    - one entry when an import unit commits its first version, with the
      object the target unit reference, the action `import:run`, the import
      ID, the source kind and the source host,
    - one entry when an import unit ends, with its state, its count of
      versions and the count of secret values that the import re-encrypted
      (ADR 242 decision 8),
    - one entry for each manual lock and each removal (ADR 213 decision 10).

    An entry never holds a credential, a secret value, a path or a query of
    a source URL, or a value of the state. The source host alone names the
    source.
18. **The inventory.** Each tool package publishes each target unit to the
    inventory in the transaction of `Tool.Create`, and again in the
    transaction of each `Tool.Write` (ADR 13). So a held unit with no
    version yet is in the inventory, and the quotas of ADR 115 count it.
    The inventory item of the unit gets the field `importID` while the
    import holds the unit. The inventory change log (ADR 150
    decision 16) records the unit at the time of the import, not at the
    time of the source versions. So the inventory at a time before the
    import does not show the unit. The view of the unit at a time (ADR 150
    decision 12) uses the source time of each item (ADR 242 decision 3).
19. **Events.** The event catalog of ADR 145 gets `import.planned`,
    `import.unit-failed`, `import.holding`, `import.released` and
    `import.failed`, so a team can get a notice when a long import ends.
    This extends the catalog of ADR 145 decision 5.

### Actions, routes and the CLI

20. **Actions (product manager).** The new area is `import`. Each action has
    the namespace as its object:
    - `import:read`: list and read imports, their units, their ledgers and
      their plans.
    - `import:run`: create (the dry run), `run`, `resume` and upload.
    - `import:cancel`: cancel.
    - `import:release`: release.

    A `run` and a `resume` also need `unit:manage`, `unit:write` and
    `unit:lock` on the reference of each target unit, because the import
    hold is a manual lock (ADR 213 decision 10). With a profile credential,
    they also need `profile:open` on the profile (decision 4). The server
    checks them at the `run`, and again before the first write of each
    import unit. An import unit whose
    check fails is `skipped` with the error code `import.denied`, and the
    other units go on. The built-in role `admin` has each `import` action.
    The role `maintainer` has `import:read`.

    Benchmark: AWS IAM separates the actions of AWS Database Migration
    Service (`dms:CreateReplicationTask`, `dms:StartReplicationTask`,
    `dms:StopReplicationTask`) from the actions on the target data.
21. **Routes.** Under `/api/dustpan/v1/namespaces/{namespace}` (ADR 210):

    | Route | Action |
    | --- | --- |
    | `POST imports` | `import:run` (the dry run) |
    | `GET imports`, `GET imports/{id}` | `import:read` |
    | `GET imports/{id}/units`, `GET imports/{id}/units/{unit}` | `import:read` |
    | `GET imports/{id}/units/{unit}/ledger` | `import:read` |
    | `POST imports/{id}/plan` | `import:run` (a new dry run) |
    | `POST imports/{id}/run`, `POST imports/{id}/resume` | `import:run` |
    | `PUT imports/{id}/units/{unit}/uploads/{sequence}` | `import:run` |
    | `POST imports/{id}/cancel` | `import:cancel` |
    | `POST imports/{id}/release` | `import:release` |

    The declaration of the `run` and `resume` routes says that their body
    can hold a credential, so the canary test checks that no log line, no
    audit entry and no idempotency record holds it. The routes of the move
    check are in ADR 244, and two of them also take a credential (ADR 244
    decision 10).

    The upload route takes the bytes of one version as its body, and the
    source fields of the version in headers: `Dustpan-Source-At` (an RFC
    3339 time), `Dustpan-Source-Actor` (text) and
    `Dustpan-Source-Metadata` (the tool metadata as base64 JSON, for
    example the kind of a Pulumi update). Each of these values is a claim of
    the client. The history item marks them "reported by the client" (ADR
    242 decision 3), and the plan of the upload source says so.

    A `GET` of a purged import (decision 25) answers 404 with the error code
    `import.purged`. The ledger route still answers the ledger rows of a
    unit after the purge of its import.
22. **The CLI.** `opendustpan import` is a client command (ADR 39). It
    calls the routes above. Its subcommands are `plan`, `run`, `resume`,
    `status`, `upload`, `check`, `release` and `cancel`. It reads an
    ephemeral credential from an environment variable or from standard
    input, never from a flag, as ADR 23 decision 1 requires.

### Gates, quotas and retention

These decisions close RFC 81 questions 1, 2 and 7.

23. **An import meets `state.write` once for each import unit (product
    manager).** Before the first write of each import unit, the server
    evaluates the gates of `state.write` (ADR 224 decision 16) with the
    `operation.kind` value `unit-import`. It is not `import`, which is the
    kind of a Pulumi update, and not `state-import`, which is the kind of a
    `pulumi stack import`. The facts get an `import` object with the source
    kind, the source host, the count of versions, the bytes and the target
    unit. The `import` object is a new minor version of the fact schema of
    `state.write` (ADR 226 decision 2). A `deny` makes that import unit
    `skipped` with `import.denied`. An approval outcome refuses the unit as
    at every `state.write` (ADR 224 decision 16): the import unit is
    `skipped` with `import.denied`, and the message names the gate and the
    decision record. A `resume` while `holding` that moves new versions
    evaluates the gates again for each unit that gets new versions. This
    extends the table of ADR 224 decision 16.

    Benchmark: `tfmigrate` plans and applies one migration file as one
    change, not one change for each state version. The GitLab project
    import runs one import for each project, not one for each relation.
24. **The dry run reports, the run reserves (product manager).** The dry
    run reports the units and the bytes against each quota of the namespace
    (ADR 115). The `run` reserves them before the first write, and fails
    with the quota error of ADR 115 when a quota is short. A `resume` while
    `holding` reserves again for the new versions. The upload source
    reserves at each upload, because its dry run has no bytes yet. The
    import keeps the IDs of its reservations as a list. A `cancel` frees
    each, and a `release` settles each.

    Benchmark: the GitLab project import refuses an export file above the
    maximum import size before the import starts. `tfmigrate plan` shows the
    whole change before `tfmigrate apply`.
25. **Retention (product manager).** The ledger rows of a unit stay as long
    as the unit, and a purge of the unit (ADR 47) deletes them. The import
    record and its plan stay for 400 days after the release or the cancel.
    The purge of an import record keeps each ledger row and the `importID`
    of each history item. No foreign key cascades from the import record to
    them. A `GET` of a purged import answers `import.purged`. The audit
    entries of the import stay under the retention of the audit log (ADR
    68). The tables `imports`, `import_units` and `import_ledger` are in
    `backup.Tables`.

    Benchmark: GitLab keeps the import status of a project after the
    import ends. 400 days is more than the 12 months that PCI DSS v4.0.1
    requirement 10.5.1 asks for an audit history.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it
replaces:

- **ADR 29, decision 1:** "The server gets no import engine, no source
  credentials and no source adapter." The server now runs imports, with the
  credential rules of decision 4 and the adapters of ADR 243. The guide
  stays, as decision 1 says.
- **ADR 29, decision 5:** "The original kind, time and user of a version are
  lost." An import through the API keeps them (ADR 242). The guide path of
  ADR 29 still loses them.
- **ADR 29, decision 8:** "No new server code." The import tool is server
  code.
- **ADR 213, decision 10:** the holder of a manual lock, or a person with
  `unit:manage` on the object of the lock, can end it. For the
  manual lock of an import, only the release or the cancel of the import
  ends it (decision 12).

This ADR also extends:

- **ADR 203, decision 1:** the list of consumers gets the consumer
  `import`, which behaves like `observe` (decision 4).
- **ADR 224, decision 16:** the table gets the `operation.kind` value
  `unit-import` (decision 23).
- **ADR 145, decision 5:** the catalog gets the types of decision 19.
- **ADR 201, decision 7:** the block key `sourceHost`.

## Consequences

- Open work, in this order. Each step lands with its e2e step first (ADR
  12), its actions in the route table (ADR 17) and its docs page (ADR 214):
  1. `pkg/unitimport` with its ports, its transition table and the
     conformance suites (ADR 243), and the store adapters for SQLite and
     PostgreSQL.
  2. The import routes, the dry run and the upload source.
  3. The adapter for the TFE-compatible API, then the adapter for the
     Pulumi Cloud-compatible API, then S3, then the OpenTofu `http`
     backend (ADR 243 decision 7).
  4. The move check for one unit, then for a namespace (ADR 244).
  5. The CLI command and the guide pages (ADR 244).
- The import is the first server feature that reads from another product
  over the network. Its adapters must follow the protocol of each source as
  the source answers it today. The e2e suite drives each adapter against
  OpenDustpan as the source (ADR 244 decision 11).
- An import with an ephemeral credential does not survive the stop of its
  instance. It pauses, and a person or a script must call `resume` with the
  credential. A profile with the issuer of the namespace avoids this.
- `notify.Types` gets the types of decision 19, so the reference page of
  the catalog changes with them (ADR 145 decision 5).
- `pkg/gate` gets `GroupImport` in `GroupsOf(PointStateWrite)` with
  `available` (false for every other operation kind, ADR 226 decision 3),
  the field `Facts.Import` (`available`, `sourceKind`, `sourceHost`,
  `versions`, `bytes`, `importID`), `OperationUnitImport`, and a Producer
  with its conformance case. The schema becomes `state.write/1.1`.
