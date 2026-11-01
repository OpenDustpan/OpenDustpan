# 300. PM answers on runs, recorded

Status: accepted · 2026-11-01. Decisions 1 to 4 are answers of the project manager under the owner's delegation. The owner accepts them with the merge.

Changes:

- Changes ADR 190 decision 10: a merge queue entry is a request to apply, and the account that queued the entry is the requester.
- Changes ADR 203 decision 5: a job of a unit with an open rule waits. It does not fail.
- Changes ADR 130: the labels inside a pool. It answers the open question of ADR 130 on labels.

## Context

The project manager answered open points on runs. Each answer changes a
decision of an earlier ADR. ADR 1 forbids an edit of an accepted ADR, and
ADR 296 requires a new ADR for a change. This ADR records the answers. It
does not rewrite the text of the earlier decisions.

Each earlier ADR gets a "Changed by" item below its status line. The "Changes"
list above names each change.

## Decision

1. **A merge queue entry is a request to apply.** A merge queue entry for the
   review request of an `approved` change order requests its apply. This
   amends ADR 190 decision 10. The account that added the entry is the
   requester. The requester must be linked to a principal (ADR 146). The
   principal must hold the apply grant on each unit of the revision (ADR 190
   decision 10). The store checks the digest of the current revision as it does
   for any other request. Benchmark: GitHub merge queue records the account
   that enqueued each pull request. A merge queue entry therefore names a
   person, as an apply request must.

2. **A job of a unit with an open rule waits.** A job of a unit whose profile
   has an open rule waits in the approval wait. It does not fail. This amends
   ADR 203 decision 5. ADR 225 decision 5 already gives this behavior. This
   decision records it as the answer of the project manager. Benchmark: the
   code of ADR 225 decision 5 and the gate model of ADR 222 decision 5 use the
   same wait.

3. **Required runner labels match as a subset inside a pool.** A job that asks
   for labels runs on a runner of the pool when the runner holds each required
   label. Two label sets match when the required set is a subset of the labels
   of the runner. The match is exact per label, with no pattern. This answers
   the open question of ADR 130 on labels inside a pool. It changes the
   durable claim query of ADR 101, which the open question of ADR 101 named.
   Benchmark: GitHub Actions `runs-on` matches a job to a runner when the
   runner holds every label that the job names. A runner with more labels still
   matches.

4. **The review watcher does not poll.** The review watcher of RFC 12 item WB-10
   does not use the `job.review-watch` poll. A merge or a close of a review
   request reaches OpenDustpan from the webhook of the Git host (ADR 271). This
   answers RFC 12 item WB-10 in favor of no polling. ADR 299, which is in review, holds the design. Benchmark:
   webhook-driven Git hosts, such as GitHub and GitLab, send a delivery for each
   merge and each close, and they do not need a poll for these events.

## Consequences

- A reader of ADR 190, ADR 203 or ADR 130 finds the change below its status
  line.
- This ADR changes no code. The label match needs a change to the claim query
  of the durable port (ADR 101 open question), which is not part of this ADR.
- Five answers of the project manager on runs are not in this ADR yet. The
  project manager has not given their text in the source of this change.
