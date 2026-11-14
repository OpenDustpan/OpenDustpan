# Stacked pull requests

A stack is a chain of pull requests (PRs). Each PR has the branch of the PR below it as its base. The bottom PR has `main` as its base.

Only the lead and the Foreman make stacks. A worker never makes a stack. A model never makes a stack.

## When to stack

Stack only when a change depends on another change that is not yet merged, and the two changes cannot be one PR.

Do not stack when:

- The changes do not depend on each other. Open separate PRs against `main`.
- One PR is small enough for review. Make one PR.
- A worker owns the change. Give the worker one PR.

## Rules

- Do not run `make check` for each layer. CI is the gate.
- Do not subscribe to the PRs of a stack.
- Do not rebase. Do not force-push. Use a merge commit.
- Do not use `gh stack submit`. It uses GraphQL, and the cloud proxy blocks GraphQL.
- Do not use a command that uses GraphQL.

## Commands

Use `scripts/stack.sh`. It uses `gh api` (REST) only.

1. Make the branches. Each branch starts on the tip of the branch below it.
2. Push the branches with `git push -u origin <bottom> <next>...`. Use one plain push. A force-with-lease push fails on a new branch.
3. Write one body file for each branch. Name the file `<branch>.md`, with `/` in the branch name changed to `-`.
4. Run `scripts/stack.sh create --bodies <dir> <bottom> <next>...`. The command:
   - checks that each branch is pushed and equal to its remote branch;
   - checks that the base of each existing PR is the branch below;
   - opens a draft PR for each branch that has none;
   - links the PRs as one stack, bottom to top;
   - prints the stack URL.
5. Run `scripts/stack.sh status` to list the stacks and the state of each PR.
6. The command marks each new PR ready for review with `gh api -X POST repos/OpenDustpan/OpenDustpan/pulls/<n>/ccr/ready_for_review`. If this call fails, the PR stays a draft. Mark it ready by hand.

## Merge rule

Only the bottom PR enters the merge queue. The merge method is squash.

When the bottom PR is merged, run `scripts/stack.sh advance <stack>`. The command:

1. refuses to run if the bottom PR is not merged;
2. sets the base of the next PR to `main`;
3. merges `main` into the branch of the next PR with a merge commit;
4. pushes the branch;
5. links the remaining PRs again.

If the merge has conflicts, the command stops and aborts the merge. Resolve the conflicts by hand, push, and run the command again.

## Unstack

Run `scripts/stack.sh unstack <stack>` to remove the stack link. The command calls `POST stacks/<n>/unstack` and does nothing more: each PR keeps its base and its branch. After an unstack, the agent repairs the PRs (base, merge of `main`, push).

## What the test showed

A test used throwaway branches `stacktest/base`, `a`, `b`, `c` and `d`, and four draft PRs.

- `POST stacks` with PR numbers, bottom to top, linked three PRs. The response has a stack number.
- `POST stacks/<n>/add` with `{"pull_requests":[<number>]}` added a fourth PR on top of the stack. It works.
- `POST stacks/<n>/unstack` removed the stack. The PRs stayed open.
- A squash merge of the bottom PR with REST was refused by the permission classifier (reason: merge without review). The test did not try another way. What GitHub does to the next PR after a squash merge (base change, stack kept) is **not tested**. The `advance` command does not rely on it: it sets the base and links the stack again.
- A local simulation of the squash on `stacktest/base`, then a merge of `stacktest/base` into `stacktest/b`, gave an add/add conflict. In the test, layer b changed a file that layer a created. The squash commit has the same content as layer a, but Git has no common history for it.
- A test did not delete the branches `stacktest/*`: the proxy refuses `DELETE` on refs. The PRs are closed. The owner must delete the five branches.

## Known limits

- A squash merge of the bottom PR can cause conflicts in the next branch when both change the same lines. Expect to resolve them by hand.
- The result of a squash merge of the bottom PR on the rest of the stack is unknown (known gap). Run `scripts/stack.sh status` after each merge.
- The call that marks a PR ready is not tested on an open draft. On a closed draft PR it returned HTTP 403.
- The commands need `gh` and `jq`.
- The cloud proxy blocks GraphQL and the deletion of branches.
