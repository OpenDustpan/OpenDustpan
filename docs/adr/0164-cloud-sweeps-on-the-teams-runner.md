# 164. Cloud sweeps on the team's runner, with the runner's read-only identity

Status: accepted · 2026-10-17. Adds to ADR 101 (runner pools and the runner protocol). Decisions 14, 15 and 19 superseded in part by ADR 166. Route paths superseded in part by ADR 210

Changed by:

- ADR 166 changes decisions 14, 15 and 19: the match waits while the index is
  not ready, the built-in roles.
- ADR 210 changes: the route paths and the server: action names.

## Context

The owner named the feature **Sweep** (ADR 163). The kind **not in any
unit** needs a list of what exists in the cloud (ADR 163). On 17 October 2026 the project owner decided that facts about
the cloud are a concern of the infrastructure-as-code tools, and that
OpenDustpan ships no cloud adapter in the server for facts. The owner asked
this design to decide how a sweep reads the cloud, and to say why. One
example was a job on the team's runner with read-only workload-identity
credentials, on the shared job model of ADRs 100 to 103 (draft).

Facts from the job model:

- A job belongs to one unit (ADR 100 decision 2). The owner set this rule.
  A sweep reads an account, not a unit.
- A runner belongs to one pool, and a pool belongs to one namespace
  (ADR 101 decision 14). A runner claims durable operations of the kind
  `job.runner.<pool ID>`.
- A runner is shell-free. It starts each step as an argument list
  (ADR 101 decision 7). No credential passes through a variable
  (ADR 101 decision 10).
- The runner protocol is `/runner/v1`, HTTP and JSON, with long polling and
  claims with an expiry (ADR 101 decision 15). A later pull request builds
  it.

## Decision

### Where a sweep runs, and why

1. **A sweep runs on a runner of the team, never in the server.** The
   reasons:
   - The owner's rule: no cloud adapter in the server.
   - **Credentials stay with the team.** The runner reads the cloud with an
     identity that the team controls in its own cloud. The server holds no
     cloud credential for a sweep, so a breach of the server gives no read
     of any cloud.
   - **Network.** A runner runs in the network of the team. It reaches
     private endpoints, for example a Kubernetes API with no public
     address, that the server cannot reach.
   - **Scale.** A sweep of a large account takes minutes and many API calls.
     It costs the runner of the team, not the server of every tenant.
   - **One trust model.** The runner already runs tools with cloud
     credentials (ADR 101). A sweep adds no new kind of process.
2. **A sweep is not a job.** A job belongs to one unit, and the one-change
   rule, the phases and the gates of ADR 100 serve changes to a unit. A
   sweep changes nothing and has no unit. A sweep uses the same parts as a
   job: the runner pools, the runner protocol, the durable operations of
   ADR 72, the claim, the expiry and the takeover. It does not use the job
   record. `unmanaged.Scan` is its record.
3. **The durable operation.** The start of a sweep starts one durable
   operation of the kind `unmanaged.sweep.<pool ID>`, with the start key
   `<sweep ID>`, in the transaction that stores the sweep. A runner of the
   pool claims it through the runner protocol, as it claims a phase. A
   takeover is a new claim on the same operation. A sweep changes nothing,
   so a takeover always runs the command again from the start.

### Credentials

4. **The server holds no cloud credential for a sweep.** The only source of
   credentials of a sweep is the **workload identity of the runner hosts**
   of the pool, for example an instance role, a Kubernetes service account
   bound to a cloud role, or a managed identity. A sweep target names no
   credential and no profile, and the claim of a sweep passes none. So the
   server stores no cloud credential for a sweep, sends none, and a breach
   of the server gives no read of any cloud. This holds for every part of
   this design: the target, the claim, the upload and the match.
5. **A sweep pool runs only sweeps.** A runner pool gets a **purpose**:
   `jobs` or `sweeps`. A sweep target must name a pool of the purpose
   `sweeps`, and a job must name a pool of the purpose `jobs`. The reason:
   the identity of the host is the credential. A pool that also applies
   changes has an identity that can write, so its sweeps would run with
   write access. A pool of its own lets the team give its hosts a
   read-only role.
   - **The purpose never changes.** The request that makes the pool sets
     the purpose. No API changes it, so a sweep pool never becomes a job pool and
     its read-only hosts never get the claims of a job, and a target never
     ends up on a pool that applies changes. A team that wants another
     purpose makes a new pool. `ScanStore.CreateTarget` and `UpdateTarget`
     refuse a pool of the purpose `jobs` in their transaction, and the suite
     holds the case.

   This adds to ADR 101 decision 14 (see "Requests to other designs").
6. **The server cannot prove that the credentials are read-only.** The
   console says so on each target, and the guide gives the read-only role of
   each cloud: for example `ReadOnlyAccess` for AWS, `Reader` for Azure and
   `roles/cloudasset.viewer` for Google Cloud.

### The sweep target

7. **A sweep target** (`unmanaged.Target`) is namespace data: a name, a runner
   pool of the namespace, the account scopes to sweep (an AWS account, an
   Azure subscription, a Google Cloud project or a Kubernetes cluster, each
   with optional regions, each scope one time), a command, a schedule
   from 1 hour to 30 days or none, and a timeout of at most 24 hours (2 hours
   by default). A namespace has at most 100 targets. `Target.Validate` holds
   the rules, and a table-driven test covers them.
8. **The command.** The command is an argument list that the launcher starts
   with no shell, with the rules of a job step: 1 to 256 arguments, 128 KiB
   in all, no NUL byte, and a program that is a bare name or a clean
   absolute path. The command writes the sweep report to the file that the
   runner names in `DUSTPAN_SWEEP_REPORT`. The variable holds a path, not a
   credential.
   - **No secret in the arguments.** The server stores the arguments, the
     console shows them and audit entries keep them (decision 20), so they
     must hold no secret. `Target.Validate` refuses an argument that the
     credential-variable rule of a job step matches (ADR 101 decision 7),
     as a flag (`--password`, `--api-key=...`) or as an assignment
     (`AWS_SECRET_ACCESS_KEY=...`), with no case and with `-` for `_`
     (`unmanaged.CredentialArg`). The runner masks the arguments in its log
     as it masks the secrets of a job step (ADR 101 decision 10), so a
     value that the rule does not catch still does not reach the log. A
     command that needs a secret is wrong for a sweep: the identity of the
     runner is the credential.
9. **OpenDustpan ships no sweeper program in the server.** The team chooses the
   command: the inventory tool of its cloud and a converter to the report
   format, or a program of its own. The read-only inventory services of
   each cloud list most resource types in one API, for example AWS
   Resource Explorer, Azure Resource Graph and Google Cloud Asset Inventory.
   Whether OpenDustpan ships converters for them with the runner is an open
   question.

### The sweep report

10. **Format.** A sweep report is JSON Lines, version 1:
    - The first line is a header: `{"format": "opendustpan-sweep-report",
      "version": 1, "scopes": [...]}`. Each scope names its provider, its
      scope, its regions, `complete`, and an `error` when it is not
      complete.
    - Each other line is one record: `{"provider", "id", "type", "region",
      "name", "tags", "scope"}`. `type` is the cloud's own type name.
      `region` is empty for a global resource, for example an IAM role.
      `scope` is optional: it names the account scope of a resource whose
      ID names none (decision 13).
    - The parser refuses an unknown field. `unmanaged.ParseHeader` and
      `unmanaged.ParseRecord` hold the rules, and table-driven tests cover
      them.
11. **Bounds.** A report is at most 1 GiB and 5,000,000 records. A line is at
    most 64 KiB. A record has at most 50 tags, with a key of at most 128
    bytes and a value of at most 256 bytes. A header has at most 1,000
    scopes.
12. **Upload.** The runner streams the report to the server through the
    runner protocol, under its claim. The server writes it to the blob store
    as a stream (ADR 45), encrypted with the data key of the namespace,
    because tags can hold names and text of the team. The upload ends the
    claim and moves the sweep to `matching`. After a match that succeeds,
    the server deletes the report. It keeps the report of a failed match for
    7 days, so a person can read the error.
13. **Scopes: what a sweep may say, and what it may close.**
    - **The header is a subset of the target.** Each scope of the header
      must be a scope of the target, with only regions of the target when
      the target lists regions. The match refuses a report whose header
      names any other scope, and the sweep fails
      (`unmanaged.CheckHeaderScopes`).
    - **Each scope comes one time.** The parser refuses a header that names
      one provider and account scope twice (`unmanaged.ParseHeader`). Else a
      complete copy of a scope could hide a copy that failed, and the match
      would resolve findings that the sweep did not see.
    - **The match drops a record outside the header.** It gives each
      record the scope of the header that it belongs to
      (`unmanaged.RecordScope`), and drops the record when there is none.
      The sweep counts these records (`outOfScope`).
    - **A key with no account or no region.** Some cloud IDs name no account,
      for example the ARN of an S3 bucket, and some name no region, for
      example the ARN of an IAM role. Such a record takes the scope of the
      sweep that saw it: the scope that the record names, or the one scope
      of its provider in the header. With two or more such scopes and no
      name, the match drops the record. A finding keeps that scope and the region
      of the record.
    - **The region of the key wins.** Where the key names a region (field 4
      of an AWS ARN, the segment after `/regions/` of a Google Cloud full
      resource name), the record takes that region (`unmanaged.Region`). A
      record that names another region than its key is outside the header,
      as a record that names another account than its key, and the match
      drops it. So a runner cannot move a finding to a region that the
      sweep does not cover, or out of one that it covers. A finding from a state check of such a key has no scope
      until a sweep sees it, and no sweep resolves it before.
    - **A complete scope.** A scope with `complete: false` had an error, for
      example a region that refused the credentials. The server never
      resolves a finding of that scope because the sweep did not see its
      resource. So a failure of the sweeper program never closes findings.
    - **Regions.** A complete scope that lists regions resolves only the
      findings of those regions. A finding with no region, a global
      resource, resolves only by a complete scope of every region
      (`unmanaged.Covers`). The suite holds the case.

### The pipeline

14. **The steps of a cloud sweep:**
    1. **Start.** A person with `namespace:start-sweep`, or the schedule,
       starts a sweep of a target. The store refuses a second sweep of the
       same target that did not end (`ErrScanBusy`).
    2. **Sweep.** A runner of the pool claims the operation and runs the
       command with the workload identity of its host.
    3. **Upload.** The runner streams the report.
    4. **Match.** A durable operation `unmanaged.match` in the server checks
       the header against the target (decision 13), then reads the report in
       batches of 1,000 records. It drops each record outside the header.
       For each batch it reads the holders of the keys in the namespace and
       its linked namespaces from the index (`Index.Holders`), classifies
       each key (ADR 163) and writes the findings (`FindingStore.Record`).
    5. **Resolve.** At the end it resolves the findings that the sweep did not
       see in a complete scope (`FindingStore.ResolveUnseen`).
    6. **End.** The sweep becomes `succeeded` with its counts, or `failed`
       with its reason. One audit entry records the end.

    A state check (ADR 163) has no steps 2 and 3. It reads its keys from the
    index.
15. **States.** A sweep has the statuses `queued`, `dispatched`, `running`,
    `matching`, `succeeded`, `failed`, `canceled` and `expired`.
    `unmanaged.NextScan` holds the table, and a test checks each pair of
    status and event against a table written out apart from the code. A
    state check goes from `queued` to `matching` and never goes to a
    runner. A cancel during the match is not allowed: the match only reads
    the report and writes findings.
16. **The schedule.** A schedule row `unmanaged.schedule` of ADR 72 runs every
    15 minutes. It starts a sweep of each enabled target whose time came,
    and the daily state check of each namespace at the tick of its offset
    (ADR 163 decision 11). It also ends the ignores and the pending actions
    whose time came (ADR 165).

### What a sweep runner that lies can do

17. A runner of a sweep pool runs on hosts of the team, so the server cannot
    trust its report. Decision 13 bounds what a runner that lies, or a
    sweeper program with an error, can do. It can do these things, and only
    **inside the scopes of its own target**:
    - **Hide findings.** It leaves out records, so the findings of not in
      any unit and left behind in those scopes resolve. A person sees the
      resolve in the counts of the sweep and in the history of each finding.
    - **Make findings.** It adds records of keys that do not exist, so new
      findings of not in any unit open in those scopes.
    - **Change the facts of a finding.** It gives wrong types, names, regions
      and tags.

    It cannot change a finding outside the scopes of its target: the header
    refuses another scope, and the match drops a record outside the header.
    It cannot resolve a finding of managed twice, which only a state check
    resolves. It cannot reach another namespace: the match writes the
    findings of the namespace of the target only, and reads the units of the
    namespace and of its linked namespaces only (ADR 163 decision 9). It
    cannot read any finding: the runner protocol gives a sweep its command
    and takes its report, nothing else. `namespace:manage-sweeps` decides
    the scopes of a target, so a person who holds it decides what a runner
    of the pool can hide.

### Ports, actions and audit

18. **`unmanaged.ScanStore`** holds targets and sweeps: `CreateTarget`,
    `UpdateTarget`, `DeleteTarget`, `Targets`, `StartScan`, `AdvanceScan`,
    `Scan` and `Scans`. Each write carries the fence. The store checks in
    its transaction that the pool belongs to the namespace of the target
    and has the purpose `sweeps`.
    The suite in `unmanagedtest` states its behavior.
19. **Actions:**

    | Operation | Action | Object |
    | --- | --- | --- |
    | Create, change or delete a sweep target | `namespace:manage-sweeps` (new) | namespace |
    | Start or cancel a sweep, or a state check | `namespace:start-sweep` (new) | namespace |
    | Read targets, sweeps and their counts | `namespace:read-unmanaged` (new) | namespace |

    `namespace:manage-sweeps` decides which command a runner starts, so only
    the roles that administer a namespace hold it. The runner protocol
    authenticates the runner, as for a job.
20. **Audit entries.** A create, a change or a delete of a target appends an
    entry with the action of decision 19 and the changed fields. The entry
    holds the command, because a person must see what the target tells a
    runner to start. The command holds no secret (decision 8). A start and a
    cancel append an entry. The end of a sweep appends one entry from the
    server, with the counts and the reason of a failure.
21. **Routes.** Proposals for the route table:
    - `GET` and `POST /api/unmanaged/{namespace}/targets`
    - `GET`, `PUT` and `DELETE /api/unmanaged/{namespace}/targets/{name}`
    - `GET /api/unmanaged/{namespace}/sweeps?target=`
    - `POST /api/unmanaged/{namespace}/sweeps` with a target, or with the kind
      `state`
    - `GET /api/unmanaged/{namespace}/sweeps/{id}`
    - `POST /api/unmanaged/{namespace}/sweeps/{id}/cancel`
    - In the runner protocol: the claim of a sweep, and
      `PUT /runner/v1/session/sweep-report` for the upload (ADR 131). The
      path is in the session scope, so only the session key of the claim
      can upload. The upload ends the claim (`runner.SweepClaimer.Report`).

## Consequences

- A team that wants the kind not in any unit must run a runner with a
  read-only identity and a sweeper program. Left behind and managed twice
  work with no runner (ADR 163).
- OpenDustpan never holds, stores or passes a credential that can read a
  cloud for a sweep.
- A team cannot use the read-only entry of a profile for a sweep. A team
  whose runner hosts cannot have a workload identity cannot sweep the cloud
  until the open question 2 below has an answer.
- The sweep uses the runner protocol and the pools of ADR 101. The runner
  protocol pull request must include the claim and the upload of a sweep.
- A team with many accounts makes one target for each group of accounts that
  one identity can read.

## Requests to other designs

- **Runner pools (ADR 101 decision 14).** A pool gets a purpose, `jobs` or
  `sweeps`. The request that makes the pool sets it, and it never changes. A runner of a sweep
  pool claims only `unmanaged.sweep.<pool ID>`. A runner of a job pool
  claims only `job.runner.<pool ID>`.
- **Runner protocol.** The claim of a sweep gives the command and the
  timeout, and no credential. The protocol takes the upload of a report as
  a stream under the claim. The runner masks the arguments of the command
  in its log, as it masks the secrets of a job step.
- **The credential-variable rule (ADR 101 decision 7).** `job.IsCredentialEnv`
  also answers for the name of a flag, so `unmanaged.CredentialArg` calls it
  and keeps no copy of the list.

## Open questions

1. **Converters.** Does OpenDustpan ship converters from AWS Resource
   Explorer, Azure Resource Graph and Google Cloud Asset Inventory to the
   report format, with the runner binary? They are code that reads a cloud,
   but on the runner, not in the server.
2. **Workload identity tokens from the server.** Should the server issue a
   short-lived signed token for each sweep, with the subject
   `sweep:<namespace>/<target>`, so a cloud role can trust only sweeps? This
   would let the cloud enforce the read-only role. It needs a token issuer
   in the server, which RFC 5 may also need for dynamic credentials. The
   signing key of such an issuer is not a cloud credential, but a cloud
   trusts it, so decision 4 would need a change and a security review.
3. **Tags in findings.** Tags can hold personal data. Should a target choose
   which tag keys to keep?
