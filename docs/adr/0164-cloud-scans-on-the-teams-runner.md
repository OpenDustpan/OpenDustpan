# 164. Cloud scans on the team's runner, with read-only credentials

Status: proposed · 2026-10-17 · adds to ADR 101 (runner pools and the runner protocol)

## Context

The kind **not in any unit** needs a list of what exists in the cloud
(ADR 163). On 17 October 2026 the project owner decided that facts about
the cloud are a concern of the infrastructure-as-code tools, and that
OpenDustpan ships no cloud adapter in the server for facts. The owner asked
this design to decide how a scan reads the cloud, and to say why. One
example was a job on the team's runner with read-only workload-identity
credentials, on the shared job model of ADRs 100 to 103 (draft).

Facts from the job model:

- A job belongs to one unit (ADR 100 decision 2). The owner set this rule.
  A scan reads an account, not a unit.
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

### Where a scan runs, and why

1. **A scan runs on a runner of the team, never in the server.** The
   reasons:
   - The owner's rule: no cloud adapter in the server.
   - **Credentials stay with the team.** The runner reads the cloud with an
     identity that the team controls in its own cloud. The server holds no
     cloud credential for a scan, so a breach of the server gives no read
     of any cloud.
   - **Network.** A runner runs in the network of the team. It reaches
     private endpoints, for example a Kubernetes API with no public
     address, that the server cannot reach.
   - **Scale.** A scan of a large account takes minutes and many API calls.
     It costs the runner of the team, not the server of every tenant.
   - **One trust model.** The runner already runs tools with cloud
     credentials (ADR 101). A scan adds no new kind of process.
2. **A scan is not a job.** A job belongs to one unit, and the one-change
   rule, the phases and the gates of ADR 100 serve changes to a unit. A
   scan changes nothing and has no unit. A scan uses the same parts as a
   job: the runner pools, the runner protocol, the durable operations of
   ADR 72, the claim, the expiry and the takeover. It does not use the job
   record. `unmanaged.Scan` is its record.
3. **The durable operation.** The start of a scan starts one durable
   operation of the kind `unmanaged.scan.<pool ID>`, with the start key
   `<scan ID>`, in the transaction that stores the scan. A runner of the
   pool claims it through the runner protocol, as it claims a phase. A
   takeover is a new claim on the same operation. A scan changes nothing,
   so a takeover always runs the command again from the start.

### Credentials

4. **Read-only credentials, never stored in the server.** A scan target names
   one of two credential sources (`unmanaged.CredentialSource`):
   - `runner`: the workload identity of the runner hosts of the pool, for
     example an instance role, a Kubernetes service account bound to a
     cloud role, or a managed identity. The server passes nothing. This is
     the default.
   - `profile`: the read-only entry of a profile (RFC 5). The runner gets it
     at the claim, through the runner protocol, as a job gets the
     credentials of its profile (ADR 101 decision 10).

   There is no third source. The server refuses a target that names a static key.
5. **A scan pool runs only scans.** A runner pool gets a **purpose**:
   `jobs` or `scans`. A scan target must name a pool of the purpose `scans`,
   and a job must name a pool of the purpose `jobs`. The reason: with the
   source `runner`, the identity of the host is the credential. A pool that
   also applies changes has an identity that can write, so its scans would
   run with write access. A pool of its own lets the team give its hosts a
   read-only role. This adds to ADR 101 decision 14 (see "Requests to other
   designs").
6. **The server cannot prove that the credentials are read-only.** The
   console says so on each target, and the guide gives the read-only role of
   each cloud: for example `ReadOnlyAccess` for AWS, `Reader` for Azure and
   `roles/cloudasset.viewer` for Google Cloud.

### The scan target

7. **A scan target** (`unmanaged.Target`) is namespace data: a name, a runner
   pool of the namespace, a credential source, the account scopes to scan
   (an AWS account, an Azure subscription, a Google Cloud project or a
   Kubernetes cluster, each with optional regions), a command, a schedule
   from 1 hour to 30 days or none, and a timeout of at most 24 hours (2 hours
   by default). A namespace has at most 100 targets. `Target.Validate` holds
   the rules, and a table-driven test covers them.
8. **The command.** The command is an argument list that the launcher starts
   with no shell, with the rules of a job step: 1 to 256 arguments, 128 KiB
   in all, no NUL byte, and a program that is a bare name or a clean
   absolute path. The command writes the scan report to the file that the
   runner names in `DUSTPAN_SCAN_REPORT`. The variable holds a path, not a
   credential.
9. **OpenDustpan ships no scanner in the server.** The team chooses the
   command: the inventory tool of its cloud and a converter to the report
   format, or a program of its own. The read-only inventory services of
   each cloud list most resource types in one API, for example AWS
   Resource Explorer, Azure Resource Graph and Google Cloud Asset Inventory.
   Whether OpenDustpan ships converters for them with the runner is an open
   question.

### The scan report

10. **Format.** A scan report is JSON Lines, version 1:
    - The first line is a header: `{"format": "opendustpan-scan-report",
      "version": 1, "scopes": [...]}`. Each scope names its provider, its
      scope, its regions, `complete`, and an `error` when it is not
      complete.
    - Each other line is one record: `{"provider", "id", "type", "region",
      "name", "tags"}`. `type` is the cloud's own type name.
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
    claim and moves the scan to `matching`. After a match that succeeds,
    the server deletes the report. It keeps the report of a failed match for
    7 days, so a person can read the error.
13. **A complete scope.** A scope with `complete: false` had an error, for
    example a region that refused the credentials. The server never
    resolves a finding of that scope because the scan did not see its
    resource. So a failure of the scanner never closes findings.

### The pipeline

14. **The steps of a cloud scan:**
    1. **Start.** A person with `namespace:start-scan`, or the schedule,
       starts a scan of a target. The store refuses a second scan of the
       same target that did not end (`ErrScanBusy`).
    2. **Scan.** A runner of the pool claims the operation and runs the
       command with the credentials of the target.
    3. **Upload.** The runner streams the report.
    4. **Match.** A durable operation `unmanaged.match` in the server reads
       the report in batches of 1,000 records. For each batch it reads the
       holders of the keys from the index (`Index.Holders`), classifies each
       key (ADR 163) and writes the findings (`FindingStore.Record`).
    5. **Resolve.** At the end it resolves the findings that the scan did not
       see in a complete scope (`FindingStore.ResolveUnseen`).
    6. **End.** The scan becomes `succeeded` with its counts, or `failed`
       with its reason. One audit entry records the end.

    A state check (ADR 163) has no steps 2 and 3. It reads its keys from the
    index.
15. **States.** A scan has the statuses `queued`, `dispatched`, `running`,
    `matching`, `succeeded`, `failed`, `canceled` and `expired`.
    `unmanaged.NextScan` holds the table, and a test checks each pair of
    status and event against a table written out apart from the code. A
    state check goes from `queued` to `matching` and never goes to a
    runner. A cancel during the match is not allowed: the match only reads
    the report and writes findings.
16. **The schedule.** A schedule row `unmanaged.schedule` of ADR 72 runs every
    15 minutes. It starts a scan of each enabled target whose time came, and
    the daily state check of each namespace. It also ends the ignores whose
    time came (ADR 165).

### Ports, actions and audit

17. **`unmanaged.ScanStore`** holds targets and scans: `CreateTarget`,
    `UpdateTarget`, `DeleteTarget`, `Targets`, `StartScan`, `AdvanceScan`,
    `Scan` and `Scans`. Each write carries the fence. The store checks in
    its transaction that the pool belongs to the namespace of the target.
    The suite in `unmanagedtest` states its behavior.
18. **Actions:**

    | Operation | Action | Object |
    | --- | --- | --- |
    | Create, change or delete a scan target | `namespace:manage-scans` (new) | namespace |
    | Start or cancel a scan, or a state check | `namespace:start-scan` (new) | namespace |
    | Read targets, scans and their counts | `namespace:read-unmanaged` (new) | namespace |

    `namespace:manage-scans` decides which command a runner starts, so only
    the roles that administer a namespace hold it. The runner protocol
    authenticates the runner, as for a job.
19. **Audit entries.** A create, a change or a delete of a target appends an
    entry with the action of decision 18 and the changed fields. The entry
    holds the command, because a person must see what the target tells a
    runner to start. A start and a cancel append an entry. The end of a scan appends
    one entry from the server, with the counts and the reason of a failure.
20. **Routes.** Proposals for the route table:
    - `GET` and `POST /api/unmanaged/{namespace}/targets`
    - `GET`, `PUT` and `DELETE /api/unmanaged/{namespace}/targets/{name}`
    - `GET /api/unmanaged/{namespace}/scans?target=`
    - `POST /api/unmanaged/{namespace}/scans` with a target, or with the kind
      `state`
    - `GET /api/unmanaged/{namespace}/scans/{id}`
    - `POST /api/unmanaged/{namespace}/scans/{id}/cancel`
    - In the runner protocol: the claim of a scan, and
      `PUT /runner/v1/scans/{id}/report` for the upload.

## Consequences

- A team that wants the kind not in any unit must run a runner with a
  read-only identity and a scanner program. Left behind and managed twice
  work with no runner (ADR 163).
- OpenDustpan never holds a credential that can read a cloud for a scan.
- The scan uses the runner protocol and the pools of ADR 101. The runner
  protocol pull request must include the claim and the upload of a scan.
- A team with many accounts makes one target for each group of accounts that
  one identity can read.

## Requests to other designs

- **Runner pools (ADR 101 decision 14).** A pool gets a purpose, `jobs` or
  `scans`. A runner of a scan pool claims only `unmanaged.scan.<pool ID>`. A
  runner of a job pool claims only `job.runner.<pool ID>`.
- **Runner protocol.** The claim of a scan gives the command, the timeout and
  the credentials of the profile, if any. The protocol takes the upload of
  a report as a stream under the claim.
- **Profiles (RFC 5).** A profile can have a read-only entry, which the plan
  phase of a job and a scan use.

## Open questions

1. **Converters.** Does OpenDustpan ship converters from AWS Resource
   Explorer, Azure Resource Graph and Google Cloud Asset Inventory to the
   report format, with the runner binary? They are code that reads a cloud,
   but on the runner, not in the server.
2. **Workload identity tokens from the server.** Should the server issue a
   short-lived signed token for each scan, with the subject
   `scan:<namespace>/<target>`, so a cloud role can trust only scans? This
   would let the cloud enforce the read-only role. It needs a token issuer
   in the server, which RFC 5 may also need for dynamic credentials.
3. **Tags in findings.** Tags can hold personal data. Should a target choose
   which tag keys to keep?
