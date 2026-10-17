---
name: code-health
description: >
  Use when the user asks about code health, quality, complexity, technical debt, risky or
  hard-to-maintain files, what to refactor next, or coverage gaps. Also use to compare
  health before and after a refactor. Uses the Repowise MCP tools.
user-invocable: false
---

# Code health with Repowise

Repowise scores each file from 1 to 10 with fixed rules (complexity, nesting, cohesion,
duplication, untested hotspots, churn, ownership). A low score means the file is more
likely to contain defects. It does not only mean the file is big.

## Modes

- `get_health()`: dashboard. `fix_first` lists what to fix first. Use it for "how healthy
  is this code?" and "what do we clean up?".
- `get_health(targets=["path/a.go", "path/b.go"])`: score and findings for those files.
- `get_health(fix_id="…")`: the steps and the tests to run for one `fix_first` item.

`include` adds blocks: `biomarkers` (findings), `refactoring` (ranked plans), `coverage`
(only if coverage was ingested), `trend` (recent snapshots).

Pass `repo` (an alias from `list_repos`) to limit the answer to one repository.

## Use the results

1. For "what do we refactor?": start with `fix_first`. Open an item with `fix_id`. Give
   the plan, not only the scores.
2. Rank by `weighted_deficit`, not `score`. The score has a floor of 1.0.
3. For one file: give the score and the top two or three findings. Say in plain words
   what each finding means. Do not copy the raw payload.
4. A target in `unresolved` matched nothing. Do not call it clean. A target in
   `not_indexed` is not in the hosted index yet.
5. Before you edit a low-health file, call `get_risk(targets=[...])`. A file that is low
   health and a churn hotspot needs the most care.
6. The index can be behind the branch. Compare against the files in the checkout before
   you act on a score.

This project requires `make check` to pass. Repowise scores do not replace it.
