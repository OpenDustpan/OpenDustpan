## Context

The merge event starts one apply job per touched unit. The job pins the default branch name and the merge commit sha at creation, then the executor checks out that sha.

## Decisions

1. **Pin the commit at job start.** Start the apply only from a merge event on the default branch, with the merge commit sha stored on the job. A later push to the default branch starts a new job and never moves a running job.
2. **Replan before apply.** Reuse the plan-then-apply chain of pull-request-automation-first-slice and the approval reset of policy-approvals: replan at the merge commit before any apply, with no new reset rule in this change.
3. **Deduplicate merge events.** Record the merge event id with the created jobs in the same write, so a replayed event or a restart creates no second job per unit. A retry after a partial write creates only the missing unit jobs.

## Structure

Structure: unexported field pinnedCommit in the merge-apply starter, set only by constructor newMergeApply(branch, sha) which rejects an empty branch, an empty sha, or a branch that is not the default branch of the unit.

## Risks

- A push between merge and job start leaves the merge commit behind the branch head.
- A gate that passed on the branch fails at the merge commit.
