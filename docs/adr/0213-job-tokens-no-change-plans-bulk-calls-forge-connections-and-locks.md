# 213. Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks

Status: accepted · 2026-10-18. Supersedes in part ADR 100 decision 20. Extends ADR 103 decisions 1 and 8, ADR 153 decision 4, ADR 20 and ADR 25. Answers an open question of ADR 103, ADR 117 and ADR 141. Accepts part of RFC 18

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.
Decision 10 is an owner decision of 2026-10-18.

Nine open questions of the job, change order, Catalog, state and inventory
RFCs have one answer that Google, AWS, PCI DSS, SOC 2 and ISO 27001 share,
or that the competitors share where these sources say nothing:

- [RFC 15](../rfc/0015-output-sharing.md): which outputs a job reads. ADR
  100 decision 20 makes the job token "an access token of the principal
  that started the job, limited to the unit of the job". An output-sharing
  rule (ADR 51) can let unit B read the outputs of unit A. A job of unit B
  must read them. A job reads them on two paths: the tool reads them with
  the job token (a stack reference of the Pulumi CLI, or a
  `terraform_remote_state` data source of OpenTofu), or a profile block
  `outputs/opendustpan` gives them to the job (the profile ADRs).
- ADR 153 decision 4 records an edge at each output read, but names only
  the read paths of a workstation.
- ADR 103 decision 8: what a job that only plans can read. A plan of
  OpenTofu or of the Pulumi CLI decrypts the secrets of its own unit.
- [RFC 28](../rfc/0028-stored-plans.md): what a plan with no change does.
- [RFC 13](../rfc/0013-policy-and-approvals.md): what the policy input of
  `job.start` holds. ADR 103 decision 1 gives it "the key paths of the
  edits" and "no secret value". A policy cannot ask "does this edit raise
  the instance size" from a key path only.
- [RFC 44](../rfc/0044-bulk-operations.md): how a call on many objects
  works, and how it shows partial failures.
- ADR 117: "Does a read of the options of a source need its own action, so
  that a platform team can let a person read a form and not submit it?"
  Today the options route needs `catalog:request`.
- [RFC 12](../rfc/0012-api-edits-and-write-back.md): where the credentials
  of a forge connection live, and how many connections a namespace has.
  ADR 111 already reads "the forge connection of the namespace" when a
  request names none.
- [RFC 26](../rfc/0026-unit-status.md): "Is 'locked' a status or a separate
  flag?" [RFC 18](../rfc/0018-state-operations.md) keeps the manual lock
  open. ADR 103 and ADR 141 ask whether a freeze covers a collection or a
  unit.

## Decision

1. **A job reads the outputs of another unit on two paths, each with two
   checks.** The server checks both conditions of a path at each read. A
   grant or a rule that changes after the start of the job applies at the
   next read.

   | Path | Check 1 | Check 2 |
   | --- | --- | --- |
   | The tool reads with the job token: a stack reference (Pulumi CLI) or `terraform_remote_state` (OpenTofu) | An output-sharing rule of the source unit names the unit of the job. | The principal that started the job has `unit:read` on the source unit with its own grants, and `unit:read-secrets` for a secret output. |
   | A profile block `outputs/opendustpan` in a job | An output-sharing rule of the source unit names the unit of the job. | The authority of the profile attachment has `unit:read` on the source unit, and `unit:read-secrets` for a secret output (the profile ADRs, ADR 207 decision 7). |

   The job gets the intersection of the two checks, never more. It reads
   no other part of the other unit. This replaces "limited to the unit of
   the job" in ADR 100 decision 20 for output reads only. Each other part
   of that decision stays.

   **The answer to a job-token read of another unit.** The tool reads the
   outputs of another unit through a route that it already sends: the
   export of a stack for the Pulumi CLI, and the current state version and
   its download for OpenTofu. For a job token of another unit, the server
   answers a state document with only the root outputs: a deployment with
   only the outputs of the root stack resource for the Pulumi CLI, and a
   state file with only its `outputs` for OpenTofu. It holds no resource.
   A secret output is in the answer only when the principal has
   `unit:read-secrets` on the source. Else the output is not in the
   answer. A secret output stays a secret in the tool: a Pulumi secret, or
   an OpenTofu output with `sensitive = true`.

   **The edge.** Each such read records the edge of ADR 153 decision 4,
   with the evidence `job`. A read through the profile block records the
   same edge.

   **Rules come with the reads.** A job reads the outputs of another unit
   only through an output-sharing rule. So cross-unit reads in jobs ship
   in the same release as output-sharing rules. The escape hatch for a
   team that wants all its units to share: one rule on a namespace or on a
   collection (ADR 51 decision 8) shares the outputs of all its units with
   each other.

   Benchmark: AWS IAM gives a session the intersection of the session
   policy and the identity policy of its role. Google Cloud gives a
   downscoped token the intersection of its credential access boundary and
   the grants of its principal. HCP Terraform lets a workspace read the
   outputs of another workspace only when that workspace shares its state
   with it.
2. **A job that only plans decrypts the secrets of its own unit.** The job
   token of a job that only plans (`unit:run-plan`, ADR 103 decision 8)
   gets `unit:decrypt` and an unmasked read of the state of its own unit,
   when the principal that started the job has `unit:run-plan`. It gets no
   write: no state write, no lock other than the lock of the plan, and no
   `unit:encrypt`. It gets no read of another unit, except by decision 1.
   The plan and the logs stay masked (ADR 102). The role page of `planner`
   says that the code of a plan runs on the runner and can see the secrets
   of the unit there.

   Benchmark: the "plan" permission of HCP Terraform has the same
   property: a plan run reads the sensitive variables and the state of its
   workspace on the agent, and a person with the permission cannot read
   them in the console.
3. **A plan with no change ends `succeeded`.** When the plan phase of a job
   finds no change, the job ends `succeeded`. The apply phase does not run,
   and no approval gate of the apply opens. A unit plan with no change in a
   change order needs no signature and starts no apply job. The tool tells
   "no change": the exit code 0 of `tofu plan -detailed-exitcode`, and a
   preview of the Pulumi CLI with only `same` steps and no change to a
   stack output. A change of an output only is a change, for both tools.
   The job record and the console say "no change". An e2e step checks
   that a Pulumi program that changes only an output gets a plan with a
   change.

   Benchmark: HCP Terraform ends a run with no change as "planned and
   finished", with no apply and no confirmation.
4. **The policy input holds old and new values, never secret ones.** For
   each edit, the input of `job.start` holds the key path and a flag
   `secret`. For a key that is not secret, it also holds the old value and
   the new value. For a secret key, it holds the flag only, never a value
   or a hash of a value. This extends ADR 103 decision 1. The rule "no
   secret value" stays.

   A value is at most 4,096 bytes as JSON. A longer value is in the input
   as a JSON string of its first 4,096 bytes, and the edit has the flag
   `truncated: true`. A policy that must see the whole value denies an
   edit with the flag.

   Benchmark: Kubernetes admission review gives a policy the old and the
   new object, and AWS Config gives a rule the old and the new
   configuration of a resource.
5. **Bulk calls: at most 100 IDs, each item on its own.** A bulk call
   takes from 1 to 100 IDs of one object kind. A call with more answers 400
   and does nothing. For each item, the server checks the authorization and
   the policy, does the operation, records the result and writes the audit
   entry, as for a call on one item. The call is not atomic: an item that
   fails does not stop or undo the others. The answer is 200 with one
   result for each ID, in the order of the request: the ID, `ok` or the
   error of ADR 182 for that item.

   The error of an item is the error that a call on that item alone
   answers. An item that does not exist and an item that the caller cannot
   read both get the not-found error of a call on one item. So a bulk call
   tells no more about which objects exist than single calls do.

   A retry of a bulk call is safe with the `Idempotency-Key` header (ADR
   210 decision 6): a replay answers the result of each item of the first
   call and does no item a second time.

   Benchmark: Amazon S3 `DeleteObjects` (up to 1,000 keys) and Amazon SQS
   `DeleteMessageBatch` (up to 10 entries) answer one result for each entry
   and are not atomic. Google AIP-233 to AIP-235 prefer atomic batches.
   This ADR follows AWS, because an authorization check on each item must
   not let one denied item block the others.
6. **A separate action reads the options of a blueprint form.** The new
   action `catalog:read-options`, on the blueprint, reads the options of the
   sources of a blueprint form (ADR 113). It submits nothing. The options
   route needs it instead of `catalog:request`. Each built-in role with
   `catalog:request` also gets `catalog:read-options`. So a platform team
   can let a person see a form with its choices and not submit it.

   Benchmark: AWS IAM and Google Cloud IAM give a read of a resource its
   own action, apart from the action that changes it.
7. **Forge credentials are sealed credentials of the namespace.** The
   credentials of a forge connection are sealed credentials of the
   namespace, not a setting. The settings resource (ADR 212 decision 7)
   does not hold them. The server seals them with a data key that the
   key-encryption key wraps (ADR 23) and never answers them. A rotation
   adds the new credentials before it removes the old ones. Two sets can
   be active during a rotation, and the server uses the newest set that
   works. Each change writes an audit entry with no part of a credential.

   When the newest set fails and the server uses an older set, it writes a
   WARN log line with the connection ID and the age of the set, and counts
   the fallback in the metric `forge_credential_fallback_total` with the
   label of the connection. The log line and the metric hold no part of a
   credential. So an operator sees a rotation that did not finish.

   Benchmark: designed against PCI DSS v4.0.1 requirements 8.3.2 (make
   authentication factors unreadable in storage) and 8.6.3 (protect the
   passwords of system accounts, and change them).
8. **Many forge connections, one default.** A namespace can have many
   forge connections, for example one for each forge or each organization
   of a forge. One of them is the default. A code location or a request
   that names no connection uses the default. The removal of the default
   connection fails until another connection is the default.

   Benchmark: HCP Terraform lets one organization hold many VCS providers,
   and a workspace names one of them.
9. **A lock is a flag, not a status.** The inventory keeps the lock of a
   unit as a flag, with its holder, beside the status. A locked unit keeps
   its status, for example "drifted" or "failed". The status vocabulary of
   RFC 26 holds no "locked" value. The flag covers the lock of a tool (an
   OpenTofu lock or a Pulumi update lease) and the manual lock of decision
   10.

   Benchmark: HCP Terraform shows the lock of a workspace apart from the
   status of its latest run.
10. **Manual locks (owner decision 2026-10-18).** A person can put a manual
    lock with a reason on one unit, one collection or a whole namespace.
    - The action is `unit:lock` on the object of the lock: a unit, a
      collection or a namespace. A collection and a namespace widen the
      object, as ADR 51 decision 8 says. The reason is required, at most
      500 characters.
    - While a manual lock covers a unit, no new job of the unit starts, and
      no queued phase of the unit dispatches. A queued job waits with the
      reason `locked`. A phase that already runs runs to its end.
    - A lock request of a tool for the unit (an OpenTofu lock, or the start
      of a Pulumi update) fails as for a lock that another principal holds
      (ADR 25 for OpenTofu, ADR 20 for the Pulumi CLI). The message names
      the holder and the reason of the manual lock. A state write of a tool that already holds its
      own lock is never refused.
    - The holder removes the lock. A person with `unit:manage` on the
      object of the lock can also remove it, as a force-unlock.
    - Each lock and each removal writes an audit entry with the principal,
      the object and the reason.
    - Each unit that a manual lock covers shows the flag of decision 9,
      with the holder, the reason, the time and the object of the lock.

    A freeze (ADR 141) stays a schedule for a whole namespace. A manual
    lock is the way to stop the jobs of one collection or one unit. This
    answers the open question of ADR 103 and ADR 141 about a freeze on a
    collection or a unit.

    Benchmark: HCP Terraform lets a person lock a workspace with a reason,
    and a locked workspace starts no run. env zero has a manual lock with a
    reason on an environment.

## Not decided here

- Which bulk operations exist first, and their routes (ADR 210 decision 3).
- The status vocabulary of RFC 26 and its order.
- Whether an output-sharing rule can name a unit of another namespace.

## Consequences

- The job token code checks both conditions of each path of decision 1 at
  each output read. The `jobtest` suite gets a case for each condition and
  for both, on each path.
- The tool packages answer a job-token read of another unit with the root
  outputs only, and record the edge with the evidence `job`. The e2e suite
  gets two steps: a Pulumi stack reference in a job, and a
  `terraform_remote_state` data source in a job. Each step checks a secret
  output and a refused read with no rule.
- The job token of a plan-only job gets the reads of decision 2, and a test
  checks that it can write nothing.
- The plan step of each tool reports "no change". The job model gets the
  rule of decision 3, and `jobtest` checks that no apply phase starts.
- `job.Decider` gets the values of decision 4. A canary test checks that no
  secret value reaches the input, and a test checks the truncation flag.
- The route test and the built-in roles get `catalog:read-options` and
  `unit:lock`.
- The forge connection store gets the sealed credentials, the default flag
  and a conformance suite. The metric of decision 7 is in the metrics
  reference.
- The inventory projection gets the manual lock of decision 10, with a
  conformance case for each object kind.
