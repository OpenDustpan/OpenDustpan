# 244. The move check, the guide pages and the e2e steps

Status: accepted · 2026-10-19. Part of ADR 241. Supersedes in part ADR 213 decision 10. Extends ADR 29 decision 6. Accepts RFC 81 question 4. Answers part of RFC 66

## Context

The owner asked for a move check, "one unit first, then a whole namespace"
(ADR 241 decision 1). RFC 66 asks what help a person needs to move a unit,
and lists the parts of a unit that an export and an import may not carry
over: access grants, bound profiles, secrets, references between units,
labels and groups, history, schedules, triggers and policies. It asks if the
check is a command or a REST API, and if it runs on one unit or on a whole
namespace.

ADR 29 decision 6 checks an import by hand: the exports match, and `up` or
`plan` shows no changes. This ADR makes that check a feature with an API,
and adds the guide pages and the e2e steps for ADRs 241 to 243.

Facts:

- A manual lock makes the start of a Pulumi update fail, and makes an
  OpenTofu lock request fail (ADR 213 decision 10). `pulumi preview` starts
  an update of the kind `preview`. `tofu plan` takes the lock unless it runs
  with `-lock=false`.
- `tofu plan -json` writes one `change_summary` message with the counts of
  resources for each kind of change.
- `pulumi preview --expect-no-changes` exits with an error when the preview
  finds a change. The engine events of the preview reach the server.
- A plan job with no change ends `succeeded` (ADR 213 decision 3).

## Decision

### What the check is

1. **One unit first, then a whole namespace (owner).** The move check for
   one unit lands first. The check for a whole namespace lands after it, and
   uses the check of each unit.
2. **A move check is an API resource (product manager).** A move check
   belongs to a namespace. It compares target units in the namespace with
   their source units. Its scope is one unit or a namespace. An import makes
   one move check of the scope namespace when it goes to `holding`. A person
   can also make one at any time, for example for a unit that the CLI path of
   the guide moved. A move check without an import needs a source and a
   credential, with the rules of ADR 241 decisions 4 and 6: the address
   holds no userinfo, no query and no fragment
   (`import.address-invalid`), its host matches the login block of the
   profile (`import.source-address-mismatch`), and the principal has
   `profile:open` on the profile.

   Benchmark: `tfmigrate plan` runs a plan after the change of state and
   fails when the plan has changes. GitLab shows the result of each relation
   of a project import, and then of each project of a group import.

### The check of one unit

3. **The facts.** The `Tool` port (ADR 243 decision 4) reads the same facts
   from the source bytes and from the latest target item:
   - **Pulumi CLI:** the masked digest (the SHA-256 of the deployment in
     canonical JSON, with each secret value replaced by the marker of its
     value path, as in ADR 41 decision 2, and with no
     `secrets_providers.state`, which the import rewrites, ADR 242 decision
     8), the count of resources, the count of secret values and the
     secrets provider.
   - **OpenTofu:** the SHA-256 and the MD5 of the bytes, the serial, the
     lineage, the count of resource instances and the names of the outputs.
   - **Client-encrypted OpenTofu state:** the SHA-256 and the MD5 of the
     bytes only.
4. **The steps.** Each step answers `passed`, `failed`, `warning`,
   `pending` or `skipped`:

   | Step | Passes when |
   | --- | --- |
   | `present` | the target unit exists and is not deleted |
   | `state-digest` | the digest facts of decision 3 are equal |
   | `resource-count` | the counts are equal |
   | `secrets` | the counts of secret values are equal. With a source that decrypts, each plain value of the source equals the plain value of the target. The check compares them in memory, with an HMAC under a random key of that check, and keeps no value. |
   | `history` | each selected source version has a ledger row whose source digest still matches |
   | `source-unchanged` | the newest version of the source is the version that the import moved last |
   | `no-change-plan` | evidence of decision 5 shows no change |
   | `carry-over` | no finding of decision 6 is an error. A finding that is a warning makes the step `warning`. |

   The status of the unit is the worst status of its steps, in the order
   `failed`, `pending`, `warning`, `passed`. A `skipped` step does not count.
   The report names the reason of each `skipped` step, for example "the
   source has no decrypt" for the plain-value part of `secrets`.
5. **The plan runs with the user's own CLI, or in a job (product
   manager).** The server cannot run a plan with the code of the team on its
   own. So the `no-change-plan` step is `pending` until evidence comes from
   one of three ways. The report names the way:
   - **An observed preview.** The person runs
     `pulumi preview --expect-no-changes` against the target stack. The
     server reads the resource changes of that preview from its engine
     events. Any change other than `same` fails the step.
   - **A client report.** The person runs `tofu plan -json -lock=false`
     against the target workspace, and sends the `change_summary` message to
     the evidence route. `opendustpan import check --plan` runs the command
     and sends the message. The report says "reported by the client",
     because the server did not see the plan. A client can forge a report,
     and a forced release (decision 11) can follow a `pending` check. The
     report labels the evidence, and `move-check.md` says that a client
     report is the claim of the person who sent it.
   - **A plan job.** When the unit has a code location and a runner pool
     (ADR 100), the person can ask the check to start a plan job. A job
     with no change passes the step (ADR 213 decision 3).

   Benchmark: `tfmigrate plan`, and the check of ADR 29 decision 6.
6. **Carry-over findings.** A finding names a part of the source unit or of
   its setting in OpenDustpan that the move did not carry over. Each finding
   has a code, a severity (`error` or `warning`), a message and the command
   or the route that helps. The first codes:

   | Code | Severity | When |
   | --- | --- | --- |
   | `carry.config-file` | warning | a Pulumi stack has secret config, and the import did not rewrite it (ADR 242 decision 9) |
   | `carry.secrets-provider` | warning | a Pulumi stack keeps a passphrase or a cloud KMS provider (ADR 242 decision 10) |
   | `carry.client-encryption` | warning | the state is client-encrypted (ADR 242 decision 14) |
   | `carry.reference` | error | the state reads another unit (a Pulumi `StackReference` or an OpenTofu `terraform_remote_state`) that is not in the namespace |
   | `carry.not-moved` | warning | the source has a part that ADR 242 lists as lost, for example variables, schedules or team access |
   | `carry.access` | warning | the target unit has no grant other than the grants of its namespace and collections. The finding names the users and teams of the source where the source gives them, as text (decision 16) |
   | `carry.profile` | warning | the source has variables or config, and the target unit has no profile attachment (ADR 203) |
   | `carry.terraform-version` | warning | ADR 242 decision 15 |

   The dry run of an import gives the findings that it can find before the
   move (ADR 241 decision 8). The move check gives them again after the
   move.
7. **A new unit-bound feature adds its finding (product manager).** The
   list of findings is code in `pkg/unitimport`. A test lists each table of
   the backup table list (`backup.Tables`) that holds a unit ID, and fails
   when the table has no finding and no entry in an explicit list of tables
   that need none. The import tables `imports`, `import_units` and
   `import_ledger` (ADR 241 decision 25) are on that list: they record the
   move itself, and the ledger rows stay with the unit. So a new feature that binds to a unit cannot ship
   without a decision on the move check.

   Benchmark: the route test of ADR 17, which fails for a route with no
   declared action.

### The check of a namespace

8. **The namespace check.** A move check of the scope namespace runs the
   check of each unit of its import, or of each unit of a source selection.
   It adds:
   - `missing`: a source unit of the selection with no target unit,
   - `extra`: a unit of the import that the source no longer has,
   - the `carry.reference` findings between the units of the namespace,
     so a reference to a unit that the same import moved passes.

   The status of the namespace is the worst status of its units. The
   report gives the count of units for each status. A namespace check of at
   most 1000 units runs as one durable operation, at most 4 units at a time.

### The report and the API

9. **The report.** `GET .../move-checks/{id}` answers one JSON document: the
   scope, the import ID, the status, the time, and for each unit its
   reference, its status, its steps (each with the name, the status, the
   expected value, the actual value and a message) and its findings. A
   value of a step is never a secret value. For `secrets`, the values are
   counts. `opendustpan import check` prints the report as a table and exits
   with a non-zero code when the status is `failed` or `pending`.
10. **Actions and routes (product manager).** The new area is `movecheck`,
    with the namespace as its object:
    - `movecheck:read`: read move checks.
    - `movecheck:run`: create a move check, send evidence and start a plan
      job.

    A check of a unit also needs `unit:read` on the unit. The plain-value
    part of `secrets` needs `unit:read-secrets` on the unit, and writes one
    audit entry of the kind `tool-operation` (ADR 41 decision 5) with the
    count of values. A plan job needs `unit:run-plan`. The built-in roles
    `maintainer` and `admin` have both `movecheck` actions.

    | Route, under `/api/dustpan/v1/namespaces/{namespace}` | Action |
    | --- | --- |
    | `POST move-checks` | `movecheck:run` |
    | `GET move-checks`, `GET move-checks/{id}` | `movecheck:read` |
    | `POST move-checks/{id}/evidence` | `movecheck:run` |
    | `POST move-checks/{id}/plan-jobs` | `movecheck:run` and `unit:run-plan` |
    | `POST move-checks/{id}/rerun` | `movecheck:run` |

    The declaration of `POST move-checks` and `POST move-checks/{id}/rerun`
    says that their body can hold a credential (ADR 241 decision 21). The
    canary test covers both routes. Their idempotency record holds a keyed
    hash of the body without its credential field.
11. **A release needs a passed check, unless forced (product manager).** A
    `release` of an import (ADR 241 decision 15) answers 409 with the error
    code `import.check-not-passed` when the latest move check of the import
    is `failed` or `pending`. A `release` with `force: true` and a reason
    (1 to 500 characters) releases anyway, and its audit entry holds the
    reason and the status of the check.

    Benchmark: a `tfmigrate` migration fails when the plan has changes,
    unless the migration file sets `force`.
12. **A preview under the import hold (supersedes in part ADR 213 decision
    10).** While
    the manual lock of an import covers a unit, the Pulumi Cloud-compatible
    API accepts the start of an update of the kind `preview` on that unit
    from a principal with `unit:run-plan`. It refuses every other kind with
    the error of the manual lock. A preview writes no state. An OpenTofu
    plan with `-lock=false` needs no change, because it takes no lock.

### The guide pages

13. **Pages in `docs/guides/` (product manager).** Each page lands with the
    feature that it documents (ADR 214):
    - `import.md`: what an import is, the choice between the import tool
      and the CLI path, the safety rules, the dry run, the hold and the
      release, and the support statement. It replaces the page of today.
    - `import-pulumi.md`: the Pulumi Cloud-compatible source, the Pulumi DIY
      bucket, the upload of a file backend, the config rewrite, the secrets
      provider, and the CLI path of ADR 29.
    - `import-opentofu.md`: the TFE-compatible source, the S3 and `http`
      sources, the upload for every other backend, client-side encryption,
      and the CLI path of ADR 29 with the replay script.
    - `move-check.md`: the check of a unit and of a namespace, each step,
      each finding with the command that helps, and the evidence of a plan.

    `docs/reference/import.md` gets the source kinds, the options, the
    table of what moves (verbatim, mapped, lost) for each source kind, and
    each error code. `docs/guides/migrate-out.md` stays, and links the move
    check for a move to another OpenDustpan installation.

    Benchmark: the Diátaxis split that `docs/` already uses: guides for
    tasks, reference for facts.

### The e2e steps

14. **The e2e steps (ADR 12).** The e2e suite gets these steps, on Linux,
    for each Pulumi CLI and OpenTofu version of its matrix, on SQLite and on
    PostgreSQL. A second OpenDustpan server S is the source, and server D is
    the target, as in ADR 29. Server S serves TLS with a test CA in the
    trust store of server D, because an import accepts only `https` and `s3`
    addresses:
    1. **TFE-compatible API.** Real `tofu apply` writes three state versions
       to S. An import into D keeps the serial, the lineage, the MD5 and the
       time of each one. Real `tofu plan -json -lock=false` against D sends
       evidence, and the move check passes. A stop of D in the middle of the
       run, then `resume`, moves the rest with no duplicate. A second `run`
       moves nothing. S receives only `GET` requests, and with `lockSource`,
       a lock and an unlock.
    2. **Pulumi Cloud-compatible API.** Real `pulumi up` writes three
       updates with a secret config value and a secret output to S. An
       import into D keeps the kind, the message and the times of each
       update. After `pulumi logout` from S, the secret output and the
       rewritten config decrypt on D. `pulumi preview --expect-no-changes`
       against D passes the `no-change-plan` step. The audit log of S shows
       the `batch-decrypt` calls.
    3. **S3.** Against an S3-compatible server with bucket versioning on,
       real `tofu apply` with `backend "s3"` writes three versions, and an
       import moves all three. Real `pulumi up` with an `s3://` login writes
       two updates, and an import moves both with the passphrase provider.
       `plan` and `preview` show no changes.
    4. **OpenTofu `http` backend.** The current state moves, and `plan`
       shows no changes.
    5. **Upload.** `opendustpan import upload` moves local OpenTofu state and
       a Pulumi file backend stack. Client-encrypted state moves as bytes,
       and `plan` with the same `encryption` block shows no changes.
    6. **Errors.** A wrong source token fails the dry run with
       `import.source-denied`. A private source address with no allowlist
       entry fails with `import.source-address-refused`. A stop of the
       instance of an ephemeral credential gives `paused` with
       `credential-needed`. A target name that exists gives `skipped`. An
       address with userinfo fails with `import.address-invalid`. Real
       `tofu apply -lock=false` with the unlocked override against a unit
       that the import hold covers gets 423, and the message
       names the import. No output, log line or audit entry holds a canary
       credential.
    7. **Namespace check.** Three units, one with a `StackReference` to a
       stack that the import did not move, give one `carry.reference` error
       and two passed units. An import of the third stack, and a `rerun`,
       pass all three.
15. **What the docs say about support.** The docs page says what the e2e
    suite drove: a Pulumi Cloud-compatible API and a TFE-compatible API,
    each with OpenDustpan as the source, an S3-compatible server, the
    OpenTofu `http` backend and the upload. It never says that the suite
    tested Pulumi Cloud, HCP Terraform, Terraform Enterprise or Amazon S3.
    It says that a source that redirects a download to another host can
    fail (ADR 243 decision 11), and that an installation behind an outbound
    proxy imports through the upload source (ADR 241 decision 6). The
    default NetworkPolicy of the Helm chart (ADR 246 decision 23) allows TCP
    443 only. A source on another port needs an `extraEgress` rule.

### Source users and teams

16. **Report only (product manager, RFC 81 question 4).** An import and a
    move check never map a user or a team of the source to a principal of
    OpenDustpan, and never apply a grant. The `carry.access` finding names
    the users and teams of the source as text, where the source gives them.
    A later decision can add a proposal that an administrator accepts.

    Benchmark: `tfmigrate` moves state only and changes no access. The
    GitLab importers keep the contributions of a source user on a
    placeholder user until an owner reassigns them and the user accepts.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it
replaces:

- **ADR 213, decision 10:** "A lock request of a tool for the unit (an
  OpenTofu lock, or the start of a Pulumi update) fails." For the manual
  lock of an import only, the start of a Pulumi update of the kind
  `preview` passes (decision 12).

This ADR also extends ADR 29 decision 6, the checks after an import by
hand. The move check of this ADR does them through the API. The guide keeps
the checks by hand for the CLI path.

## Relation to RFC 66

This ADR answers these questions of RFC 66 for an import into OpenDustpan:

- The scope: an import tool and a move check (ADR 241, this ADR).
- The form: a REST API with a CLI that calls it (ADR 17).
- One unit or a namespace: both, one unit first (decision 1).
- How the list stays current: decision 7.

- A source principal and a target principal: the tooling reports, and
  never maps (decision 16).

## Consequences

- The import is not done until its move check passes, or a person forces
  the release with a reason. A team that cannot run a plan for a unit gets a
  `pending` check, and uses the forced release.
- A client report is weaker evidence than an observed preview or a plan
  job. The report says which way gave the evidence.
- The test of decision 7 makes each new unit-bound table name its move
  check finding.
- The e2e harness needs an S3-compatible server in the e2e job, in addition
  to the second OpenDustpan server that ADR 29 already uses.
