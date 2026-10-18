# 191. Plan revisions, unit plans and signatures

Status: accepted · 2026-10-18 · part of ADR 190

## Context

A change order holds a plan of one or more units (ADR 190). An author can
change the code after a review and attach a new plan. The owner decided
that the signatures reset when a new plan is attached (owner). So the
server must know which plan each signature is for, and which plan is the
current one.

The job model already binds an approval to a stored plan (ADR 102 decision
7): the ID of the plan, the hashes of its two parts and the state version
that it read (`job.PlanBinding`). A change order binds each signature to
all the stored plans of one revision at the same time.

## Decision

1. **A plan revision.** A plan revision (`changeorder.Revision`) is the
   plan of a change order at one time. It has a number, from 1 and one
   higher for each attach. It holds one unit plan for each unit of the
   change order, the principal that attached it, the time, and a state:
   `planning`, `ready`, `failed` or `superseded`. Only the revision with
   the highest number is the current revision. The others are
   `superseded`, and stay in the record.
2. **Attach.** An author attaches a revision with the units, the operation
   of each unit (`apply` or `destroy`) and the code location of each unit
   with its commit (ADR 100 decision 5). The first revision comes with the
   create of the change order. In the transaction of the attach, the store:
   - checks the state with `changeorder.Next` (ADR 190 decision 8),
   - marks the revision before it `superseded`,
   - voids each signature of the revision before it (decision 6),
   - starts the durable operation `changeorder.plan` with the start key
     `<change order ID>/<revision>`,
   - adds the principal to the authors (ADR 190 decision 4).

   The operation starts one plan job for each unit, as the principal that
   attached the revision, with the idempotency key `<change order
   ID>/<revision>/<unit ID>/plan`. The start needs `unit:run-plan` on each
   unit (ADR 103 decision 8). It then cancels each plan job of the
   revision before it that did not end.
3. **A unit plan.** A unit plan (`changeorder.UnitPlan`) holds the unit
   reference with its unit ID, the operation, the code location with its
   commit, the ID of the plan job, and its status: `planning`, `ready` or
   `failed`. When the plan job succeeds, `ObserveJob` (ADR 190 decision 11)
   copies the binding of its stored plan: the plan ID (`pln-`), the
   SHA-256 of the part `plan-display`, the SHA-256 of the part
   `plan-apply`, and the state version that the plan read. A plan job that
   fails, or that ends with no stored plan, makes the unit plan `failed`
   with the reason of the job. The revision is `ready` when each unit plan
   is `ready` and the analysis of each unit plan ended (ADR 194 decision
   6). The revision is `failed` when one unit plan is `failed`.
4. **The digest of a revision.** When a revision becomes `ready`, the store
   computes its digest (`changeorder.Digest`). It is the SHA-256 of a
   canonical encoding with the version name `cho-digest/1`. The encoding
   holds the change order ID, the revision number and, for each unit plan
   in the order of the unit IDs: the unit ID, the unit reference, the
   operation, the commit, the plan ID, the two part hashes and the state
   version. The encoding is a list of length-prefixed byte strings, so no
   two inputs give the same bytes. A table-driven test holds known inputs
   and their digests. **One-way door:** signatures and audit entries hold
   the digest. A new encoding gets a new version name, and the store keeps
   the old one for old revisions.
5. **A signature.** A signature (`changeorder.Signature`) is the record
   that binds one approval to one revision. It holds the subject of the
   approver, the revision number, the digest that the approver saw, the
   requirement that it counts for (ADR 192 decision 4), the time, and the
   kind and the ID of the credential of the request (`session` or `token`,
   as `job.Actor.Credential` of ADR 128 decision 6). It never holds the
   credential. An approve verdict makes one signature (ADR 192). The
   request names the digest that the approver saw. A digest that is not
   the digest of the current revision fails with
   `changeorder.ErrPlanChanged`, and records nothing. So a person never
   signs a plan that they did not see.
6. **A new plan resets the signatures (owner).** An attach voids each
   signature of each earlier revision, in the same transaction. A void
   signature stays in the record with the time and the cause `new-plan`.
   It never counts again, also when a later revision has the same digest
   inputs. The request-changes threads do not reset. They follow their
   anchors into the new revision (ADR 193 decision 7).
7. **A stale plan.** A unit plan is stale when the current state version
   of its unit is not the version that it read, when it is older than the
   maximum plan age of the job model, or when the history model redacted
   it (ADR 102 decision 7). A stale plan does not void a signature, because
   no new plan exists. A request to apply checks each unit plan of the
   revision with the rule of `job.CheckPlan`, through the wiring, before
   its transaction. A stale unit plan fails the request with
   `changeorder.ErrPlanStale`, and names each stale unit. The author then
   attaches a new revision. The dispatch check of each apply job is the
   final guarantee (ADR 190 decision 9).
8. **What a reviewer reads.** A reviewer reads the readable plan of each
   unit plan, masked, with `job:read-plans` on its unit (ADR 102 decision
   8). The review view of ADR 194 reads the same parts. No person reads
   the part `plan-apply`.

## Consequences

- A reviewer signs one digest for the whole plan of a change order. A
  change of any unit plan gives another digest.
- A new commit after a review starts a new plan job for each unit. A later
  ADR may let a revision keep the unit plan of a unit whose commit and
  state version did not change.
- A state write on a unit after the review makes the plan stale. The
  author attaches a new revision, and the reviewers sign again.

## Open questions

- Does a new revision keep the unit plan of a unit whose commit, state
  version and operation did not change, when that plan is not stale? The
  digest of the revision still changes, so the signatures still reset.
- Does a signature need a fresh authentication, as a confirmation does
  (ADR 128 decision 7)? Version 1 accepts each credential that holds the
  action, so a portal can approve through the API (RFC 13).
- Does an author add or drop a unit with an attach? Version 1 allows it:
  a revision names its units, and a new revision can name other units.
