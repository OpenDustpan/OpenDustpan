# Merge session

A merge session merges one batch of pull requests into `main`. The PM gives
the batch: at most 8 pull requests, each with a CLEAN review on its current
head. Do the steps below in order. The steps need no answer from the PM. Send
the PM one report at the end, or one report when you stop.

## Rules

- Do not merge a pull request that is not in the batch.
- Do not rebase, amend, force-push, stash or reset.
- Do not run a package subset as a merge gate. The gate is the full
  `make check` and the full `make e2e` on the combined tip.
- Do not skip, disable or delete a test to make a gate pass.
- Push with one plain `git push` command. Put no other command in it.
- If a permission or safety classifier refuses a command, stop. Report the
  refusal text to the PM.

## 1. Start

1. Run `make agent-preflight`. Fix each `FAIL` line with the command that the
   line names. Then run the preflight again. Every line must show `PASS`.
2. Write the batch order. Put the pull requests in the order that the PM
   gave. If the PM gave no order, put them in the order of their numbers,
   lowest first.

## 2. Build the combined tip

1. Make a local branch from the current main:
   `git switch -c merge-batch-<N> refs/remotes/origin/main`.
2. For each pull request in the batch order:
   1. Fetch its head:
      `git fetch https://github.com/OpenDustpan/OpenDustpan.git pull/<PR>/head`.
   2. Merge it: `git merge --no-ff --no-edit FETCH_HEAD`.
   3. If the merge has a conflict, resolve it. Renumber each API version and
      each migration that clashes with an earlier pull request of the batch
      or with `main`. Only the merge session renumbers.
   4. Run `make generate`. Commit each change that it makes.
   5. Record the sha of this step as `T<i>`, where `<i>` is the place of the
      pull request in the batch order: `git rev-parse HEAD`.
3. Record the sha of the combined tip: `git rev-parse HEAD`. It is the same
   sha as the last `T<i>`.

## 3. Run the gate on the combined tip

1. Check that `DUSTPAN_TEST_POSTGRES_URL` is set. Do not print it.
2. Run `make checks-agent` with `run_in_background: true` and a timeout of
   7200000 ms. Write its output to a file in your scratchpad.
3. The gate passes only when the summary shows `PASS` for `preflight`,
   `generate`, `check`, `e2e` and `trufflehog`, and the summary names the
   sha from step 2.3.

## 4. A red tip

1. Read the failing step. If the failure is a known load flake (a test that
   times out under load, or PostgreSQL that stopped), run
   `make agent-preflight`, then run that one package again alone. If it
   passes alone, run the full gate again one time. Write the flake in the
   report.
2. If the failure is real, drop the last pull request of the batch order.
   Make the combined tip again from step 2, without that pull request. Run
   the gate again.
3. Give each dropped pull request back to the PM with the failing step and
   the last 40 lines of its output.
4. When the batch has no pull request left, stop and report to the PM.

## 5. Merge

1. For each pull request of the green tip, in the batch order:
   1. Fetch main:
      `git fetch https://github.com/OpenDustpan/OpenDustpan.git +refs/heads/main:refs/remotes/origin/main`.
   2. Make a merge commit with the tree of `T<i>`, the head of the pull
      request and main as parents:
      `git commit-tree 'T<i>^{tree}' -p <PR head sha> -p refs/remotes/origin/main -m "Merge main into <branch>"`.
      The command prints the sha of the new commit.
   3. Push that commit to the branch of the pull request:
      `git push https://github.com/OpenDustpan/OpenDustpan.git <new sha>:refs/heads/<branch>`.
      The head of the pull request is a parent of the new commit, so the
      push is not a force-push.
   4. Read the pull request body. Check its closing keywords.
   5. Squash-merge the pull request with the GitHub tool. Give the head sha
      of step 5.1.2, so that the merge fails if the branch moved.
   6. Check that the tree of `main` is now the tree of `T<i>`:
      `git fetch https://github.com/OpenDustpan/OpenDustpan.git +refs/heads/main:refs/remotes/origin/main`, then
      `git diff --stat refs/remotes/origin/main 'T<i>'`. The output must be
      empty. If it is not empty, stop and report the difference to the PM.
2. If main moved during the merges (a merge that is not from this batch),
   stop. Report to the PM. Do not merge the remaining pull requests on a tip
   that the gate did not test.

## 6. Report

Send the PM one message:

- the batch order and the sha of the green tip.
- each merged pull request, with its merge commit.
- each dropped pull request, with the failing step.
- each flake that you ran again.
- the time of the full gate.
