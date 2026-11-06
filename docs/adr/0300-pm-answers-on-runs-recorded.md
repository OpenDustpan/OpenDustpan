# 300. PM answers on runs, recorded

Status: accepted · 2026-11-01. Decisions 1 to 9 are answers of the project manager under the owner's delegation. The owner accepts them with the merge.

Changes:

- Changes ADR 190 decision 10: a merge queue entry is a request to apply, and the account that queued the entry is the requester.
- Changes ADR 203 decision 5: a job of a unit with an open rule waits. It does not fail.
- Changes ADR 130: the labels inside a pool. It answers the open question of ADR 130 on labels.
- Extends ADR 191: after a merge, a stored plan applies only when the tree is the same.
- Changes ADR 254 decision 6: the log read of OpenTofu uses a signed URL for each job, phase and principal.
- Changes ADR 213 decision 10: a manual lock can cover the whole installation.
- Changes ADR 213 decision 2: a plan that starts from the Git host never gets the secrets of the first bullet.
- Changes ADR 101 decision 1: a job with no profile is valid. It runs with no cloud credentials.
- Changes ADR 271 decision 7: a third comment command, `dustpan plan`.

## Context

The project manager answered open points on runs. Each answer changes a
decision of an earlier ADR, or answers an open question of one. ADR 1 forbids
an edit of an accepted ADR, and ADR 296 requires a new ADR for a change. This
ADR records the answers. It does not rewrite the text of the earlier decisions.

Each earlier ADR gets a "Changed by" item below its status line. The "Changes"
list above names each change.

## Decision

1. **A merge queue entry is a request to apply.** A merge queue entry for the
   review request of an `approved` change order requests its apply. This
   changes ADR 190 decision 10. The account that added the entry is the
   requester. The requester must be linked to a principal (ADR 146), and the
   principal must hold the apply grant on each unit of the revision. The store
   checks the digest of the current revision as it does for any other request.
   Benchmark: GitHub merge queue records the account that enqueued each
   pull request.

2. **A job of a unit with an open rule waits.** A job of a unit whose profile
   has an open rule waits in the approval wait. It does not fail. This changes
   ADR 203 decision 5. ADR 225 decision 5 already gives this behavior. This
   decision records it as the answer of the project manager. Benchmark: the
   wait of ADR 225 decision 5 and the gate model of ADR 222 decision 5 use the
   same wait.

3. **Required runner labels match as a subset inside a pool.** A job that asks
   for labels runs on a runner of the pool when the runner holds each required
   label. The required set must be a subset of the labels of the runner. The
   match is exact per label, with no pattern. This answers the open question of
   ADR 130 on labels inside a pool. The durable claim query of ADR 101 needs a
   change to apply it. Benchmark: GitHub Actions `runs-on` matches a job to a
   runner when the runner holds every label that the job names. A runner with
   more labels still matches.

4. **The review watcher does not poll.** The review watcher of RFC 12 item WB-10
   does not use the `job.review-watch` poll. A merge or a close of a review
   request reaches OpenDustpan from the webhook of the Git host (ADR 271). This
   answers RFC 12 item WB-10 in favor of no polling. ADR 299, which is in review,
   holds the design. Benchmark: webhook-driven Git hosts, such as GitHub and
   GitLab, send a delivery for each merge and each close, so they need no poll
   for these events.

5. **A stored plan applies after a merge only on the same tree.** After a merge,
   the server reads the new head of the default branch. A stored plan applies
   only when the root tree of the new head equals the root tree of the test
   merge that the approved revision planned. Otherwise the change order plans
   again and needs a new approval. This extends ADR 191. Benchmark: a Git tree
   hash equality is a content identity, and merge queues test the merge result.

6. **The log read of OpenTofu uses a signed URL.** OpenTofu sends no token to
   `log-read-url`. The log read therefore uses an HMAC-signed URL. The URL is
   bound to one job, one phase, one principal and one cap. It is valid for the
   phase plus 15 minutes. The server checks `job:read-logs` when it mints the
   URL and again when it reads through the URL. This changes ADR 254 decision 6.
   Benchmark: S3 presigned URLs are short-lived signed URLs, and GitHub Actions
   log download redirects to a short-lived signed URL.

7. **A manual lock can cover the whole installation.** A person adds a manual
   lock with the scope `installation` and the action `installation:lock`. A
   reason is required. While the lock holds, it stops applies in the whole
   installation. This changes ADR 213 decision 10. Benchmark: change freezes,
   such as the GitLab deploy freeze and run blocking in HCP Terraform, stop
   changes across a scope.

8. **A job with no profile may run.** A runner job with no profile attachment
   is valid. It runs with no cloud credentials. This changes ADR 101 decision 1,
   which named "no profile" as an invalid spec. Benchmark: GitHub Actions and
   GitLab jobs run without cloud credentials when no credential is configured.

9. **A third comment command, `dustpan plan`.** `dustpan plan` plans the current
   head of a review request again. It is added to the two commands of ADR 271
   decision 7. A plan that starts from the Git host never gets the secrets of the
   first bullet of ADR 213 decision 2, whoever starts it. This changes ADR 271
   decision 7 and ADR 213 decision 2. Benchmark: `atlantis plan` and the plan
   command of Digger run a plan from a comment.

## Consequences

- A reader of ADR 190, 203, 130, 191, 254, 213, 101 or 271 finds the change
  below its status line.
- This ADR changes no code. The label match needs a change to the claim query
  of the durable port. The signed log URL, the installation lock and the plan
  command need their own code and tests.
