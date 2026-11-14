## Context

Apply after the merge is the default. Pre-merge apply is an explicit per-unit option for teams that accept the risk of applying unmerged code. The plan on the exact branch head comes from the pull request automation work. The review request approval comes from the approval mirror work.

## Decisions

1. **Pre-merge apply reuses applyTiming.** Reuse the unit value applyTiming with value pre-merge for the option and value post-merge as the default, stored on unit settings and shown as its effective value in the API.
2. **Branch apply needs approval and plan on the exact commit.** An approved review request starts an apply job from the branch only when the unit sets applyTiming pre-merge, the approval targets the current head commit SHA, and a plan exists on that exact commit SHA.
3. **Pin SHA and re-check before first change.** Pin the approved and planned commit SHA in the job. Re-check the branch head at job start and again before the first change; when the head moved, refuse or stop instead of applying unreviewed code.
4. **Toggle does not stop a running job.** A change of the option while an apply runs does not stop that run; the check applies at start and before the first change only.
5. **Units decide alone.** Each unit carries its own value with no cross-unit rule; disagreement means one unit applies from its branch and the other waits for merge.
6. **Docs state the option is not recommended.** The docs page states that the project does not recommend pre-merge apply because it applies code that no merge reviewed.

## Structure

Unexported type preMergeGuard built only by its constructor, which checks the unit applyTiming value, the approval target SHA, the plan SHA, and the pinned branch SHA before a job starts and before the first change.

## Risks

- A team enables the option and applies unmerged code.
- The branch head moves after approval, at job start, or before the first change.
