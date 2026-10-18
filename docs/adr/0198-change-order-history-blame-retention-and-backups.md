# 198. Change order history, blame, retention and backups

Status: accepted · 2026-10-18 · part of ADR 190

## Context

The history model gives blame and the point-in-time views (ADR 150), item
keys and redaction (ADR 151), and the retention of plans and job logs (ADR
152). ADR 147 decision 9 says that a job is the change record. ADR 128
decision 18 says that blame finds the agent of a change through the job of
its history item. A change order adds four kinds of data: its record, the
summaries of its unit plans (ADR 194), its comments (ADR 193), and its
signatures (ADR 191).

## Decision

1. **Blame names the change order through the job.** A state item records
   the job ID in its actor (ADR 150 decision 3). The job holds its change
   order origin (ADR 190 decision 5). So blame and the point-in-time views
   show, for each change that an apply job made: the change order, its
   title, the revision, and the approvers whose signatures met the
   requirements at the apply. `history.Actor` gets no new field. A reader
   of blame sees the change order only with `changeorder:read` on each of
   its units. Else blame shows the job ID only, as today.
2. **The summary of a unit plan goes with its stored plan.** The content of
   the summary (value paths and masked values) has the item key of the
   stored plan that it came from (ADR 194 decision 6). So:
   - a redaction of the plan shreds the content of its summary, in the
     same shred,
   - the end of the retention of the plan does the same (ADR 152 decision
     5),
   - a read of a shredded summary answers the metadata, with the mark
     `redacted` and no value.

   The metadata of the summary (keys, types, change kinds, counts and
   group keys) stays until the purge of its unit, as the metadata of a
   stored plan does (ADR 152 decision 7). The purge of a unit deletes the
   metadata rows of that unit, and the change order keeps the unit
   reference with its name.
3. **A comment is a history item.** A comment is each comment of a thread,
   and the summary of each review, with its reason (ADR 192 decision 1).
   Each comment has its own item key of
   the new item kind `comment` (`secret.ItemKindComment`), with the scope
   of the namespace of the change order. The server writes the wrapped key
   blob first, then the comment and the key record in one fenced
   transaction (ADR 151 decision 3). So a person who pastes a secret in a
   comment can have it destroyed, also in each backup target:
   - A redaction of a comment needs `unit:redact` on each unit of the
     change order, and a reason (ADR 151 decision 27). It shreds the item
     key and writes the shred ledger in each backup target (ADR 151
     decision 19). The comment keeps its author, its time and the SHA-256
     of its text. Its text reads as `[redacted]`.
   - The change order implements `history.Redactor` for comments. Its
     conformance suite is `historytest.RunRedactor` with the new table
     `historytest.CommentSituations()`.
   - `history.ItemRef` names a unit today. A comment names a change order.
     The history model gets a second owner kind for an item reference,
     `change-order`, with the namespace and the change order ID. The
     `history` package changes before the comment store lands.
   - A comment has no retention period. It stays until the final delete of
     the namespace, as the record of the review. **One-way door:** backups
     hold the sealed text of each comment.
4. **The record stays.** The change order, its revisions, its unit plans,
   its reviews, its signatures, its threads with their anchors and the
   metadata of its comments stay until the final delete of the namespace.
   They hold IDs, hashes, digests, names, times and reasons. They never
   hold a value of a plan. The store deletes a draft at its submit, at the
   end of its change order, or 30 days after its last write. A draft is
   not sealed with an item key. So no backup holds it: the backup of a
   namespace leaves out the table of drafts, and a restore gives no draft.
   A redaction cannot reach a copy of a draft text, so no such copy leaves
   the database.
5. **Backups and restore.** The tables of the change order are data tables
   of the namespace (ADR 196 decision 6). A backup holds them, except the
   table of drafts (decision 4), and the
   portable format (ADR 42) gets them as tables of the namespace. A
   restore applies each redaction of a comment, as for every item kind
   (ADR 151 decision 21). After a restore, a change order can name a job
   that the restore did not keep, or a durable operation that ended. The
   schedule `changeorder.reconcile` reads each job of a change order that
   is not terminal. A job that does not exist makes its unit plan or its
   apply `failed` with the reason `restored`. An apply never starts again
   because of a restore: the apply operation starts each apply job with
   its idempotency key, and the job model never repeats a change (ADR 100
   decision 9).
6. **The change record.** For a change under a change order, the change
   order and its jobs are the change record. The export of ADR 147
   decision 11 answers one record for each change order that applied, in
   part or in full. It holds the authors, each revision with its digest,
   the signatures that met the requirements, the reviews, the dismissals
   with their reasons, the apply jobs with their results, the freeze and
   the break-glass. A job of a change order is not a record of its own. A
   job with no change order stays its own record, as ADR 147 decision 9
   says.
7. **The point-in-time view of a change order.** A read of a superseded
   revision answers its unit plans, its digest and its signatures, with
   the cause `new-plan` on each void signature. The review view of an old
   revision answers what its stored plans still hold. A redacted or
   expired plan answers the metadata of its summary only.

## Consequences

- A team reads, from the blame of a resource, the change order and the
  people who approved it.
- A secret in a comment can be destroyed in each copy that the server
  controls. A copy in a notification is not possible, because a
  notification never holds a comment text (ADR 197 decision 7).
- The change order record grows with each review and each comment. The
  sizing guide states the growth for a change order of 100 units and 500
  threads.
- The `history` and `secret` packages change: the item kind `comment`, the
  owner kind `change-order` of an item reference, and the situations table
  of comments.

## Open questions

- Does a comment get a retention period, for example the period of the
  audit log (ADR 68)?
- Does the purge of the last unit of a change order also purge the change
  order, or does the change order stay until the final delete of the
  namespace?
