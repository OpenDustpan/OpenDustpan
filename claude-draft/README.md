# Proposed agent allowlist

This directory holds a PROPOSED `settings.json` for agent sessions. It is not
active. Nobody edited `.claude/settings.json`. The project owner reviews the
file and installs it by hand.

## Why

Agent sessions in accept-edits mode stop at permission prompts for commands
that are safe and routine (`go test`, `make`, `gh api` reads, `git fetch`, and
others). Each stop needs a person. This file allows those commands and denies
some dangerous forms.

## The deny list is not a boundary

- `Bash(go run *)`, `Bash(go test *)`, `Bash(go generate *)`, `Bash(make *)`,
  `Bash(droid *)` and `Bash(opencode *)` run code that the agent can write. That
  code can do anything that a denied command does. So each `Bash` deny entry
  slows a mistake down. It does not stop an agent that wants to pass it.
- The boundary for `main` must be on GitHub: a ruleset that blocks direct
  pushes, force pushes and the deletion of `main`, and a token scope without
  admin rights.
- A `Bash` rule matches the text of the command, with `*` as a wildcard. A
  rule on arguments matches only the forms that it names. Another spelling of
  the same command can pass.
- The `PreToolUse` hook `rtk hook claude` rewrites a command to `rtk <command>`
  and can answer `allow` with its own matcher. A command that it rewrites no
  longer starts with the text of a deny rule. Test this file with the installed
  `rtk` version before you rely on it.
- `rtk` 1.1.0 does not honor a `Bash` deny pattern that contains `:`. It
  reads `X:Y` as the old `prefix:*` form. So the deny `Bash(git push *main*)`
  has no colon, and the file allows no push to `origin`. A push to the
  repository URL prompts when `rtk` gives no decision.
- No deny pattern without a colon catches the delete of a branch with an empty
  source (`git push <url> :name`). Only the GitHub ruleset stops it.
- `rtk` does not apply `Read()` deny rules to the commands that it allows.
  So the secret files have a `Read()` deny and a `Bash` deny on their path.

## Base

The file starts from `.claude/settings.json` on branch `retro-agent-rules`
(head 76fccdc). It keeps the deny entries of that file. It adds entries to
`allow` and `deny`, and it removes entries that a wider entry already covers.

## Install

Run this command from the repository root:

```
mv claude-draft/settings.json .claude/settings.json
```

Then delete the directory `claude-draft/`.

## What the file allows

- Builds, tests, lint and generation: `go build`, `go test`, `go vet`,
  `golangci-lint`, `make`, `sqlc`, `go run`, `go generate`, `gofmt`.
- Git reads, `git add` of named files, `git commit`, `git merge`, `git fetch`
  from `OpenDustpan` and `origin`, and `git push` of `HEAD` to the repository
  URL. A push to `origin` is not allowed.
- `gh api` with no explicit method: GET reads and the implicit POST of a
  comment (`-F body=@file`). The deny of `-X` also blocks `-X GET`. For a
  search, put the query in the URL:
  `gh api 'search/code?q=<terms>'`.
- `go env` and `go env GO...` reads, and three `git config --get` keys:
  `remote.origin.url`, `user.name` and `user.email`.
- `psql "$DUSTPAN_TEST_POSTGRES_URL" -Atc ...` only.
- `sha256sum`, `npx ctx7@0.8.2` (a pinned release at least two weeks old),
  `python3 -I scripts/...` (not with `..` in the path).
- `rg`, `head` and `tail` are not allowed. A path can name a secret file in
  forms that no `Bash` pattern catches (`~/.config//gh`, `~/.config/./gh`,
  a glob, `rg -uuu` over a parent directory). Use the Read and Grep tools:
  they apply the `Read()` deny rules to the resolved path. `rg` with `--pre`
  or `--hostname-bin`, which run a program, stays denied.
- The MCP tools `register_repo_root` and `get_session`. `add_repo` is not
  allowed, so the owner sees each repository that an agent attaches.

## What the file denies

- Reads of the environment: `env`, `printenv`, `set`, `export -p`, the
  `environ` files under `/proc`, and the Read tool under `/proc`.
- Reads of the GitHub CLI configuration, `~/.git-credentials`, `~/.ssh` and
  `~/.netrc`, with the Read tool and with any `Bash` command that names them.
- `psql` shell escapes (`\!`), and `go env -w` and `go env -u`.
- `gh api` with any explicit method (`-X`, `--method`), `--input`, `graphql`,
  and the paths of collaborators, hooks, keys, branch protection, rulesets,
  renames, contents, dispatches, actions, environments, merges, releases, Git
  refs and tags, forks, reviews (reviews go through the MCP tool), and new
  repositories (`user/repos`, `orgs/.../repos`). A field flag (`-f`, `-F`)
  makes `gh api` send a POST, so these paths are denied for every method.
- `gh api` with a `%` in the command. GitHub decodes a percent-encoded path
  (`re%6Ceases` is `releases`), so an encoded path passes the path denies.
- `git push` of any command text that contains `main`, a push with an empty
  source (a delete), `-d`, `--delete`, `--mirror`, `+`, and force flags, also
  bundled (`-uf`, `-fu`).
- `git -C`, `git -c`, `git --git-dir` and `git --work-tree`. Run `cd` first.
- `git reset --hard`, `git rebase`, `git commit --amend`, `git stash`,
  `git clean`, `git checkout --`, `git checkout -f`,
  `git switch --discard-changes`, `git switch -f`,
  `git worktree remove --force` and `-f`, and `git branch -D`.
- A commit that skips the hooks: `--no-verify`, `-n` and bundled forms
  (`-sn`, `-ns`).
- `git add -A`, `git add .` and `git add --all`.
- The Edit and Write tools on `.claude/` and `.github/workflows/`, so that an
  agent cannot widen its own permissions or change CI.
- `subscribe_pr_activity`. This is intended: a swarm session does not watch
  PRs. The session instruction to subscribe after a PR opens does not apply
  to these sessions.

## Review notes

- Some deny patterns match text inside a branch name or a message, for
  example `-fu` in `fix-fuzz` or `-f` in `x-fix`. Such a command needs a
  prompt. This is the safe side.
- `sed` is not allowed: its `w` command writes files.
