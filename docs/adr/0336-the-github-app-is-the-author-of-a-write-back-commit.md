# 336. The GitHub App is the author of a write-back commit

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 271: answers the open question 4, how the GitHub adapter signs a commit. It also sets the rule for a branch head that moved.

## Context

ADR 271 leaves question 4 open: how the GitHub adapter signs a commit. RFC 12 questions 4 and 5 give the options. For the commit, the options are the GraphQL mutation `createCommitOnBranch`, the Git data API and the contents API. For a branch head that moved, the options are to read again and retry, or to fail at once.

A branch rule can require signed commits. Such a rule refuses a commit from the Git data API or the contents API, unless the server holds a signing key. `createCommitOnBranch` makes one commit for many files. It checks the head of the branch with `expectedHeadOid`. GitHub signs the commit when a GitHub App calls it.

On 7 November 2026 the project owner decided these points.

## Decision

Decided by the project owner on 7 November 2026.

1. **The App is the author, and the person is a co-author (owner, 2026-11-07).** A write-back commit on GitHub has the GitHub App as its author. The message has a `Co-authored-by` trailer that names the person who asked. The job record and the audit entry stay the record of who asked and why.
2. **GitHub signs the commit (owner, 2026-11-07).** GitHub signs the commit. The server holds no signing key for a write-back commit. The adapter makes the commit with `createCommitOnBranch`. This is a detail of the design of the adapter, not an owner decision.
3. **The write-back follows a moved head at most 3 times (owner, 2026-11-07).** The head of the branch can move after the base. Then the write-back may read the files again at the new head. It may try again. It makes at most 3 retries.
4. **A retry has two conditions (owner, 2026-11-07).** The write-back retries only when both are true:
   - The move does not touch the files that the write-back changes.
   - The new head descends from the base.

   If one condition is false, or the 3 retries are gone, the write-back fails. The adapter uses `job.ErrConflict` for this. That error is a detail of the design, not an owner decision.

## Consequences

- The author on GitHub is the App, not the person. The console and the guide must say so.
- A branch rule "require signed commits" works with no key in the server.
- The server checks the files and the descent at each retry. The check of `expectedHeadOid` by GitHub stays as a second layer.
- This ADR does not decide the merge method of `MergeReview`. That question stays with RFC 12.
- This ADR does not decide the behavior of other Git hosts. The adapter for each host answers question 4 for itself.
