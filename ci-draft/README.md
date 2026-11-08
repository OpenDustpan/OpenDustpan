# Draft workflows for auto-regen

The files in this folder are the new versions of the auto-regen workflows.
They are here because the tool that wrote them cannot push to
`.github/workflows/`. The owner moves them before the merge.

## Move steps

Do these steps on the branch of this pull request, from the root of the
repository:

1. Move the three files. The first command replaces the current file.

   ```sh
   git mv -f ci-draft/auto-regen.yml .github/workflows/auto-regen.yml
   git mv ci-draft/auto-regen-resolve.yml .github/workflows/auto-regen-resolve.yml
   git mv ci-draft/auto-regen-push.yml .github/workflows/auto-regen-push.yml
   ```

2. Remove this folder.

   ```sh
   git rm ci-draft/README.md
   ```

3. Check the workflows. The only finding that you can ignore is the runner
   label `ubuntu-26.04` when your actionlint release does not know it.

   ```sh
   actionlint .github/workflows/auto-regen.yml .github/workflows/auto-regen-resolve.yml .github/workflows/auto-regen-push.yml
   shellcheck scripts/auto-regen.sh
   ```

4. Commit with `git commit -s` and push.

The change to `scripts/auto-regen.sh` is already in place. The current
`.github/workflows/auto-regen.yml` does not call its new subcommands, so the
script works with both versions of the workflow.

## Why three workflows

All the triggers of `auto-regen.yml` (`pull_request_target`, `schedule`, a
push to main and `workflow_dispatch`) run in the Actions cache scope of main.
The resolve step runs the code of the pull request (`make generate`). Code
that runs in the cache scope of main can save a cache entry that a later run
on main restores. So the resolve step moves to a separate workflow:

- `auto-regen.yml` runs on main. It finds the candidates and dispatches
  `auto-regen-resolve.yml` with `workflow_dispatch` on the branch of each
  pull request. A failed dispatch writes one log line and does not stop the
  other dispatches. For example, a branch that was made before this change
  has no copy of `auto-regen-resolve.yml`.
- `auto-regen-resolve.yml` runs on the branch of the pull request, in the
  cache scope of that branch. It has a read-only token, no secret, no saved
  credentials and no cache. It uploads `result.env` and `regen.patch`.
- `auto-regen-push.yml` runs on `workflow_run` in the context of main. It has
  the token of the GitHub App. It runs only git, the script of main and the
  GitHub API.

## The guards of the push workflow

- The job runs only for a successful run with the event `workflow_dispatch`,
  the path `.github/workflows/auto-regen-resolve.yml`, and this repository as
  the repository and as the head repository of the run.
- The SHA of main comes from the checkout of main in the push run.
- `match-run` takes the branch and the head SHA of the run. It checks the
  branch with an allowlist. It refuses `main`, `gh-readonly-queue/*` and `..`.
  It requires a head SHA of 40 hex characters. It requires exactly one open
  pull request that is not a draft, from this repository to main, whose
  current head is the head of the run. If one check fails, the later steps
  do not run.
- From the result of the resolve run, the push reads only the `status=` line.
  It reads the `main=` line only to compare it with the SHA of main. If the
  two are not equal, the run stops, and the next run uses the new main.
- `check-patch` from main accepts only generated paths. It refuses renames,
  symbolic links and submodules. `apply` checks the patch again and checks
  that the branch did not move.
- The push is a plain push. It refuses `main` and `gh-readonly-queue/*`.

## Trust assumptions

- The resolve run uses the workflow file of the branch. A person who can
  push a branch to this repository controls that file. This person can
  already run a workflow on the branch, so the change gives no new power.
  The result of that run can change only the pull request of that branch,
  and only through a patch of generated paths. The commit of the GitHub App
  then goes through CI and review, as every other commit does.
- Code scanning can show an alert at the checkout of the pull request branch
  with the token of the GitHub App. No pull request code runs after that
  checkout: only git, the script of main and the GitHub API.
- The owner makes the GitHub App and the secrets `REGEN_APP_CLIENT_ID` and
  `REGEN_APP_PRIVATE_KEY`.
