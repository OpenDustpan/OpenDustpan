---
rfc: 113
title: Rules that hold by themselves, with code gates, nested agent files, project skills and a size budget
status: discussing
created: 2026-10-30
---

# RFC 113. Rules that hold by themselves, with code gates, nested agent files, project skills and a size budget

Status: discussing. Design only. The owner approved the goal on 2026-10-30.
This RFC holds the options, a recommendation for each question, and the plan
of the follow-up pull requests. It changes no code.

## Problem

Many agents work on this repository at the same time. They make the same
mistakes again, because a rule that only text holds does not hold. The root
`CLAUDE.md` has 365 lines. An agent reads it once, at the start, and then
works in one package for hours.

The goal, in this order of preference:

1. Code gates: Make targets, lint rules and tests that fail.
2. Short nested agent files (`AGENTS.md`, which `CLAUDE.md` imports) in
   the packages that have their own conventions.
3. A small number of project skills for workflows that span several files.

Also bring the size of files and packages under control.

### The repeated failures of the last 48 hours

Each line gives the failure, what exists on `main` today, and why that did
not stop it.

| # | Failure | What exists on `main` (`a7dc146`) | Why it did not stop it |
| --- | --- | --- | --- |
| F1 | Agents ran golangci-lint v2.14.0. CI runs v2.15.0. An `errcheck` finding in `load/main.go` reached `main` (fix #2113). | The Makefile names v2.15.0 in a comment only. CI pins `version: v2.15.0` in two steps of `ci.yml`. | `make lint` runs whatever `golangci-lint` is on the `PATH`. Nothing compares the versions. |
| F2 | `make e2e` and `internal/servertest` ran on SQLite only when `DUSTPAN_TEST_POSTGRES_URL` was not set. Three merges had SQLite-only e2e. | 61 `t.Skip` calls in 47 files say "set DUSTPAN_TEST_POSTGRES_URL". The e2e summary writes a "not run" report for each skipped store. | A skip is a pass for `go test`. The notice is one line among thousands of lines of output. |
| F3 | Open pull requests clash on `DocumentVersion` and on migration numbers. The merge session must renumber. | `TestVersionFollowsTheDescription` pins the version and the SHA-256 of `openapi.json` in `openapi.pin`. `TestFrozenMigrations` pins each migration file. | Each branch passes alone. The clash shows only on the merged head, and the fix is manual. |
| F4 | A `git push` together with other commands in one shell line was refused by the permission classifier. | `.claude/settings.json` allows exactly `git push -u https://github.com/OpenDustpan/OpenDustpan.git HEAD:*`. | A compound command does not match the allow rule. No text near the work says so. |
| F5 | Credential-shaped literals in tests reached history twice. Such a literal is a URL with a user name and a password before the host. Each one needs an owner-approved TruffleHog exclusion for good. | `make trufflehog` scans all history. `make semgrep` runs `p/secrets`. `.trufflehog-exclude-paths.txt` has 14 lines. Draft #2117 adds two more. | `p/secrets` does not match a URL with a user and a password. TruffleHog runs after the commit, when the literal is already in history. 21 such literals are in Go source today, in 19 files. |
| F6 | `TestRoundTripsOfBackup` has fixed ceilings that each new backup table breaks. | Issue #2115 has a session of its own. | This RFC does not redo it. It only references it. |
| F7 | A store port change merged without the `internal/store` and `storefault` gates. `storefault.Store` did not forward `CountBlobPurges`, and `TestTransparency` failed on `main` (fix #2103). | `var _ Inner = (*Store)(nil)` checks the methods of `Inner` at compile time. `TestTransparency` runs the store suite through the wrapper. | The new method was on a port that `Inner` did not embed, so the compiler saw nothing. `TestTransparency` ran only after the merge. |
| F8 | An `openapi.json` change merged without a version change, and `main` went red (fix #2110). | The pin test of F3. | The test ran on the branch, not on the merged head. |
| F9 | Benchmark models drifted from the code that they model. | `cmd/s3bench/measure.go` has functions such as `hotPath`, `unitImport` and `listThenPut`. Their comments say "models the checkpoint ... writes", in prose. | No model names the code that it follows, so no test can find the drift. |
| F10 | Containers restart and PostgreSQL stops. Gate scripts assume that it runs. | `scripts/checks-agent.sh` starts a container when the URL is not set. The lead workflow tells each worker to run `service postgresql start`. | When the URL is set but the server is down, each suite fails late, with a connection error. |
| F11 | The checklist of a new endpoint is long and easy to miss. | Gates exist for most items: the route test, the error code gate, the pin test, `make clients-check`, and the route coverage check of `make e2e`. | The steps and their gates are in five places. No one place lists them in order. |
| F12 | Each new backup table needs the table list, the staged-row checks, both dialects and the restore round trip. | `pkg/backup/coverage.go` fails an export when a column list and the schema differ. | The steps are in several packages. An agent finds each gate only when it fails. |

Of the last 50 commits on `main`, 4 are `fix-main` commits: #2058, #2103,
#2110 and #2113. F1, F7 and F8 caused three of them. The lead workflow
records that pull requests that each passed alone broke `main` together about
10 times from 2026-10-11 to 2026-10-17.

### Size facts

Measured on `main` at `a7dc146` with `git ls-files '*.go'`. A file is
generated when its first 2,000 bytes hold "Code generated" and "DO NOT EDIT".

- 3,034 Go files with 923,634 lines. 424,171 lines (46%) are in `_test.go`
  files.
- 8 generated files with 40,261 lines: the sqlc code in
  `pkg/store/*/internal/db`.
- Hand-written files over a limit:

  | Limit | Files over it | Of them `_test.go` | Lines over the limit, in total |
  | --- | --- | --- | --- |
  | 800 | 194 | 87 | 111,221 |
  | 1,000 | 120 | 55 | 80,685 |
  | 1,500 | 38 | 22 | 43,872 |
  | 2,000 | 17 | 10 | 29,401 |
  | 3,000 | 8 | 4 | 18,110 |

- The largest hand-written files:

  | File | Lines |
  | --- | --- |
  | `e2e/e2e_test.go` | 15,207 |
  | `pkg/workspace/workspace_test.go` | 5,410 |
  | `pkg/store/storetest/workspaces.go` | 4,337 |
  | `pkg/dustpan/dustpan.go` | 4,226 |
  | `internal/api/pulumi/updates_test.go` | 3,455 |
  | `pkg/store/storetest/updates.go` | 3,274 |
  | `e2e/coverage.go` | 3,181 |
  | `pkg/store/storetest/backup_test.go` | 3,020 |
  | `pkg/workspace/service.go` | 2,906 |
  | `e2e/harness.go` | 2,758 |

- The widest packages, in Go files: `pkg/store/postgres` 187,
  `pkg/store/sqlite` 182, `pkg/dustpan` 146, `e2e` 133,
  `internal/api/server` 102. 11 packages have more than 60 files.

Repowise on the indexed commit `50282f0`:

- `get_health`: average health 7.59, hotspot health 6.31. The worst
  production file is `pkg/store/storetest/workspaces.go` (score 1). The worst
  test file is `e2e/e2e_test.go` (score 1). 1,793 open production findings and
  2,617 test findings.
- `get_risk`: `pkg/dustpan/dustpan.go` had 151 commits in 90 days, in the
  99.9th churn percentile. Its strongest co-change partners are
  `e2e/coverage.go` (80 shared commits) and `cmd/opendustpan/main.go` (52).
  `e2e/e2e_test.go` had 121 commits in 90 days, in the 99.9th percentile.
  `internal/api/apidesc/document.go` had 26 commits, and `openapi.pin`
  changes with it in 100% of its commits. `storefault.go` had 29 commits, and
  it changes with the `queries.sql` of each dialect.
- `get_dead_code`: 64 findings, 1,262 lines, all in the medium tier
  (confidence 0.4), 0 marked safe to remove. 20 of them (772 lines) are in
  `internal/clientgen`. Most of these are TypeScript runtime files that the
  generator copies into the client, so they are false positives.

`pkg/dustpan/dustpan.go` has 166 functions. `mountAPI` alone has 105 lines.
`e2e/e2e_test.go` has 466 functions and 12 `Test` functions. Its scenarios are
already named functions, for example `pulumiScenario` (233 lines) and
`tofuLockScenario` (196 lines).

## Questions and options

Each question has options, a recommendation and the reason. Section
"Plan of the follow-up pull requests" turns the recommendations into pull
requests.

### Q1. A code gate for each mechanical failure

#### Q1a. The golangci-lint version (F1)

| Option | Cost | Gives |
| --- | --- | --- |
| Do nothing | None | F1 comes back with each new version. |
| `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0` in `make lint` | A slow first build, and the golangci-lint project advises against a source build. | The exact version on each machine. |
| `make lint` reads `golangci-lint version`, compares it with `GOLANGCI_LINT_VERSION` in the Makefile, and fails when they differ | About 10 lines of Make. | A fail with the one command that helps. |

**Recommendation:** the version check. Put each tool version in one
Makefile variable (`GOLANGCI_LINT_VERSION`, `SQLC_VERSION`,
`SEMGREP_VERSION`, `TRIVY_VERSION`, `TRUFFLEHOG_VERSION`). Add a small
standard-library Python check, run by `make lint`, that fails when a version
in `.github/workflows/*.yml` differs from the Makefile variable. **Reason:**
it fails closed, it costs no download, and it also stops the drift of the
other four pinned tools. The error text names the install page and the
version. The preflight script of the steward pull request,
`scripts/agent-preflight.sh`, also checks the lint version when a session
starts. Its function `pinned_lint_version` reads the pin from the comment line
of the Makefile (`#   golangci-lint v2.15.0`). The self-test of the preflight
runs in `make lint`, and it fails closed when it cannot read the pin. So the
pull request that moves the pin into `GOLANGCI_LINT_VERSION` also changes
`pinned_lint_version` to read that variable. After that, the two checks read
one variable. The check in `make lint` stays, because it runs where the lint
runs, also for a person who never runs the preflight script.

#### Q1b. PostgreSQL is required, and it must run (F2, F10)

| Option | Cost | Gives |
| --- | --- | --- |
| Do nothing | None | SQLite-only runs continue to pass in silence. |
| A loud notice at the end of `make e2e` | Small. | Better, but an agent that reads only the exit code still misses it. |
| `make test`, `make e2e`, `make e2e-ha` and `make merge-gates` fail without `DUSTPAN_TEST_POSTGRES_URL`, unless `DUSTPAN_TEST_SQLITE_ONLY=1` is set | Small. A person who wants a fast local loop sets one variable. | The skip becomes a choice that is visible in the command line. |
| The Go tests fail instead of skip | Large. `go test ./...` with no database stops working for every contributor. | Too strict for a first checkout. |

**Recommendation:** the third option, plus a readiness step. The readiness
step is not new work of this RFC. The ways-of-working steward pull request
(see "Work that this RFC builds on") adds `scripts/agent-preflight.sh`. That
script checks that PostgreSQL is up, the lint version, the merge base and the
free disk space. It starts PostgreSQL with `pg_ctlcluster` when the server is
down (`PG_START=1` is the default, and `PG_START=0` only checks), and it waits
up to 30 seconds. It cannot run only its PostgreSQL check yet. So this RFC
adds two small things: a check name argument to the preflight script, for
example `agent-preflight.sh postgres`, and a Make target `pg-ready` that calls
it. The targets in the list above depend on `pg-ready`. The e2e summary also
ends with one line that counts the stores that did not run. **Reason:** the
Make targets are the entry points of agents and of CI. A plain `go test ./...`
stays usable on a laptop. The readiness check fails within 30 seconds with a
clear cause, not after 20 minutes with a connection error. One preflight
script holds the check and the start of PostgreSQL, so they do not drift
apart.

#### Q1c. Version and migration number clashes between open pull requests (F3, F8)

| Option | Cost | Gives |
| --- | --- | --- |
| Do nothing | The merge session renumbers by hand. | Red `main` when it forgets (F8). |
| A version derived from the bytes of the description | It changes the version scheme of the API description, which an ADR decided. | No clash, but no meaning in the number. |
| Timestamps for migration numbers | It changes the frozen and collapse rules of the migrations. | No clash, but the order of files no longer reads as a sequence. |
| Keep the numbers. Run the gates on the merged head (Q1f). Add one command for each mechanical fix. | Two small Make targets. | The clash fails before the merge, and the fix is one command. |

**Recommendation:** the last option. `make api-version-bump` raises the minor
part of `DocumentVersion` and rewrites `openapi.pin` with the update flag of
the pin test. `make migration-renumber FROM=<n> TO=<m>` renames the file
pair of both dialects and updates the pinned hashes of the frozen-migration
test. A test fails when one dialect has two files with one number. The merge
session runs the two targets. An author does not, because after CLEAN only
the merge session renumbers. **Reason:**
the existing pins already detect the clash. The cost was that the detection
ran on the wrong head and that the fix was manual. Both problems have a
small mechanical fix, and no ADR changes.

#### Q1d. The `git push` command (F4)

A shell command of an agent cannot be gated by `make`. Two layers are
possible.

| Option | Cost | Gives |
| --- | --- | --- |
| Text in the root agent file and in `docs/agents/merge-session.md` | Small. | It helps only when the agent reads it. |
| A `PreToolUse` hook in `.claude/settings.json` that refuses a Bash command that holds `git push` and also `&&`, `;`, `\|` or a line break | A short standard-library Python script. The file already has one `PreToolUse` hook. | A refusal with the exact allowed form, before the classifier sees the command. |

**Recommendation:** both. The hook message gives the allowed form:
`git push -u https://github.com/OpenDustpan/OpenDustpan.git HEAD:<branch>`,
alone. **Reason:** the hook is a code gate for agents that use Claude Code.
The text covers the other tools. Changes to `.claude/` are process changes,
so the owner merges that pull request.

#### Q1e. Credential-shaped literals (F5)

| Option | Cost | Gives |
| --- | --- | --- |
| Do nothing | Each new literal needs an owner-approved exclusion for good. | The exclusion list only grows. |
| A local Semgrep rule file, `.semgrep/credential-url.yml`, added to `make semgrep` with `--config .semgrep/` | No new tool. One rule with a regular expression for a string literal that holds a URL with a user name and a password before the host. | `make check` fails before the commit is pushed. |
| A Git pre-commit hook | Each clone must install it. Agents in fresh containers do not. | Earlier, but not reliable. |

**Recommendation:** the Semgrep rule, with a known list of today's 21
literals in the `paths.exclude` of the rule, and a test helper
`credurl.Fake(scheme, host)` that builds such a URL at run time with
`url.UserPassword`. A follow-up moves each literal to the helper and removes
its path from the known list. The list only shrinks. **Reason:** Semgrep is
already a gate of `make check`. A URL built at run time does not match the
TruffleHog detectors, so a test can still send a hostile URL. The rule stops a
new literal before it reaches history. Draft #2117 handles two old commits.
This rule stops the next ones.

#### Q1f. One target that runs exactly what a merge needs (F3, F7, F8)

`scripts/checks-agent.sh` (`make checks-agent`) runs `make generate`,
`make check`, `make e2e` and a TruffleHog scan of the branch commits. The
steward pull request changes this script. It starts the native PostgreSQL
with `pg_ctlcluster`, it has no rebase option and no credential literal, and
its default is `SYNC=none`: it runs on the head as it is. It fails when
`make generate` leaves a diff and when a tracked file has an uncommitted
change. Its summary names the head SHA. From the next
merge session on, the merge session also gates once on the combined head of
each batch, with a full `make check` and `make e2e` and no package subsets
(`docs/agents/merge-session.md` of that pull request).

| Option | Cost | Gives |
| --- | --- | --- |
| A new `make merge-gates` script | A second script that drifts from the first. | A clean start. |
| `make merge-gates` as a fixed mode of `checks-agent.sh` | A few lines in one script. | One script. The merge mode only adds the refusal of `SKIP`. |
| A Git pre-push hook | Not installed in fresh containers. | Not reliable. |

**Recommendation:** the second option, built on the script as the steward
pull request leaves it. That pull request is not redesigned here. The script
already runs on the head as it is, fails on a diff of `make generate` and on
uncommitted changes, and names the head SHA. This RFC adds only the Make
target and a merge mode that refuses `SKIP`. The agent pastes the summary of
the script into the pull request, as `CONTRIBUTING.md` asks.
`docs/agents/merge-session.md` decides whether the merge session also uses
this target on the combined head of a batch.
**Reason:** F3, F7 and F8 all passed on the branch and failed on the merged
head. One target, with no way to skip a step, closes that gap. It uses the
script that already exists.

Research supports the gate on the combined head. A study of pull requests by
agents found a textual conflict in 19.8% of the pairs of pull requests by one
agent, and in 41.7% of the pairs by two different agents (arXiv 2607.04697,
grade A). Uber's SubmitQueue answers the same problem with a queue that tests
each change on the head that it will merge into. The GitHub merge queue does
this too. It is a later option, for when the CI machines have the capacity to
test each queued head (open question 9).

#### Q1g. The forward completeness of `storefault` (F7)

| Option | Cost | Gives |
| --- | --- | --- |
| Rely on `TestTransparency` | None. | It fails only when the new method has a case in the suite and the test runs. |
| A test that compares method sets with `reflect` | One test file. | A fast fail for each exported method of `*sqlite.Store` or `*postgres.Store` that `*storefault.Store` does not have. A short known list holds methods that the wrapper must not forward, each with a reason. |

**Recommendation:** the `reflect` test in `internal/store/storefault`, and the
same test in `internal/blob/blobfault` for the blob adapters. **Reason:** the
fault in #2103 was a method on a port that `Inner` did not embed. Only a check
of the method sets of the real adapters finds that, and it runs in less than
a second.

#### Q1h. Benchmark models name the code that they follow (F9)

| Option | Cost | Gives |
| --- | --- | --- |
| Review only | None. | The drift continues. |
| A `Follows:` line in the comment of each model names a Go package path and a symbol. A test parses that package with `go/parser`. It fails when the symbol is not there. | One test file, and one line for each model. | A model can no longer point at a symbol that was renamed or removed. |

**Recommendation:** the `Follows:` line and its test, in `cmd/s3bench` and in
`load`. The nested agent file of each of these directories says: a change to
a followed symbol checks the model in the same pull request. `get_risk` with
`changed_files` shows the co-change. **Reason:** a test cannot prove that a
model is still true. It can prove that the model names live code, and that
gives a reviewer a place to look. A path and a symbol are not internal project
references, so this follows ADR 66.

#### Q1i. The endpoint checklist and the backup table checklist (F11, F12)

Most steps already have a gate. What is missing is one ordered list of the
steps, with the gate of each step.

**Recommendation:** no new gate in this RFC. The skills of Q5 hold each list.
Each step names the command that fails when the step is missing. The skill
pull request first checks each gate on `main` and lists each step that has no
gate as an open item. For backup tables, the ceilings of
`TestRoundTripsOfBackup` stay with issue #2115. **Reason:** the gates exist.
The cost was finding them, and a skill fixes that.

#### Q1j. Verifiers before a pull request opens

Spotify runs verifiers in a Claude Code `Stop` hook: when the agent stops,
the hook runs fast checks, and the agent continues when one fails (grade B).

| Option | Cost | Gives |
| --- | --- | --- |
| Do nothing | None. | The gates run when the agent remembers them, or in CI. |
| A `Stop` hook that runs `make merge-gates` | It takes more than 20 minutes. Each stop of each session pays it. | Too slow for a hook. |
| A `Stop` hook that runs only the fast gates on the changed files. These are `gofmt`, the size budget, the credential URL rule, the agent file check, and `go vet` of the changed packages. | One short script. It must finish in about 60 seconds. | The agent sees a mechanical failure before it opens a pull request, not after CI. |

**Recommendation:** the fast `Stop` hook, as a later pull request (owner),
after the gates that it calls exist. `make merge-gates` stays the full gate.
**Reason:** the cheap gates catch the most frequent failures of F1 and F5 in
seconds. A hook runs without the agent having to remember it. Like Q1d, it
covers Claude Code only.

### Q2. A size budget for hand-written files

| Option | Cost | Gives |
| --- | --- | --- |
| Do nothing | The largest files grow by each feature. | More merge conflicts and harder reads. |
| The `file-length-limit` rule of revive, with path exclusions for today's large files | Configuration only. | An exclusion cannot hold a ceiling for each file, so an excluded file can grow without a limit. |
| A standard-library Python script with a known list | About 100 lines, and a list file. | A ceiling for each listed file, and a list that only shrinks. |
| A Go test | Same as the script. | It needs a package with no other purpose. |

**Recommendation:**

- A limit of **1,500 lines** for each hand-written `.go` file, test files
  included. 38 files are over it today. Generated files are exempt by their
  header. `testdata/` is exempt.
- `scripts/size-budget.py`, run by `make lint`. The known list
  `scripts/size-budget.txt` holds one line for each file over the limit:
  the path and its ceiling, the current line count. A first line
  `limit <n>` holds the number of entries, as in the known lists of the error
  catalog.
- The script fails for a file over 1,500 lines that is not on the list, for a
  listed file over its ceiling, for a listed file that is now at or below
  1,500 lines, and for a list whose length is not its `limit`. So a new entry
  is a higher `limit` in the diff, which a review sees.
- The ceiling of a listed file does not go down by itself. A pull request
  that splits a file lowers or removes its line. **Reason:** a strict
  ceiling, equal to the current count, would make each small edit of a hot
  file change the list too. That is a new conflict point.
- A package width signal: the script prints, but does not fail on, each
  package with more than 60 non-test files. The two store adapters are wide
  by design, with one file for each port area. A gate on width adds no value
  now.

**Reason for 1,500:** at 1,500 the known list has 38 lines and is a real
work list. At 1,000 it has 120 lines and is noise. The limit can go down
later, when the list is short.

### Q3. Split plans for `e2e/e2e_test.go` and `pkg/dustpan/dustpan.go`

These two files conflict with most open pull requests. Of the last 50
commits, 6 changed `e2e_test.go` and 9 changed `dustpan.go`.

#### The method for both

- A deterministic split script, `scripts/split-go-file.go`, moves top-level
  declarations by name from one file to new files of the same package. It
  uses `go/ast` and `go/format` only. It takes a plan file with lines of the
  form `<new file> <declaration name>`. A declaration that the plan does not
  name stays in the old file.
- The script checks that the set of declarations of the package is the same
  before and after, and that each moved declaration has the same bytes.
- The pull request holds the plan file, the script and the result, in one
  commit with no other change. No behavior change. A reviewer runs the script
  on the base SHA and compares.
- An open pull request takes in the split in two steps. First the same
  script runs with the same plan on its branch, so its own edits move with
  their functions. Then the branch merges `main`. The conflicts are then only
  in the functions that both sides changed. This uses `git merge`, not a
  rebase. The branch rules of "Work that this RFC builds on" decide who does
  the two steps:
  - For a pull request that is not CLEAN yet, the PM's announcement of the
    split window counts as the PM's ask to merge `main`. The author runs the
    two steps.
  - The author of a CLEAN pull request does not change the branch. The merge
    session runs the two steps on that branch when it merges it.
- Timing: the split lands as the first merge of a merge session, right after
  the session merges the other ready pull requests. That is when the fewest
  open pull requests touch the file. The PM announces it to the open
  sessions with the plan file and the two steps above.
- The script stays in `scripts/` until no open pull request needs it, and
  then a small pull request removes it.

#### `e2e/e2e_test.go` by scenario

The 466 functions are already named scenarios and their checks. The plan
keeps `TestCLIs`, the matrix, `runOnStore` and the report hooks in
`e2e_test.go`, and moves the scenarios into files by area. The final plan
comes from the call graph of each scenario. The first draft:

| New file | Scenarios and their checks |
| --- | --- |
| `pulumi_scenario_test.go` | `pulumiScenario` and its steps |
| `pulumi_import_test.go` | `TestPulumiFileImport`, `TestPulumiServiceImport` and their scenarios |
| `pulumi_secrets_test.go` | the secrets provider scenarios |
| `tofu_scenario_test.go` | `tofuScenario` and its steps |
| `tofu_lock_test.go` | `tofuLockScenario`, `tofuForceUnlockFenceScenario` |
| `tofu_state_test.go` | `tofuStatePushScenario`, `tofuStateVersionListScenario`, `tofuStateWriteGateScenario`, `tofuSealedStateScenario` |
| `tofu_import_test.go` | `TestTofuPairHosts`, `TestTofuImportReplay` |
| `backup_restore_test.go` | `TestBackups`, `TestBackupRestore`, `TestBackupCheck`, `tofuRestoreScenario` |
| `maintenance_test.go` | `TestMaintenance`, `tofuMaintenanceScenario` |
| `binary_test.go` | `TestBinaryRunAndRestart`, `TestUpgradeCheck` |
| `inventory_scenario_test.go` | `inventorySchemeScenario`, `collectionsScenario`, `manualLockRoutesScenario`, the context scenarios |
| `login_test.go` | `TestPairTwoLogins` |

Each new file stays under 1,500 lines. A scenario of more than 1,500 lines
has its own file. After the split, the rule for new work is one file for each
new scenario. The brief `e2e/AGENTS.md` says so.

#### `pkg/dustpan/dustpan.go` by area

| New file | Declarations |
| --- | --- |
| `options.go` | the 43 `With...` options and `ipAllowlistConfig` |
| `settings.go` | `defaultSettings`, `defaultDenialLimiter`, the `check...` functions |
| `services.go` | `newServices` and the `new...Service` and `wire...` functions |
| `handlers.go` | `newAdapters` and the `new...Handler` functions |
| `mux.go` | `rootMux`, `newRootMux`, `Handle`, `HandleFunc`, `ServeHTTP`, `mountAPI`, `buildMux`, `routes`, `debugRoute` |
| `run.go` | `Run`, `newHTTPServer`, `listen`, `serveUntilDone`, `drain`, `shutdownServer`, `startUp`, `startBackground`, the sweep functions |
| `requestlog.go` | `withRequestLog`, `logRoute`, `RequestID`, `newRequestID`, the status writer |
| `backupwiring.go` | `newBackupService`, `backupOperations`, `newRestoreService`, `restoreLiveError`, `afterRestore`, `startBackupWorker` |
| `dustpan.go` | `Server`, `NewServer`, `assemble` and the types |

A second pull request, with a behavior check but no behavior change, splits
`mountAPI` (105 lines) into one mount function for each area, in the file of
that area. `mountAPI` then only calls them in order. A new feature then adds
one line to `mountAPI` and its own function in its own file, as `drift.go`
and `jobs.go` do today. **Reason:** this removes the cause of the repeated
`funlen` findings and of most conflicts, and not only the line count.

#### Later splits

`pkg/workspace/workspace_test.go`, `pkg/store/storetest/workspaces.go`,
`internal/api/pulumi/updates_test.go`, `pkg/store/storetest/updates.go` and
`e2e/coverage.go` use the same script and the same timing. One pull request
for each file, in the order of the size budget list.

### Q4. Area briefs: nested agent files that hold rules only

The owner rejected standing lead sessions for each area as overhead. The
context of an area lives in files instead. A nested agent file is the brief of
its area. An area label on issues and the briefs of the PM point at it.

#### What the research says

The community research session of 2026-10-30 reported these findings. The
grades are those of that session. The author of this RFC did not read the
sources again.

- Developer-written `AGENTS.md` and `CLAUDE.md` files did not improve task
  success on average, and they added more than 20% to the cost. Overviews of
  the repository did not help. Agents did follow explicit instructions
  (ETH Zurich and LogicStar, arXiv 2602.11988, grade A).
- Each model that was tested got worse as its input grew ("context rot",
  Chroma, grade A).

So an area brief holds rules, not a description. It has no overview and no
layout. The same applies to the root file: its "Layout" section has 92 of its
365 lines, and it describes, it does not instruct.

#### The file format

| Option | Cost | Gives |
| --- | --- | --- |
| `CLAUDE.md` is the source, and `AGENTS.md` is a symbolic link to it | The Edit tool of Claude Code refuses to write through a symbolic link, as the research session reports. Agents then edit the wrong file or fail. | One content, but a trap for each edit. |
| `AGENTS.md` is the source. `CLAUDE.md` next to it is a short file: `@AGENTS.md`, then only the lines that apply to Claude Code alone | Two files in each directory, one of them one or two lines long. | `AGENTS.md` is the format that several tools read. Claude Code reads the import (`@path`, up to 4 levels deep). No link. |
| `AGENTS.md` only | Claude Code reads `AGENTS.md` natively since version 2.1.277, as the research session reports, but only when no `CLAUDE.md` is in the directory or above it. The root has a `CLAUDE.md`, so a nested `AGENTS.md` alone may not load. | Fewest files, but it depends on the root. |
| `.claude/rules/*.md` with `paths:` front matter | `gopls.md` uses this today. | It works for Claude Code only. |

**Recommendation:** `AGENTS.md` is the source in each directory, and
`CLAUDE.md` imports it. This also applies to the root. Today the root
`AGENTS.md` and `CLAUDE.md` are two separate regular files. On `a7dc146` they
are byte for byte equal, but no check keeps them equal, and an edit of one
does not reach the other. The root `CLAUDE.md` becomes `@AGENTS.md` plus the
Claude-only lines, for example the Repowise tool names. **Reason:**
`AGENTS.md` is the standard across tools. The import keeps one source without
a link that the Edit tool refuses. This is owner question 8.

#### The briefs

- Briefs only where a package has its own rules. The first set:

  | Brief | Rules that it holds | ADRs that it follows |
  | --- | --- | --- |
  | `pkg/store/AGENTS.md` | Both dialects for each change. Fixed queries in `queries.sql` with sqlc, never an edit of `internal/db`. New numbered migration files, the frozen and collapse rules, `make migration-renumber`. The fence in the same transaction. Unix milliseconds in `BIGINT`. A port method gets conformance tests first. `storefault` and `blobfault` forward each method, and the method set test checks it. | 40, 73, 274 |
  | `pkg/blob/AGENTS.md` | The `blobtest` suite for each adapter. A key for each write attempt. A blob removal waits for a running backup or restore. | 221, 281 |
  | `internal/api/AGENTS.md` | The endpoint checklist in short, with a pointer to the skill. Each route declares its actions. Each error path has a code of the catalog. The description version and `make api-version-bump`. `make clients-check`. The e2e step first. | 17, 264, 272, 276 |
  | `e2e/AGENTS.md` | PostgreSQL is required, and `pg-ready`. One file for each scenario. Route coverage and the contract file of each CLI version. The build tags. | 12 |
  | `pkg/backup/AGENTS.md` | The table list, the control tables, the staged-row checks, both dialects, the restore round trip, a pointer to the skill and to issue #2115. | 30, 38, 42 |
  | `pkg/dustpan/AGENTS.md` | One file for each area. A new feature adds its own mount function. No new code in `dustpan.go`. | 35, 272 |
  | `cmd/s3bench/AGENTS.md` and `load/AGENTS.md` | Each model has a `Follows:` line. A change to a followed symbol checks the model. | none |

  Each has a `CLAUDE.md` next to it that imports it. The ADR numbers in this
  table are the first draft. The pull request that writes each brief checks
  them against `docs/adr/`.
- Each brief has 40 lines or fewer, in three fixed sections and one last
  line, in this order:
  - **Invariants:** the rules that each change of the area keeps.
  - **Pitfalls:** the mistakes that review or a `fix-main` pull request
    found in this area, one line each, with the rule that prevents them.
  - **Gates:** the commands that fail when a rule of the area breaks, for
    example `make merge-gates` or one named test.
  - A last line `ADRs: <numbers>`.

  No overview, no layout and no description of the code. A line that does
  not tell the agent to do or not to do something does not go in a brief. It
  is in strict ASD-STE100, because it is an instruction for agents (ADR 14).
  It holds no copy of a global rule. The root file stays the only place for
  global rules.
- Each area brief has an issue label of the form `area:<name>`, for example
  `area:store`, `area:api`, `area:e2e`, `area:backup` and `area:dustpan`.
  `docs/agents/issue-tracker.md` lists each label with the path of its
  brief. A PM brief to a worker names the area brief to read first, instead
  of a copy of its content. A worker that has an issue with an area label
  reads that brief before it reads code.
- The pitfall loop. Each review finding and each `fix-main` pull request that
  shows a pitfall of an area changes that area. The first choice is a gate:
  a test, a lint rule or a Make target. When no gate is possible, the pull
  request that fixes the finding adds one line under **Pitfalls**. A pitfall
  line that later gets a gate moves to **Gates**. The 40-line limit forces
  this: a long pitfall list is a sign that a gate is missing. For example,
  #2103 would have added the method set test of Q1g to **Gates** of
  `pkg/store/AGENTS.md`, not a line of text. Stripe and Spotify report the
  same approach: lessons go into mechanical checks, not into prose (grade B).
- A check, `scripts/agent-files-check.py`, run by `make docs-lint`:
  - Each nested `AGENTS.md` has 40 lines or fewer and the three sections and
    the `ADRs:` line in their order.
  - Each ADR that it names exists in `docs/adr/`.
  - Each `CLAUDE.md` in a directory with an `AGENTS.md` starts with the line
    `@AGENTS.md`. No agent file is a symbolic link.
  - When an ADR says that it supersedes ADR N, each brief that names N must
    also name the new ADR. This is how the briefs stay current without a
    person who remembers them.
- `make docs-lint` also runs the STE linter on the briefs.
- Who updates them: the author of a change to a rule of the area, in the same
  pull request. The author of an ADR that changes a rule of the area, in the
  ADR pull request. The check above fails when a superseded ADR is named
  alone. A reviewer checks the brief of each area that the diff touches.

#### The root file

**Recommendation:** a separate pull request (owner) cuts the overview parts of
the root file and keeps its rules. The "Layout" section (92 lines) goes. A
rule in it that an agent needs, for example "the web console lives in its own
repository", moves to the section where it applies. The first paragraphs keep
the naming rules about other projects, and lose the product description.
**Reason:** the research found that overviews add cost and do not help, and
that each added line of input makes the result worse. An agent finds the
layout with `ls`, Repowise or the briefs.

**Reason for the whole of Q4:** a short file of rules next to the code is read
when the agent works there, which is the moment the rule matters. It costs no
session that waits for work. The area label and the PM brief lead a worker to
it before the first edit. The 40-line limit keeps it a brief, not a second
manual, and pushes each pitfall towards a gate. The superseded-ADR check makes
staleness a fail, not a memory task.

ADR 66 lists `CLAUDE.md` among the files that may hold internal references.
This RFC reads that as each agent file, also a nested `AGENTS.md` (open
question 4).

### Q5. Project skills

Three skills, each a workflow that spans several files:

| Skill | Steps, each with its gate |
| --- | --- |
| `od-add-endpoint` | The e2e step first. The route and its authorization actions (the route test). The description and `make api-version-bump` (the pin test). `make clients-generate` (`make clients-check`). The error codes of the catalog (the error code gate). The user documentation page (ADR 214). `make merge-gates`. |
| `od-add-store-method` | The port method and its documented errors. The conformance tests first. Both dialects, with sqlc for fixed queries. The fence. The `storefault` forward (the method set test). `make generate`, then `make merge-gates`. |
| `od-add-backup-table` | The migration in both dialects. The table list or the control table list with its reason. The column list (`ErrCoverage`). The staged-row checks. The restore round trip. `make merge-gates`. |

No skill for the merge session. `docs/agents/merge-session.md` of the steward
pull request holds those steps: the combined head of up to 6 pull requests,
the drop-last rule, and one `commit-tree` merge for each pull request. A
second copy of the steps would drift from it. A merge session reads that
file, and the root agent file points at it.

| Option for the location | Cost | Gives |
| --- | --- | --- |
| `.agents/skills/<name>`, next to the upstream skills | The root rule says: never edit files under `.agents/skills/` by hand. `npx skills` can change that directory. | Confusion between managed and own skills. Six Repowise skills are there today with no entry in `skills-lock.json`, so the confusion exists already. |
| `.agents/project-skills/<name>/SKILL.md`, with a link in `.claude/skills/<name>` | One new directory. | A clear line: `npx skills` manages `.agents/skills/`, we write `.agents/project-skills/`. |
| `docs/agents/skills/` | `docs/agents/` holds the lead workflow, which is a different audience. | Mixed content. |

**Recommendation:** `.agents/project-skills/`. The link in `.claude/skills/`
is for reading only. An agent edits a skill at its path in
`.agents/project-skills/`, because the Edit tool refuses to write through a
link (Q4). Each skill name starts with
`od-`, and its front matter has `metadata: {owner: OpenDustpan}`. A check in
`scripts/agent-files-check.py` fails when a directory under `.agents/skills/`
has no entry in `skills-lock.json`, and when a directory under
`.agents/project-skills/` has no link in `.claude/skills/`. The six Repowise
skills move to `.agents/project-skills/` in the same pull request.
`make docs-lint` runs the STE linter in strict mode on each project
`SKILL.md`. The skill pull request checks that OpenCode finds the skills
through `.claude/skills/`, and adds a second link if it does not.

**Reason:** the prefix and the directory show at a glance which skills we
own. The lock check keeps the two sets apart by code, not by memory.

### Q6. A sweep of dead code and duplicate helpers

Sources of candidates:

| Source | What it finds | Limits |
| --- | --- | --- |
| golangci `unused` | Unused unexported code. | Enabled today. It finds nothing new. |
| golangci `dupl` | Duplicate blocks of 120 tokens or more. | Enabled, but tests and the conformance suites are excluded, and that is where helpers repeat. |
| `deadcode` from `golang.org/x/tools` | Functions that no `main` and no test reaches, across packages. | A new tool. It would run with `go run ...@<version>`, as `nilaway` and `govulncheck` do, so `go.mod` does not change. This RFC asks for it explicitly. |
| Repowise `get_dead_code` | Unused exports and unreachable files. | 64 findings, all at confidence 0.4. Most of the 20 in `internal/clientgen` are false positives. |

**Recommendation:**

- A report target `make deadcode-report`, not part of `make check`. It runs
  `deadcode -test` with all build tags of the repository (`e2e`, `upgrade`,
  `soak`, `faults`, `journalrecording`), and `dupl` with a threshold of 80
  tokens on test files only. It writes a list. It never fails.
- Exported symbols of `pkg/` are the public SDK (ADR 272). They are not dead
  because no code in this repository calls them. The sweep leaves them out,
  unless the SDK surface check also agrees.
- Each removal is one pull request for one package, with 300 removed lines or
  fewer. For each symbol, the pull request shows: `rg` finds no other use,
  including Go templates, reflection by name and the e2e build tags, and
  `make merge-gates` passes.
- A duplicate helper moves to the `*test` package of its domain, or to
  `internal/testutil/<area>` when there is no such package. One helper for
  each pull request.
- The sweep has the lowest priority in the plan. It starts after the gates
  and the splits.

**Reason:** the tools give candidates, not proof. Small removals of one
package are easy to review and easy to revert. A report that never fails does
not block other work.

### Q7. A metric for review precision

Review by a model finds real defects, but many of its findings are not
defects. On SWR-Bench, the precision of review by a model was about 17%.
Five combined review passes raised F1 by 23% to 44% (grade A).

**Recommendation:** measure the precision of each reviewer model in this
repository. Precision is the share of review findings that the author fixes.
`docs/agents/review-protocol.md` of the steward pull request already numbers
each finding, and the author answers each number. The author's answer then
starts with "fixed" or "not fixed", and "not fixed" has a reason. The PM log counts both for
each model, each week. This changes only `docs/agents/` (owner). **Reason:**
without a number, the project cannot tell a useful reviewer from a noisy
one. The count also shows whether several passes pay for their cost here,
before the project adds them.

## Work that this RFC builds on

The ways-of-working steward session has the PM's go-ahead for one pull
request that does three things:

- It changes `scripts/checks-agent.sh`: native PostgreSQL with
  `pg_ctlcluster`, no rebase option, and no credential literal.
- It adds `scripts/agent-preflight.sh`, which checks that PostgreSQL is up,
  the lint version, the merge base and the free disk space.
- It updates `docs/agents/`: `merge-session.md`, `worker-brief.md` and
  `lead-workflow.md`. From the next merge session on, merges also gate once
  on the combined head of each batch, with a full `make check` and
  `make e2e` and no package subsets.

The PM accepted these process rules on 2026-10-30 too. The follow-up pull
requests of this RFC keep them:

- A merge batch has at most 6 pull requests. Store port changes come last,
  and the largest pull requests come just before them.
- After a review gives CLEAN, the author does not change the branch. Only the
  merge session renumbers API versions and migrations. So the bump and
  renumber targets of Q1c are tools of the merge session.
- An author merges `main` only for a real conflict or when the PM asks.
- The reviewer sends must-fix items straight to the author. The PM hears only
  CLEAN, the end of round 3 or a blocker.
- Round 1 is a full review. Rounds 2 and 3 check the must-fix items again
  only, on Sonnet. A security check stays on Opus.
- The `needs-owner` label marks what only the owner can decide. A session
  searches the open pull requests before work starts.
- The mutation self-check: the author lists each defense layer with the test
  that fails without it.

`docs/agents/review-protocol.md` of the steward pull request holds the review
rules. `docs/agents/merge-session.md` holds the merge steps.

This RFC does not redesign that work. Pull requests 1, 2 and 3 below change
the scripts of the steward pull request only where they say so. No skill of
Q5 copies the merge steps.

## Plan of the follow-up pull requests

Each pull request is small and has one concern. "Owner" means that the owner
merges it, because it changes the process or the agent rules. The conflict
column names the shared files and the open drafts that touch the same area.

| # | Pull request | Files | Gate that it adds | Conflicts | Depends on |
| --- | --- | --- | --- | --- | --- |
| 1 | Tool version variables and the version check | `Makefile`, `scripts/tool-pins-check.py`, `pinned_lint_version` in `scripts/agent-preflight.sh` | `make lint` fails on a golangci-lint version that is not the pin, and on a CI pin that differs from the Makefile. The preflight reads the new variable. | `Makefile`, `scripts/agent-preflight.sh`. Lands after the steward pull request. | the steward pull request |
| 2 | PostgreSQL is required, and `pg-ready` | `Makefile`, the e2e summary in `e2e/report.go`, a check name argument in `scripts/agent-preflight.sh` | `make test`, `make e2e`, `make e2e-ha` fail without the URL, unless `DUSTPAN_TEST_SQLITE_ONLY=1`. `pg-ready` starts PostgreSQL when it is down (`PG_START=0` opts out) and fails within 30 seconds. | `Makefile`. CI jobs without PostgreSQL set the opt-out explicitly. | the steward pull request |
| 3 | `make merge-gates` | `Makefile`, a merge mode in `scripts/checks-agent.sh` as the steward pull request leaves it | A merge mode that refuses `SKIP`. The script already does the rest. | `scripts/checks-agent.sh`. Lands after the steward pull request. | 1, 2, the steward pull request |
| 4 | Version bump and migration renumber targets | `Makefile`, `scripts/migration-renumber.sh`, a test in `pkg/store/internal/migrate` | A test fails on two migration files with one number in one dialect | Migration test files, low | none |
| 5 | Method set tests of the fault wrappers | `internal/store/storefault/methods_test.go`, `internal/blob/blobfault/methods_test.go` | A fail for an adapter method that the wrapper does not forward | Draft #2114 changes the blob `Store` port. Land after it, or add its method to the test. | none |
| 6 | The credential URL rule | `.semgrep/credential-url.yml`, `Makefile`, a test helper package | `make semgrep` fails on a new credential-shaped literal | Draft #2117. Land after it. | none |
| 7 | The push hook (owner) | `.claude/settings.json`, `.claude/hooks/push-alone.py` | A refused compound `git push`, with the allowed form | `.claude/settings.json`, low | none |
| 8 | `Follows:` lines of the benchmark models | `cmd/s3bench/*.go`, `load/*.go`, one test in each | A fail for a model that names no live symbol | Draft #2045 changes `cmd/s3bench`. Land after it. | none |
| 9 | The size budget | `scripts/size-budget.py`, `scripts/size-budget.txt`, `Makefile` | A fail for a new file over 1,500 lines, and for a listed file over its ceiling | `Makefile`, low | none |
| 10 | Area briefs and their check (owner) | the eight `AGENTS.md` briefs of Q4, a `CLAUDE.md` with `@AGENTS.md` next to each, the root `CLAUDE.md` as an import of the root `AGENTS.md`, `scripts/agent-files-check.py`, `Makefile`, the `area:` labels in `docs/agents/issue-tracker.md` | Line limit, the three sections and the ADR line, ADR names, the import line, no symbolic links, superseded ADRs, STE lint | `docs/agents/` with the steward pull request. Lands after it. | 2, 3, 4, 5 (the briefs name their gates), the steward pull request |
| 11 | Project skills (owner) | the three `.agents/project-skills/od-*/SKILL.md`, the six Repowise skills moved, `.claude/skills/` links, the check of PR 10 | Lock and link checks, STE lint in strict mode | `.claude/skills/` | 3, 4, 10 |
| 12 | The split script and the split of `e2e/e2e_test.go` | `scripts/split-go-file.go`, `scripts/split-plans/e2e.txt`, `e2e/*_test.go` | The size budget line of the file goes down | Most open pull requests that add an e2e step. First merge of a merge session. | 9 |
| 13 | The split of `pkg/dustpan/dustpan.go` | `scripts/split-plans/dustpan.txt`, `pkg/dustpan/*.go` | Same | Most open pull requests that add a route. First merge of a merge session, not the same session as 12. | 12 |
| 14 | `mountAPI` by area | `pkg/dustpan/mux.go` and the area files | `funlen` passes with no exception for `mountAPI` | Same as 13 | 13 |
| 15 | Later splits, one file each | the five files of Q3 | Same | Each with its own window | 12 |
| 16 | `make deadcode-report` | `Makefile` | None. A report. | Low | 3 |
| 17 | Removals, one package each | the package | None new | Low | 16 |
| 18 | The root agent file keeps its rules only (owner) | `AGENTS.md`, `CLAUDE.md` at the root | The agent file check of PR 10 also runs on the root | Each pull request that edits the root file. Lands in a quiet window. | 10 |
| 19 | The fast `Stop` hook (owner) | `.claude/settings.json`, `.claude/hooks/stop-verify.py` | Fast gates on the changed files before the agent stops | `.claude/settings.json`, with PR 7 | 1, 6, 9, 10 |
| 20 | Review precision in the review and merge docs (owner) | `docs/agents/` | None. A metric. | `docs/agents/`, with the steward pull request | the steward pull request |

Pull requests 1, 2, 4, 5, 6, 8 and 9 can run in parallel. They touch the
`Makefile` in different targets. Each of them must run `make merge-gates`
once 3 lands.

## Open questions

1. Q1b: should CI jobs that today run without PostgreSQL set
   `DUSTPAN_TEST_SQLITE_ONLY=1`, or should each such job have a PostgreSQL
   service?
2. Closed. `pg-ready` starts PostgreSQL, as the preflight script does.
   `PG_START=0` opts out.
3. Q2: is 1,500 the right first limit, or does the owner want 1,000 with a
   list of 120 files?
4. Q4: does ADR 66 decision 3 cover nested `CLAUDE.md` files, the area
   briefs? This RFC reads
   it so. If not, the nested files cite no ADR numbers, and the
   superseded-ADR check of PR 10 goes away.
5. Q5: `od-` as the prefix of project skills, or another prefix?
6. Q6: is `deadcode` from `golang.org/x/tools`, run with `go run` and a
   pinned version, an accepted new tool?
7. Q3: who announces the split windows to the open sessions, and how long
   after the announcement does the split merge?
8. Q4: is `AGENTS.md` the source in each directory, with a `CLAUDE.md` that
   imports it, also at the root? The other choices are in Q4.
9. Q1f: when the CI machines have the capacity, should merges move to the
   GitHub merge queue?

## Sources

The community research session of 2026-10-30 collected these sources and
graded them. A is a peer-reviewed or replicated study, B is an engineering
report of a company. The author of this RFC did not read them again.

- ETH Zurich and LogicStar, a study of agent context files, arXiv 2602.11988
  (A).
- Chroma, "context rot": the quality of each tested model goes down as its
  input grows (A).
- A study of conflicts between pull requests by agents, arXiv 2607.04697 (A).
- Uber, SubmitQueue, a queue that tests each change on its merge head.
- Stripe and Spotify, engineering reports on lessons as mechanical checks and
  on verifiers in a `Stop` hook (B).
- SWR-Bench, a benchmark of code review by models (A).
- The research session's report on the `AGENTS.md` support of Claude Code
  (version 2.1.277), its `@path` imports and its Edit tool with symbolic
  links.

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write
  waits for another installation (ADR 248 decisions 3, 6 and 9). This RFC
  changes no data path.

## Outcome

Empty until the RFC is accepted or dropped.
