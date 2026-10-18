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
   change after its submit.
2. **Four verdicts.**

   | Verdict | Who | Reason | Effect |
   | --- | --- | --- | --- |
   | `approve` | an eligible approver (decision 5), never an author, never an agent | optional | One signature on the current revision (ADR 191 decision 5). |
   | `request-changes` | an eligible approver, or an agent of an eligible approver, never an author | required | Opens at least one request-changes thread. The change order stays open (owner). |
   | `comment` | a principal with `changeorder:comment` on each unit, or an agent of one | optional | Opens threads that do not block, and replies. |
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
   an author (`changeorder.MinApprovals`). With no policy, or with `allow`,
   the server adds one requirement of one approval with no groups. A
   change order with no approval would be a set of jobs. The revision
   records each decision as ADR 114 decision 11 says. A policy that fails
   is not a decision. The revision stays `in-review` with no requirement,
   and the store refuses each `approve` with `changeorder.ErrPolicyFailed`,
   until a retry of the operation succeeds.
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
   `Match` is deterministic: it takes the signatures in the order of their
   time, and the requirements in the order of `policy.Combined`. The
   answer names, for each requirement, the signatures that fill it and the
   count that it still needs. A table-driven test covers two groups with
   one shared member, a person in each group, a requirement with no
   groups, and more signatures than slots. The bounds keep the cost low:
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
   kind of the close: `rejected` or `withdrawn`.
9. **A signature stops to count.** At each write, the store reads the
   signatures of the current revision again with the access of now. A
   signature of a principal that lost `changeorder:approve` on a unit, or
   that lost the membership of each group of its requirement, stops to
   count. Its record keeps the cause `access-lost`. An approved change
   order then moves back to `in-review` (`signature-void`, ADR 190
   decision 8). This rule is the same as the rule of a view at a time:
   the access of now decides (ADR 150 decision 14).
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
