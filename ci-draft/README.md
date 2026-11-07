# CI load cut: steps for the owner

This directory holds a draft. The owner moves the workflow file into
`.github/workflows/` and removes this directory in one commit on this branch.
After that commit, the pull request adds no `ci-draft/` directory to main.

The draft is `ci-draft/workflows/ci.yml`, the complete new
`.github/workflows/ci.yml`. No other workflow file changes.
`cancel-closed-pr.yml` keeps the concurrency group of a pull request, so it
needs no change.

## 1. Commands

### 1a. Before the move: merge the four check pull requests

The lint job of the draft runs four make targets that are not on main yet.
Each one comes from its own pull request:

| Step name in the lint job | Make target | Pull request |
|---|---|---|
| No added suppression marker | `nolint-check` | #2725 |
| The API description pin does not change before the first tag | `pin-frozen` | #2726 |
| No large or binary file outside the allowlist | `files-check` | #2727 |
| The pull request body names an issue | `pr-body-check` | #2729 |

Merge these four pull requests first. If the draft lands before one of them,
the lint job fails on every pull request with "No rule to make target".
If one of them stays open, delete its step from `ci-draft/workflows/ci.yml`
before the move, and add the step again after that pull request merges.
If all four stay open, also delete the step "Fetch main for the pull request
checks".

#2725 also edits `.github/workflows/ci.yml`. The draft holds the same step,
so the move replaces the file and keeps the step.

### 1b. The move

On this branch, after the four pull requests merge:

```sh
git switch ci-load-cut
git pull --ff-only
git merge origin/main        # or "Update branch" in the pull request
git mv -f ci-draft/workflows/ci.yml .github/workflows/ci.yml
# Run the ruleset step (section 2) now if you use the file, before the next line removes it.
git rm -r ci-draft
git commit -s -m "Apply the CI load cut"
git push
```

Update the branch to the newest main before the move. The branch starts at
main `0386a53`. If a later merge changes `.github/workflows/ci.yml`, compare
it with the draft and carry the change into the draft before the move.

Create the label `full-ci` (Issues > Labels > New label) if it does not exist.

## 2. Ruleset (main, id 26715149)

`ci-draft/ruleset-main.json` is the full body for
`PUT /repos/OpenDustpan/OpenDustpan/rulesets/26715149`. I read the live
ruleset on 2026-11-07 and changed one value:

- `merge_queue.max_entries_to_build`: 3 -> 5. `max_entries_to_merge` stays 5,
  `grouping_strategy` stays `ALLGREEN`, the check timeout stays 90 minutes.

The required check names do not change. The draft keeps the 16 names, and
the two store jobs keep the names `store (sqlite)` and `store (postgres)`.

Command (from the repository root, before `git rm -r ci-draft`):

```sh
gh api repos/OpenDustpan/OpenDustpan/rulesets/26715149 > /tmp/ruleset-live.json
# Compare: the only difference is max_entries_to_build.
gh api -X PUT repos/OpenDustpan/OpenDustpan/rulesets/26715149 --input ci-draft/ruleset-main.json
```

Order: apply the ruleset change after the workflow change merges, not
before. Each merge group runs 17 jobs. With 5 groups that build at the same
time, the queue can hold 85 jobs, which is more than the limit of 60
concurrent jobs of the organization. Before the workflow change, pull request
runs use most of the 60 jobs, so 5 groups would wait for runners and could
reach the 90-minute check timeout. After the workflow change, a pull request
run uses 3 to 7 jobs. If the queue still waits for runners, set the value
back to 3 or 4.

When a group fails, the merge queue removes the failing pull request and
builds the groups after it again without it. The GitHub docs ("Managing a
merge queue", the scenario of a failing status) say: "the merge queue
automatically removes pull request #1 from the merge queue" and "recreates
the temporary branch ... to only contain changes from the target branch and
pull request #2". With 5 groups that build, one failure can start up to 4
new builds.

Optional, later: add `changes` as a required check (integration 15368).
Each job already runs when the `changes` job fails, so this is a second
layer. Add it about one day after the workflow change merges. If you add it
earlier, open pull requests wait for a `changes` check that their old runs do
not have.

What the change relies on:

- GitHub docs, "Using conditions to control job execution": "A job that is
  skipped will report its status as "Success". It will not prevent a pull
  request from merging, even if it is a required check."
- The workflow always starts (there is no workflow-level `paths:` filter), so
  no required check waits for a workflow that did not start.
- A matrix job that its `if:` skips reports one check without the matrix
  values. The draft has two store jobs and no matrix for this reason.
- In `merge_group`, `push` and `schedule` runs, the `changes` job sets every
  output to true, so the merge queue runs every required job.

## 3. What changes, estimates, risks, rollback

### Which jobs run

| Event | Jobs |
|---|---|
| merge_group, push to main | every job, as before |
| schedule | lint, go, store (sqlite), store (postgres), as before |
| pull_request with the label `full-ci` | every job |
| pull_request, docs only | changes, lint, scan |
| other pull_request | changes, lint, golangci-lint, golangci-lint-journal, nilaway, go, scan, and each suite whose own files changed |

Docs only means that each changed path is under `docs/`, under `.repowise/`,
or a Markdown file at the repository root. Three docs files count as code,
because Go tests read them: `docs/tfe-api.md`,
`docs/reference/configuration.md`, `docs/reference/helm-values.md`. Markdown
files in other directories count as code (some are test data).

A change to `.github/`, `Makefile`, `go.mod` or `go.sum` runs every job.

Own files of each suite:

| Output | Jobs | Paths |
|---|---|---|
| e2e | e2e, e2e-journal, upgrade-test, crash-test, faults | `e2e/`, `internal/api/pulumi/`, `internal/api/tfe/`, `internal/pulumijournal/`, `pkg/dustpan/`, `cmd/opendustpan/`, `pkg/store/*/migrations/`, `scripts/e2e-*`, `scripts/toxiproxy.sh` |
| store | store (sqlite), store (postgres) | `pkg/store/`, `internal/store/`, `internal/pgtest/`, `pkg/*test/` (the conformance suites) |
| bench | bench | `pkg/stack/`, `pkg/store/sqlite/` |
| journal | journal-test | `internal/api/pulumi/`, `internal/pulumijournal/`, `pkg/dustpan/`, `pkg/stack/` |
| chart | chart-lint | `deploy/helm/`, `scripts/chart-lint.sh` |

The label `full-ci` is read from the event. It applies from the next push to
the pull request. A re-run of a job may read the labels of the old event.

Superseded pull request runs are canceled. This was already on main
(`concurrency` with `cancel-in-progress` for `pull_request` only). Runs on
main and in the merge queue are never canceled. The draft keeps it.

Other changes in the draft:

- Each job has `timeout-minutes` (lint, golangci-lint, golangci-lint-journal
  and nilaway 30, go 60, bench 50, store 45, scan 20, chart-lint 15,
  changes 5). Before, most jobs had the default of 360 minutes.
- The comment of `DUSTPAN_TEST_SKIP_FINAL_DROP` says that the variable also
  keeps the databases of a failed run, and that it is only for a disposable
  server that is isolated to one run.
- The four pull request checks of section 1a run in the lint job, on
  `pull_request` only, so they also run for a docs-only pull request.

### Estimates

Source: the job start and end times of one recent run for each case. Each
job is rounded up to a whole minute. The repository is public, so the API
reports no billable minutes; the limit that matters is 60 concurrent jobs.

- Go pull request, run 42405941732: 16 jobs, 78 minutes.
- Docs pull request, run 42405854006: 16 jobs, 70 minutes.
- Merge group, run 42408769686: 16 jobs, 82 minutes.

| Case | Minutes before | Minutes after | Jobs per run |
|---|---|---|---|
| Docs-only pull request, 1 push | 70 | 5 | 16 -> 3 |
| Go pull request, 3 pushes, no suite files | 234 | 75 | 16 -> 7 |
| Go pull request, 3 pushes, touches `pkg/store` | 234 | about 150 | 16 -> 10 |
| Go pull request, 3 pushes, touches an API adapter | 234 | about 138 | 16 -> 12 |
| Merge queue entry | 82 | 83 | 16 -> 17 |

The four pull request checks add about 1 minute to the lint job. That
minute is not in the table.

Of the last 50 runs, 33 were pull_request, 11 merge_group and 6 push. If
about 75% of pull request pushes touch no suite files, the pull request
minutes drop by about 60 to 65%. That is roughly 15,000 to 18,000 of the
44,000 runner minutes of a day. This is an estimate from one run for each
case, not a measurement.

Not in the draft (owner decision):

- Skip the suites on push to main. With squash merges, the push has the tree
  that the merge group tested. Keep lint, golangci-lint,
  golangci-lint-journal, nilaway, go and store on push, because they save
  the caches. Saving: about 25 minutes for each merge.
- Run the schedule every 6 hours, not every 2 hours. Saving: about 400 to
  600 minutes a day. Flaky tests are found 3 times slower.
- Merge e2e-journal, upgrade-test, crash-test and faults into one job.
  Saving: about 4 minutes and 3 job slots for each full run. The required
  check names change.
- Run golangci-lint-journal in the golangci-lint job. Saving: about 1 to 2
  minutes and 1 job slot. The job takes about 3.5 minutes longer.

### Risks

1. More failures show first in the merge queue. A pull request can break e2e
   or a store suite without a change to their paths. For example, a change
   in `pkg/access` can break the store conformance suites. The queue removes
   that pull request and builds the groups after it again. If this happens
   often, widen the store paths to `pkg/` or ask authors to use `full-ci`.
2. The path lists can get old when new directories appear. The merge queue
   still runs every job.
3. A new test that reads a docs file is not in the list of three docs files
   until someone adds it. The merge queue catches it.
4. The other jobs start about 15 to 30 seconds later, because they wait for
   the `changes` job.
5. The cache keys of the store jobs change. Until the first push to main
   saves new entries, the store jobs of pull requests build without a cache,
   about 1 to 3 minutes slower.
6. If the `changes` job fails, every job runs. A canceled run still skips
   them.
7. For a docs-only pull request, the skipped jobs show "skipped". The merge
   queue runs them.
8. `max_entries_to_build` 5 can use more than 60 concurrent jobs. See
   section 2.
9. The pull request body check reads the body from the event. After an edit
   of the body, the check sees the new body at the next push.

### Rollback

- Workflow: revert the commit of the move (`git revert`). The check names did
  not change, so the ruleset needs no change for the revert.
- Ruleset: set `max_entries_to_build` back to 3 (Settings > Rules > Rulesets
  > main > Merge queue > Build concurrency).
- If you added `changes` as a required check, remove it before you revert
  the workflow. If you do not, pull requests wait for a check that no
  workflow sends.
