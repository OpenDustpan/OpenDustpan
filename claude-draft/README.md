# Proposed agent allowlist

This directory holds a PROPOSED `settings.json` for agent sessions. It is not
active. Nobody edited `.claude/settings.json`. The project owner reviews the
file and installs it by hand.

## Why

Agent sessions in accept-edits mode stop at permission prompts for commands
that are safe and routine (`go test`, `make`, `gh api`, `git fetch`, and
others). Each stop needs a person. This file allows those commands and denies
the dangerous ones.

## Base

The file starts from `.claude/settings.json` on branch `retro-agent-rules`
(pull request #2724, head 76fccdc). It keeps every entry of that file,
including the deny list (`--no-verify` and others). It adds entries to
`allow` and `deny`. It removes exact duplicates.

## Install

Run this command from the repository root:

```
mv claude-draft/settings.json .claude/settings.json
```

Then delete the directory `claude-draft/`.

## Review notes

A deny entry wins over a matching allow entry. Check these points before you
install:

- `Bash(gh api *)` is wide. The deny list stops DELETE, the merge of a pull
  request and the ref `heads/main`. Other POST, PUT and PATCH calls stay
  allowed.
- The `git push` deny entries for `main` use exact patterns, so a branch name
  that contains the text "main" (for example `drain/domain`) is not blocked.
- `Bash(make *)`, `Bash(go run *)` and `Bash(go generate *)` run code from the
  checkout.
- The base file already allows `Bash(git commit *)`, `Bash(git checkout *)`,
  `Bash(git merge *)`, `Bash(psql *)`, `Bash(opencode *)` and `Bash(droid *)`.
  This proposal keeps them, so the narrower entries that it adds
  (`git commit -s *`, `droid exec *`, and others) do not limit them.
- The new MCP entry `mcp__claude-code-remote__send_message` was already in the
  base file. This proposal adds `add_repo`, `register_repo_root` and
  `get_session`.
