---
name: dead-code-cleanup
description: >
  Use when the user asks to clean up, remove unused code, find dead code, or reduce
  technical debt. Uses the Repowise MCP tools.
user-invocable: false
---

# Dead code cleanup with Repowise

Repowise finds dead code from the dependency graph. It needs no LLM.

## Find dead code

Call `get_dead_code()`. Useful arguments:

- `safe_only=true`: only findings flagged safe to delete.
- `kind`: `unreachable_file`, `unused_export`, `unused_internal` or `zombie_package`.
- `directory="pkg/old/"`: limit to a directory.
- `tier="high"`: only high-confidence findings.
- `min_confidence=0.7`: raise the minimum (default 0.4).
- `group_by="directory"` or `"owner"`: see where dead code collects.
- `repo`: an alias from `list_repos`. Without it, results come from both repositories.

## Present the findings

- Suggest deletion only for findings with `safe_to_delete: true`.
- Call other findings "candidates to investigate".
- Code loaded at run time can look dead. In Go, check for `init` functions, adapter
  registration, interface implementations, reflection and build tags. In this project,
  packages under `pkg/` are the public SDK. An unused export there may have outside users.
  Do not remove it without the user's approval.

## Before you delete

1. Show the user the name, the confidence and the reason. Get approval.
2. Call `get_risk(targets=["path"])` to check dependents.
3. Search the checkout for the name (use `rg`). The index can be behind the branch.
4. Code with recent git activity is more likely a false positive. Say so.
5. After the deletion, `make check` must pass.

## Order of deletion

1. Unreachable files.
2. Unused internal symbols.
3. Unused exports. These have the highest chance of a false positive.
