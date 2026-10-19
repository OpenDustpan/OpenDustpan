# 241. An import is an API resource that the server runs

Status: accepted · 2026-10-19. ADRs 242 to 244 give the other parts. Supersedes in part ADR 29 decisions 1, 5 and 8. Answers part of RFC 42 and RFC 66. RFC 81 holds the open questions

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
     the open writes its own audit entry. The profile can hold a static
     token as a sealed secret, or get a short-lived credential through the
     issuer of the namespace (ADR 204), for example a role at a cloud for an
     S3 bucket. With the issuer, no long-lived credential exists anywhere.
   - **An ephemeral credential.** The `run` and `resume` calls take a
     credential in the request body. The instance that takes the call keeps
     it in memory for that run only. The server never writes it to SQL, to
     the blob store or to any other instance. When the run ends, the
     instance drops it.

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
   Go type of a credential (ADR 243) has no exported field. Its `String`,
   `GoString` and `LogValue` methods answer `[credential]`, and its JSON
   encoding fails. The canary test of ADR 32 covers each route of this ADR
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
8. **A dry run comes first (product manager).** A create of an import is
   the dry run. The server reads the source and writes the plan of the
   import: each unit with its target name, its count of versions, its
   bytes, its secrets provider, and the three classes of ADR 242
   decision 1 (ADR 242), and each finding of the move check that the server can
   give before the move (ADR 244 decision 6). The dry run sends only read
   requests to the source. It writes nothing to a unit. The import is then
   `planned`. A `run` starts the move. A `run` on an import whose plan is
   older than 24 hours answers 409 with the error code `import.plan-stale`.
   A new dry run on the same import refreshes the plan.

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
    the error code `import.target-exists`. An import never merges history
    into a unit that has its own history.

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
    date just before the switch. A `release` call deletes the manual locks
    and sets the import to `released`. After the release, the team points
    its CLIs and runners at OpenDustpan. A `resume` after the release
    answers 409 with the error code `import.released`, because the target
    units now have their own history.

    Benchmark: AWS Database Migration Service copies the full data, then
    copies the ongoing changes until the cutover. GitLab direct transfer
    and the GitHub Enterprise Importer leave the source in place.
16. **Cancel.** A `cancel` stops the run at the next version. It soft-deletes
    each target unit that the import created and that the import has not
    released (ADR 47), so a restore within the retention period gets it
    back. It never changes the source. A `cancel` after a release answers
    409 with the error code `import.released`.

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
    inventory in the same transaction as the write of its first version
    (ADR 13). The inventory item of the unit gets the field `importID`
    while the import holds the unit. The inventory change log (ADR 150
    decision 16) records the unit at the time of the import, not at the
    time of the source versions. So the inventory at a time before the
    import does not show the unit. The view of the unit at a time (ADR 150
    decision 12) uses the source time of each item (ADR 242 decision 3).
19. **Events.** The event catalog of ADR 145 gets `import.planned`,
    `import.unit-failed`, `import.holding`, `import.released` and
    `import.failed`, so a team can get a notice when a long import ends.

### Actions, routes and the CLI

20. **Actions (product manager).** The new area is `import`. Each action has
    the namespace as its object:
    - `import:read`: list and read imports, their units, their ledgers and
      their plans.
    - `import:run`: create (the dry run), `run`, `resume` and upload.
    - `import:cancel`: cancel.
    - `import:release`: release.

    A `run` and a `resume` also need `unit:manage` and `unit:write` on the
    reference of each target unit. The server checks them at the `run`, and
    again before the first write of each import unit. An import unit whose
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
    | `POST imports/{id}/plan` | `import:run` (a new dry run) |
    | `POST imports/{id}/run`, `POST imports/{id}/resume` | `import:run` |
    | `PUT imports/{id}/units/{unit}/uploads/{sequence}` | `import:run` |
    | `POST imports/{id}/cancel` | `import:cancel` |
    | `POST imports/{id}/release` | `import:release` |

    The declaration of the `run` and `resume` routes says that their body
    can hold a credential, so a test checks that no log line, no audit
    entry and no idempotency record holds it. The routes of the move check
    are in ADR 244.
22. **The CLI.** `opendustpan import` is a client command (ADR 39). It
    calls the routes above. Its subcommands are `plan`, `run`, `resume`,
    `status`, `upload`, `check`, `release` and `cancel`. It reads an
    ephemeral credential from an environment variable or from standard
    input, never from a flag, as ADR 23 decision 1 requires.

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
- Which gate an import meets (ADR 224) is open in RFC 81 question 1. The
  routes of decision 21 do not ship before it has an answer.
- `notify.Types` gets the types of decision 19, so the reference page of
  the catalog changes with them (ADR 145 decision 5).
- RFC 81 holds the questions that need the owner.
