# 192. Reviews: approve, request changes, and reject and close

Status: accepted · 2026-10-18 · part of ADR 190

## Context

A change order needs approvals before its apply (ADR 190). The owner gave
the verdicts of a reviewer (owner):

- Approve.
- Request changes, the main counterpart to Approve. The change order stays
  open.
- Reject and close, a secondary action in the menu of the Request changes
  split button.
- Request changes and Reject and close each need a reason.
- An agent never rejects. It can only leave blocking comments (ADR 128).

The project manager decided for the owner that the approvals across the
gates of one phase come from distinct people (ADR 116 decision 6). ADR
114 decision 4 gives one requirement for each `approve` decision of a
policy, with a number and the groups of the approvers.

## Decision

1. **A review.** A review (`changeorder.Review`) is one submit of one
   reviewer on one revision. It has an ID with the prefix `cor-`, the
   subject of the reviewer, the agent reference when an agent submitted
   it, the revision number, the digest that the reviewer saw, a verdict, a
   summary, the threads that it opened, and the time. A review does not
   change after its submit. The summary is a comment (ADR 193 decision 2):
   the review holds the ID of that comment (`coc-`), and the text is a
   history item with its own item key (ADR 198 decision 3). So a redaction
   reaches the summary and the reason of a review, as for each comment.
   The audit entry of the review holds the SHA-256 of the summary and the
   comment ID, never the text (ADR 197 decision 6).
2. **Four verdicts.**

   | Verdict | Who | Reason | Effect |
   | --- | --- | --- | --- |
   | `approve` | an eligible approver (decision 5), never an author, never an agent | optional | One signature on the current revision, at most one for each principal (ADR 191 decision 5). |
   | `request-changes` | an eligible approver, never an author, never an agent | required | Opens at least one request-changes thread. The change order stays open (owner). |
   | `comment` | a principal with `changeorder:comment` on each unit, or an agent of one | optional | Opens comment threads, and replies. It can also open blocking threads, for an eligible approver or an agent of an eligible approver (ADR 193 decision 4). |
   | `reject-and-close` | an eligible approver, never an author, never an agent | required | The change order ends `closed` (owner). |

   A reason is the summary of the review: 1 to 1,000 characters of plain
   text with no control characters, as the reason of ADR 151 decision 27.
   An empty reason for `request-changes` or `reject-and-close` fails with
   `changeorder.ErrReasonRequired` (owner).
3. **Request changes opens a thread.** A review with the verdict
   `request-changes` opens each blocking thread of its draft (ADR 193). A
   draft with no blocking thread gets one thread with the summary as its
   text, anchored to the whole plan. So a request for changes always has a
   thread that blocks until a person resolves it. A review with the verdict
   `approve` and a blocking thread in its draft fails with
   `changeorder.ErrInvalid`: one review cannot both approve and block.
   An agent records no verdict (ADR 128 decision 10). It submits its
   blocking threads in a review of the verdict `comment`. That review
   blocks approval through its threads, and records no verdict of the
   agent. `changeorder.CheckAgent` (ADR 196 decision 10) does not change.
4. **Requirements.** At the end of each attach, when the revision is
   `ready`, the server asks the policy engines that the namespace binds to
   the decision point `changeorder.review` (ADR 114). The input of the
   kind `changeorder.review`, schema version 1, holds the authors, the
   namespace, each unit reference, its labels, its operation and the
   counts of the change kinds of its plan (ADR 194). It holds no value of
   a resource and no secret. `policy.Combine` joins the decisions
   (ADR 114 decision 4):
   - `deny` blocks the approval of the revision. The change order stays
     `in-review` with the reasons of the policy, and the store refuses
     each `approve` with `changeorder.ErrPolicyDenied`. An author can
     attach a new revision.
   - `approve` gives the requirements: for each one, a number of
     approvals and the groups of the approvers.
   - `allow` gives no requirement of its own.

   A change order always needs at least one approval by a person who is not
   an author (`changeorder.MinApprovals`). This rule always applies, also
   when a policy gives requirements. It is a check of `changeorder.Approved`
   (ADR 190 decision 7), not one more slot of the matching. With no
   policy, with `allow`, or with `approve` and no requirement, the server
   adds one requirement of one approval with no groups. So a stored set of
   requirements is never empty. A requirement with a number below 1 or
   above 10, or more than 10 requirements, is not a valid decision: the
   store records the policy of the revision as failed, with the reason
   `invalid-requirement`. The durable operation does not retry the same
   decision: a retry succeeds only after the policy changes. A change order
   with no approval would be a set of jobs. The revision records each
   decision as ADR 114 decision 11 says. A policy that fails is not a
   decision. The revision stays `in-review` with no requirement, and the
   store refuses each `approve` with `changeorder.ErrPolicyFailed`, until a
   retry of the operation succeeds.

   A unit plan whose summary is `unreadable` (ADR 194 decision 6) blocks the
   approval of the revision, also with no policy and with `allow`. The store
   refuses each `approve` with `changeorder.ErrSummaryUnreadable`, which
   names the unit. The checks fail closed. An author attaches a new
   revision. A reviewer can attach one too, and then becomes an author (ADR
   190 decision 4).
   Before the operation `changeorder.decide` stores the decisions, the
   store refuses each `approve` with `changeorder.ErrNotReady`.

   `changeordertest.RunStore` gets a case for each path: an approve while
   the decisions are not stored, an approve after a policy failed, an
   approve after a `deny`, a set of decisions with no requirement (the
   server adds the default requirement), a requirement with the number 0,
   a `mandatory` failed result of a resource policy, an unreadable policy
   report, an unreadable summary with no policy, an `invalid-requirement`
   decision and its retry with the same policy, and the `MinApprovals`
   check with requirements from a policy.
5. **Eligible approvers.** A principal is an eligible approver of a change
   order when all of these are true:
   - it holds `changeorder:approve` on each unit of the current revision,
   - it is a member of the groups of at least one requirement, or a
     requirement has no groups,
   - it is not an author (ADR 190 decision 4).

   An approve, a request for changes and a reject by a principal that is
   not eligible fail with `changeorder.ErrNotEligible`. An author fails
   with `changeorder.ErrSelfApproval`, also when the author is a member of
   each group (owner). `changeorder.Eligible` holds the rule, and a
   table-driven test checks it.
6. **Distinct people across requirements.** The signatures on the current
   revision meet the requirements only when each signature counts for at
   most one requirement. One approver who is a member of two groups meets
   one requirement, not both. The store solves this as a bipartite
   matching: each requirement has as many slots as its number, and a
   signature fills one slot of a requirement whose groups hold the
   approver. `changeorder.Match` finds a maximum matching with augmenting
   paths. The requirements are met when the matching fills each slot.
   `Match` counts distinct subjects: two signatures of one subject fill at
   most one slot. The store refuses a second signature of a principal on
   one revision (ADR 191 decision 5), and `Match` does not trust that
   check. `Match` is deterministic: it takes the signatures in the order of
   their time, and the requirements in the order of `policy.Combined`. The
   answer names, for each requirement, the signatures that fill it and the
   count that it still needs. A table-driven test covers two groups with
   one shared member, a person in each group, a requirement with no
   groups, more signatures than slots, and two signatures of one subject
   on a requirement of two approvals (one slot filled).
   `changeordertest.RunStore` gets a case for a second `approve` by the
   same principal on the same revision, which fails with
   `changeorder.ErrInvalid`. The bounds keep the cost low:
   at most 10 requirements of at most 10 approvals each, and at most 100
   signatures on one revision.
7. **Open threads block approval.** While a blocking thread is open, the
   store refuses each `approve` with `changeorder.ErrChangesRequested`,
   and names the threads (ADR 128 decision 12). An `approve` that a
   reviewer recorded before the thread opened stays. It counts again when
   no blocking thread is open. An outdated thread still blocks (ADR 193
   decision 8).
8. **Close by an author.** An author can close a change order that is not
   terminal and not `applying`, with a reason, with `changeorder:author` on
   each unit. This is a withdrawal, not a rejection. The record names the
   kind of the close: `rejected` or `withdrawn`. The reason of a
   withdrawal is in the audit entry of the close (ADR 197 decision 6), and
   a redaction cannot reach it. The reason of a `reject-and-close` is the
   summary of its review, a comment that a redaction can reach (decision
   1).
9. **A signature stops to count.** A signature of a principal that lost
   `changeorder:approve` on a unit, or that lost the membership of each
   group of its requirement, stops to count. Its record keeps the cause
   `access-lost`. An approved change order then moves back to `in-review`
   (`signature-void`, ADR 190 decision 8). This rule is the same as the
   rule of a view at a time: the access of now decides (ADR 150 decision
   14). The exact rule:
   - Before each `Submit`, `Resolve`, `Reopen` and `StartApply`, the
     service reads the access data and computes the eligibility
     (`changeorder.Eligibility`): for each signer of the current revision
     and for the actor, the requirements whose groups hold that principal,
     or none when the principal is not eligible. It also reads the access
     version of each of these principals (`identity.AccessVersion`): a
     counter for each principal that each change of a grant, a role or a
     group membership that reaches that principal makes higher. There is
     no global counter, so a change for one principal does not fail the
     writes of other principals.
   - The store gets the eligibility and the access versions. In its
     transaction, it reads the access versions of the actor and of each
     signer again. A version that changed fails the write with
     `changeorder.ErrConflict`, and the service computes the eligibility
     again, at most 3 times. So a revoke that commits between the check
     and the commit never counts a signature that the revoke voided.
   - The store applies the eligibility in the transaction: it voids each
     signature whose principal is no longer eligible, then computes
     `Approved`.
   - A write with no eligibility (a reply, the analysis, the decisions, an
     `ObserveJob`) voids no signature. The schedule `changeorder.reconcile`
     (ADR 190 decision 12) applies the rule each hour to each change order
     in `in-review` or `approved`. An apply request applies it in its own
     transaction.
10. **The console.** The review panel has two main buttons: Approve, and a
    split button with Request changes. The menu of the split button holds
    Reject and close (owner). Request changes and Reject and close each
    ask for a reason before the submit. The console shows each
    requirement, the people that fill it, and the count that it still
    needs.

## Consequences

- One approver cannot fill two requirements. A team that wants one
  person from `security` and one from `dba` gets two people, also when
  one person is in both groups.
- A reviewer who sees a problem and does not want to end the change order
  requests changes. Reject and close is for a change that must not
  happen.
- With no policy, each change order needs one approval by a person who is
  not an author. The job model with no policy needs none (ADR 103). This
  is a choice of the change order design, and a namespace cannot lower it.

## Open questions

- Does a namespace setting allow a change order with no approval, for
  example for a sandbox namespace? Version 1 always needs one.
- Does the approval of a change order with a `destroy` unit need a fresh
  authentication of the approver?
- Does a review by a person who holds `changeorder:approve` on some units
  only count for those units? Version 1 needs the action on each unit.
