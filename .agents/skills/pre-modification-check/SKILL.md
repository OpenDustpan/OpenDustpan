---
name: pre-modification-check
description: >
  Use before you modify, refactor, move or delete files, in particular shared packages,
  core modules, or files the user did not name. It shows the impact before you edit.
  Uses the Repowise MCP tools.
user-invocable: false
---

# Pre-modification check with Repowise

## Before you edit a file

Call `get_risk(targets=["path/to/file.go"])`. Batch all files in one call (maximum 25).
Pass `repo` (an alias from `list_repos`) to limit the answer to one repository. Read:

- `defect_profile`: present only for files with bug fixes. `bug_magnet` means repeated
  recent fixes. This is the strongest sign that an edit can break something. Say it first.
- `hotspot_score` and `trend`: a file with high churn and high complexity needs care.
- `dependents_count` and `impact_surface`: how many files depend on this one.
- `co_change_partners`: files that usually change together with this one. You may need to
  update them.
- `primary_owner` and bus factor.
- `test_gap` and `security_signals`: say so before you edit an untested or sensitive file.

`include` adds blocks: `graph`, `churn`, `owners`, `tests`, `blast`.

## Tell the user when

- `bug_magnet` is set.
- The hotspot score is above the 90th percentile.
- A file has more than 10 dependents. List the top ones.
- The bus factor is 1.
- `risk_type` is `bug-prone` or `high-coupling`.

## Public contracts

Files in `pkg/` and `internal/api/` define the SDK and the HTTP APIs. The console uses
these APIs. Before you change them, call `get_blast_radius(targets=["<symbol id>"])` to
see the consumers in the other repository. Do not remove or rename a field, endpoint or
status code that a released console or a supported CLI uses.

## Before a refactor or a move

Call `get_context(targets=["file.go"])` to see what uses the file and which decisions
apply. Then call `get_why(query="file.go")`. For a large refactor, also call
`get_health(targets=["file.go"])` for a before and after score.

## Errors

If `get_risk` returns an error, continue with the change and say that the risk check was
not available. The index can be behind the branch. Use the checkout as the truth.
