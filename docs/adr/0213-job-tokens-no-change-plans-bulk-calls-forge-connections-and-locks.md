# 213. Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks

Status: accepted · 2026-10-18 · supersedes in part ADR 100 decision 20. Extends ADR 103 decision 1. Answers an open question of ADR 117

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.

Eight open questions of the job, change order, Catalog and inventory RFCs
have one answer that Google, AWS, PCI DSS, SOC 2 and ISO 27001 share, or
that the competitors share where these sources say nothing:

- [RFC 15](../rfc/0015-output-sharing.md): which outputs a job reads. ADR
  100 decision 20 makes the job token "an access token of the principal
  that started the job, limited to the unit of the job". An output-sharing
  rule (ADR 51) can let unit B read the outputs of unit A. A job of unit B
  must read them.
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
  flag?"

## Decision

1. **A job token reads shared outputs.** A job token reads the outputs of
   another unit only when both of these are true:
   - An output-sharing rule of that unit names the unit of the job.
   - The principal that started the job can read the outputs of that unit
     with its own grants.

   The token gets the intersection of the two, never more. It reads no
   other part of the other unit. This replaces "limited to the unit of the
   job" in ADR 100 decision 20 for output reads only. Each other part of
   that decision stays.

   Benchmark: AWS IAM gives a session the intersection of the session
   policy and the identity policy of its role. Google Cloud gives a
   downscoped token the intersection of its credential access boundary and
   the grants of its principal.
2. **A plan with no change ends `succeeded`.** When the plan phase of a job
   finds no change, the job ends `succeeded`. The apply phase does not run,
   and no approval gate of the apply opens. A unit plan with no change in a
   change order needs no signature and starts no apply job. The tool tells
   "no change": the exit code 0 of `tofu plan -detailed-exitcode`, and a
   preview of the Pulumi CLI with only `same` steps. The job record and the
   console say "no change".

   Benchmark: HCP Terraform ends a run with no change as "planned and
   finished", with no apply and no confirmation.
3. **The policy input holds old and new values, never secret ones.** For
   each edit, the input of `job.start` holds the key path and a flag
   `secret`. For a key that is not secret, it also holds the old value and
   the new value. For a secret key, it holds the flag only, never a value
   or a hash of a value. This extends ADR 103 decision 1. The rule "no
   secret value" stays.

   Benchmark: Kubernetes admission review gives a policy the old and the
   new object, and AWS Config gives a rule the old and the new
   configuration of a resource.
4. **Bulk calls: at most 100 IDs, each item on its own.** A bulk call
   takes from 1 to 100 IDs of one object kind. A call with more answers 400
   and does nothing. For each item, the server checks the authorization and
   the policy, does the operation, records the result and writes the audit
   entry, as for a call on one item. The call is not atomic: an item that
   fails does not stop or undo the others. The answer is 200 with one
   result for each ID, in the order of the request: the ID, `ok` or the
   error of ADR 182 for that item.

   Benchmark: Amazon S3 `DeleteObjects` (up to 1,000 keys) and Amazon SQS
   `DeleteMessageBatch` (up to 10 entries) answer one result for each entry
   and are not atomic. Google AIP-233 to AIP-235 prefer atomic batches.
   This ADR follows AWS, because an authorization check on each item must
   not let one denied item block the others.
5. **A separate action reads the options of a blueprint form.** The new
   action `catalog:read-options`, on the blueprint, reads the options of the
   sources of a blueprint form (ADR 113). It submits nothing. The options
   route needs it instead of `catalog:request`. Each built-in role with
   `catalog:request` also gets `catalog:read-options`. So a platform team
   can let a person see a form with its choices and not submit it.

   Benchmark: AWS IAM and Google Cloud IAM give a read of a resource its
   own action, apart from the action that changes it.
6. **The server seals forge credentials in the database.** The credentials of a
   forge connection are a sensitive setting of the namespace. The server
   seals them with the key-encryption key (ADR 23) and never answers them.
   A rotation adds the new credentials before it deletes the old ones. Two
   sets can be active during a rotation, and the server uses the newest
   set that works. Each change writes an audit entry with no part of a
   credential.

   Benchmark: designed against PCI DSS v4.0.1 requirements 8.3.2 (make
   authentication factors unreadable in storage) and 8.6.3 (protect the
   passwords of system accounts, and change them).
7. **Many forge connections, one default.** A namespace can have many
   forge connections, for example one for each forge or each organization
   of a forge. One of them is the default. A code location or a request
   that names no connection uses the default. The delete of the default
   connection fails until another connection is the default.

   Benchmark: HCP Terraform lets one organization hold many VCS providers,
   and a workspace names one of them.
8. **A lock is a flag, not a status.** The inventory keeps the lock of a
   unit as a flag, with its holder, beside the status. A locked unit keeps
   its status, for example "drifted" or "failed". The status vocabulary of
   RFC 26 holds no "locked" value.

   Benchmark: HCP Terraform shows the lock of a workspace apart from the
   status of its latest run.

## Not decided here

- Which bulk operations exist first, and their routes (ADR 210 decision 2).
- The status vocabulary of RFC 26 and its order.
- Whether an output-sharing rule can name a unit of another namespace.

## Consequences

- The job token code checks both conditions of decision 1 at each output
  read. The `jobtest` suite gets a case for each condition and for both.
- The plan step of each tool reports "no change". The job model gets the
  rule of decision 2, and `jobtest` checks that no apply phase starts.
- `job.Decider` gets the values of decision 3. A canary test checks that no
  secret value reaches the input.
- The route test and the built-in roles get `catalog:read-options`.
- The forge connection store gets the sealed credentials, the default flag
  and a conformance suite.
