# 193. Threads, anchors and draft reviews

Status: accepted · 2026-10-18 · part of ADR 190. Decisions 4 and 9 read the eligible approver of ADR 225 decision 7

Changed by:

- ADR 222 changes decisions 4 and 9: the eligible approver comes from approval
  rules.
- ADR 225 changes decisions 4 and 9: eligible approver comes from approval
  rules.

## Context

ADR 128 decision 11 to decision 14 give request-changes threads on a job:
who opens one, what it blocks, who resolves it, and the reason of a
dismissal. A change order holds the plan of many units, and a reviewer
comments on parts of it. The owner decided (owner):

- One thread for each concern.
- The anchor of a thread is the whole plan, one resource, several
  resources, or a group.
- Threads queue as a draft review, and the reviewer submits them together.
- Threads follow their resource addresses into the next plan. When a
  resource is not in the next plan, the thread is outdated, and it still
  blocks until a person resolves it.
- A request-changes thread blocks approval until the reviewer or another
  approver resolves it. The author never resolves it, also when the author
  is an approver. An agent never resolves it. The audit log records each
  dismissal with its reason.

## Decision

1. **A thread.** A thread (`changeorder.Thread`) has an ID with the prefix
   `cot-`, an anchor, the flag `Blocking`, the principal that opened it
   with the agent reference if any, the revision where it opened, the
   revision of its anchor now, a status (`open` or `resolved`), the flag
   `Outdated`, and its comments. A blocking thread is a request-changes
   thread (ADR 128 decision 11). A thread that does not block is a comment
   thread.
2. **A comment.** A comment (`changeorder.Comment`) has an ID with the
   prefix `coc-`, the author with the agent reference if any, the time and
   the text. The text is 1 to 4,000 bytes of UTF-8 (ADR 128 decision 11).
   The server treats it as untrusted text under the rules of ADR 124
   decision 13. Each answer to an agent, and the console, label it with
   its author, as data, never as instructions. A comment does not change
   after its write. A redaction can destroy its text (ADR 198 decision 3).
3. **Anchors.** An anchor (`changeorder.Anchor`) has one of three kinds:

   | Kind | Holds | Example |
   | --- | --- | --- |
   | `plan` | nothing more | "Why do we change this now?" |
   | `resources` | 1 to 50 resource references | "This subnet must not be public." |
   | `change-group` | one change group key (ADR 194 decision 5) | "These 40 tag changes are fine, except the cost center." |

   A resource reference (`changeorder.ResourceRef`) holds the unit ID and
   the key of the resource in the plan of its tool: the URN for Pulumi,
   the resource instance address for OpenTofu. This is the resource
   identity of blame (ADR 150 decision 5). The core never parses the key.
   It compares keys as bytes. An anchor of the kind `resources` names
   resources that have a change in the plan of the revision. Else the
   submit fails with `changeorder.ErrInvalid` and names the reference.
4. **Who opens a thread.** A blocking thread needs an eligible approver
   (ADR 192 decision 5), or an agent of an eligible approver. An author
   cannot open a blocking thread on the change order. That fails with
   `changeorder.ErrAuthorThread`, also through an agent (ADR 128 decision
   11). A comment thread needs `changeorder:comment` on each unit. An
   author can open comment threads and reply to each thread.
5. **A draft review.** Each reviewer has at most one draft review on each
   change order (`changeorder.Draft`). The store keys a draft by the change
   order, the subject and the agent reference, which is empty for a
   person. So a person and each agent of that person have different
   drafts. An agent never reads or submits the draft of its person. It holds the threads and the
   replies that the reviewer wrote and did not submit, each with its
   anchor. Only its reviewer reads it. The server keeps it, so a reviewer
   can continue on another device. A draft holds at most 100 items. A
   write of a draft writes no audit entry and sends no notification: a
   draft changes nothing until its submit. A draft is not in a backup: the
   backup of a namespace leaves out the table of drafts (ADR 198 decision
   4). The store deletes a draft at its submit, at the end of its change
   order, or 30 days after its last write.
6. **One submit.** A submit of a review (ADR 192) writes, in one fenced
   transaction: the review, each thread and reply of the draft, the
   signature for an `approve`, the change of the state, the audit entries
   and the notifications. Then it deletes the draft. A submit names the
   digest that the reviewer saw. A digest that is not the digest of the
   current revision fails with `changeorder.ErrPlanChanged` for an
   `approve`, and for a `reject-and-close` of a `ready` revision. A
   `reject-and-close` while the current revision is `planning` or
   `plan-failed` names no digest: it closes the change order and signs
   nothing. For `request-changes` and `comment`, the store checks each
   anchor of the draft against the revision that the reviewer saw, which
   is the revision of the digest in the submit. An anchor that names no
   change of that revision fails with `changeorder.ErrInvalid`. While the
   current revision is `planning`, the submit names the digest of the last
   `ready` revision that the reviewer saw. The threads keep the revision
   that the reviewer saw as the revision of their anchor. When the next
   revision is `ready`, they follow it with the rule of decision 7, and
   can become outdated (decision 8). If that revision is already `ready`
   at the submit, the store maps them in the transaction of the submit.
   So a reviewer who reads an old revision can still ask for changes.
7. **Threads follow their anchors into the next plan.** When a new
   revision becomes `ready`, the store maps each thread that is open to
   the new revision, in the transaction that marks the revision `ready`:
   - An anchor of the kind `plan` always maps.
   - An anchor of the kind `resources` maps each reference whose unit and
     key have a change in the new plan.
   - An anchor of the kind `change-group` maps when the new plan has a
     change group with the same key (ADR 194 decision 5).

   `changeorder.Follow` holds the rule, and a table-driven test checks it.
   A thread whose anchor maps in full gets the new revision as the revision
   of its anchor. A thread that resolved before stays resolved, and keeps
   the revision of its anchor.
8. **Outdated threads still block.** A thread is outdated when its anchor
   does not map in full. That is, at least one resource reference has no
   change in the new plan, or the change group key is not in the new plan.
   The thread keeps its old anchor and the revision of that anchor. It names
   each reference that did not map. A blocking thread that is outdated
   still blocks approval until a person resolves it (owner). The console
   shows it with the label "Outdated", with the old plan of its anchor
   while the history model keeps that plan.
9. **Who resolves a thread.** `changeorder.Service.Resolve` closes a
   thread. For a blocking thread it accepts only these, each in a request
   with no agent reference (owner, ADR 128 decision 13):
   - the principal that opened the thread, while that principal still
     holds `changeorder:approve` on each unit of the current revision, and
     is not an author,
   - an eligible approver of the change order.

   A principal that opened a blocking thread and then lost
   `changeorder:approve` on a unit, or that became an author with an
   attach, fails with `changeorder.ErrNotEligible`, or with
   `changeorder.ErrSelfResolve` for an author.

   It refuses each author with `changeorder.ErrSelfResolve`, also when the
   author holds `changeorder:approve` and is a member of each group. It
   refuses each request with an agent reference with
   `changeorder.ErrAgentDenied`, also for a thread that an agent of the
   same person opened. A thread that an agent opened has the person of the
   agent as the principal that opened it. The principal that opened a
   comment thread, and each eligible approver, can resolve it.
10. **A dismissal needs a reason (owner).** A resolve of a blocking thread
    by an approver other than the principal that opened it is a dismissal
    (ADR 128 decision 14). A dismissal needs a reason with the limits of
    ADR 192 decision 2, else it fails with `changeorder.ErrReasonRequired`.
    The audit entry of the dismissal holds the reason (ADR 197 decision 6).
    The reason is not a comment, so a redaction cannot reach it. The
    console warns at the input that the reason stays in the audit log.
11. **Reopen.** A reopen of a resolved thread needs a reason. A reopen of
    a blocking thread has the same rule as a resolve (decision 9). It
    accepts only these, each in a request with no agent reference:
    - the principal that opened the thread, while that principal still
      holds `changeorder:approve` on each unit of the current revision, and
      is not an author,
    - an eligible approver of the change order.

    An author fails with `changeorder.ErrAuthorThread`, because an author
    cannot open a blocking thread. A principal that opened the thread and
    lost `changeorder:approve` on a unit fails with
    `changeorder.ErrNotEligible`. A request with an agent reference fails
    with `changeorder.ErrAgentDenied`. The principal that opened a comment
    thread, and each eligible approver, can reopen it. A reopened blocking
    thread blocks again, and an approved change order moves back to
    `in-review`. The audit entry of the reopen holds the reason, and a
    redaction cannot reach it (ADR 197 decision 6).
12. **Limits.** A change order holds at most 500 threads, and a thread at
    most 200 comments. A write above a limit fails with
    `changeorder.ErrLimit`, which names the limit.

## Consequences

- A thread on a resource stays on that resource across new plans. A
  reviewer sees whether the author changed the resource that the thread
  is about.
- A resource that a new plan no longer changes leaves its thread
  outdated, and a person must still resolve it. A fix that takes away the
  change of a resource does not clear a block on its own.
- A reviewer writes many threads before the author sees one. The author
  gets one notification for each submit, not one for each thread.

## Open questions

- Does a thread follow a resource that the tool renamed, for example a
  Pulumi alias or an OpenTofu `moved` block? Version 1 follows the key of
  the resource, so a rename makes the thread outdated.
- Does a reply from an author to a blocking thread notify the person that
  opened it, also during quiet hours (ADR 145 decision 25)?
- Do the anchors get a fourth kind, one unit, for a concern about a whole
  unit?
