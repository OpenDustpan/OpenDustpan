# Tool trials

This file lists the agent tool trials that run now. Each trial has an end
date and one metric. On the end date, the steward session measures the
metric and gives the PM a keep, change or drop recommendation.

## Trials from 2026-10-30 to 2026-11-06

| Trial | Change | Metric | Baseline |
| --- | --- | --- | --- |
| No gopls MCP server | `.mcp.json` has no `gopls` server. `.claude/rules/gopls.md` is removed, because it describes only that server. | Lint-fix commits per merged pull request, together with the next trial. | See the next row. |
| No gopls-lsp plugin | `.claude/settings.json` sets `gopls-lsp@claude-plugins-official` to `false`. | Lint-fix commits per merged pull request: commits whose subject names lint, golangci, gofmt, errcheck, staticcheck, vet or nilaway. | 0.26 per pull request (12 commits in 47 pull requests, merged from 2026-10-27 to 2026-10-30). 11 of 47 pull requests (23%) had one or more. |
| Repowise before the first edit | Every brief holds the line below. The Repowise section of `CLAUDE.md` stays. | Repowise calls per session. Also the share of Repowise answers that a grep or a Read for the same thing follows within 3 tool calls. | 0 Repowise calls in 1,160 tool calls of 20 sessions (2026-10-27 to 2026-10-30). |

## The line for each brief during the Repowise trial

```
Before your first edit, load the Repowise tools with ToolSearch
(query "repowise get_context"), then call get_context on each file that
you will change.
```

## End of a trial

On 2026-11-06, the steward session measures each metric over the pull
requests merged and the sessions started from 2026-10-30 to 2026-11-06.
To end a trial, revert its change in one pull request. The owner merges
changes to `.claude/` and `.mcp.json`.
