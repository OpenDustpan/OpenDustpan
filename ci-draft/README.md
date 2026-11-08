# CI workflow draft

A maintainer reviews this draft and moves the files into `.github/workflows/`
by hand. The draft changes no file under `.github/`.

## Move

| Draft file | Target | Action |
|---|---|---|
| `ci-draft/ci.yml` | `.github/workflows/ci.yml` | Replaces the file. |
| `ci-draft/e2e-nightly.yml` | `.github/workflows/e2e-nightly.yml` | Replaces the file. |
| `ci-draft/ci-cache.yml` | `.github/workflows/ci-cache.yml` | New file. |

1. Run actionlint once on the three files. The draft was not checked with
   actionlint.
2. Move the files as the table shows.
3. Delete `ci-draft/`.

## Changes

### `ci-draft/ci.yml`

- Item 1: The scan job restores the Trivy and TruffleHog archives from the
  Actions cache. A missing or corrupt archive is downloaded with five attempts
  (waits of 5, 10, 20 and 40 seconds) and checked against its pinned SHA-256.
  The save step writes the archives only after the earlier steps succeed, on a
  cache miss, and on a push to main or the schedule.
- Item 1: The e2e, e2e-journal, upgrade-test, crash-test and faults jobs no
  longer look up the newest CLI versions or cache their archives. The pinned
  cache holds the whole CLI cache directory. The test steps set
  `E2E_PULUMI_VERSIONS: pinned` and `E2E_TOFU_VERSIONS: pinned`; the journal
  test keeps its own version.
- Item 3: A push to main cancels the older push run. Pull requests keep their
  group and cancel their older runs. Merge groups and scheduled runs stay
  uncanceled.

### `ci-draft/e2e-nightly.yml`

- Item 2: The newest-version lookup runs only in the `latest` row. The pinned
  row restores the pinned cache without exclusion lines. The latest row keeps
  its two cache steps.
- Item 4: New `upgrade-latest` job on PostgreSQL. It runs `make upgrade-test`
  with the newest CLI releases, after the newest-version lookup and the latest
  CLI cache. A red run does not block a release.

### `ci-draft/ci-cache.yml`

- Item 3: New workflow that saves the scan tool archives cache entry. A
  workflow-level cancel of `ci.yml` cancels every job of the run, so the save
  lives in its own workflow with `cancel-in-progress: false`. The save step
  runs only on a cache miss and only after the download step succeeds. The job
  has no checkout and no Go setup, because it uses no repository file and no
  Go.

## Not changed

- Item 5 (OPTIONAL): Merge-group concurrency per pull request. The group needs
  the pull request number from `github.event.merge_group.head_ref`
  (`refs/heads/gh-readonly-queue/main/pr-<number>-<sha>`). A workflow
  expression cannot cut a string, so this needs a first job that computes the
  group and job-level concurrency on each job. This is a choice for the
  maintainer.
- The pinned CLI archives in `ci-cache.yml`: left out because the repository
  has no command that downloads them without a test run. The e2e jobs of
  `ci.yml` still save that entry through `actions/cache`.
- The merge-group group in `ci.yml`: unchanged, as item 5 says.
- The scan job on the schedule: unchanged; it still does not run on the
  schedule, and the save condition uses the same rule as the Go cache save.
- The job ids, job names and check names: unchanged, because they are required
  checks.
- The action pins: unchanged, and no action or third-party download is added.
- The Makefile: no new target, because the cache fill workflow does not need
  one.
- sqlc (`sqlc-dev/setup-sqlc`), golangci-lint (`golangci/golangci-lint-action`)
  and semgrep (`pipx install`) still download their tools without a pinned
  SHA-256 in this repository and without the retry loop. A pinned sum needs a
  value from each upstream release. This is a choice for the maintainer.
- A push run on main that a newer push cancels saves no Go cache. The next
  push run that finishes saves it.

## Open items

These items need a code change outside this draft.

- A cache hit of the pinned CLI cache runs the extracted binary without a sum
  check. `e2e.Fetcher.Install` returns the cached binary when `os.Stat` finds
  it, and it checks the sum only on a download. The cache holds the extracted
  directories, and this draft makes the cache the only CLI source of five
  jobs. A fix keeps the archives in the cache and checks them on each
  install, or checks the binary against a recorded sum.
- The e2e-journal job is not pinned. `make e2e-journal` sets
  `E2E_PULUMI_VERSIONS=3.268.0`, and `e2e/tool.go` has no pinned sum for that
  version. So each run on a pull request, a merge group and a push gets the
  published checksum file from get.pulumi.com, the host that also serves the
  archive. A fix pins the sums of the 3.268.0 archives in `e2e/tool.go`, or
  moves the journal test to a pinned version.
