---
name: codebase-exploration
description: >
  Use to explore or understand the codebase: "how does X work", "explain the architecture",
  "where is Y implemented", "what does this module do", or any task that needs structure
  before you read source. Uses the Repowise MCP tools.
user-invocable: false
---

# Codebase exploration with Repowise

Before you search and read raw source, use the Repowise MCP tools. They return
documentation, ownership, history, decisions and graph structure, often in one call.

The workspace has two repositories: `opendustpan-opendustpan` (the backend) and
`opendustpan-console` (the web console). Pass `repo` to limit a call to one. Without it,
results come from both. `get_symbol` always needs `repo`.

## Which tool

| You want | Call |
|---|---|
| First orientation | `get_overview(repo=…)`. Call it once. |
| An answer to "how, where or why does X work" | `get_answer(question="…")`. It returns a cited answer and a `confidence` value. |
| To find a symbol, file or concept | `search_codebase(query="…")`. An identifier gives symbol hits (`symbol_id`, line range). A path gives file pages. Prose gives semantic search. |
| A summary card for files or modules | `get_context(targets=[…])`. Batch up to 25 targets. |
| Signature, location, callers and callees of one symbol | `get_symbol(repo=…, symbol_id="path::Name")` |

`get_symbol` does not return source code. The hosted index does not store source. Take
`file_path` and the line range from the result, then read that range of the file in the
checkout.

## Flow

1. For an unknown area, call `get_overview` once.
2. For a question, call `get_answer` first. If `confidence` is high, answer and cite the
   paths. If it is `medium` or `low`, follow `best_guesses[0].file` or
   `fallback_targets[0]` into `get_context`, then read the source.
3. For a named symbol or path, call `search_codebase`. Send symbol hits to `get_symbol`
   and file hits to `get_context`.
4. Read the source in the checkout for the details you need. The checkout is the truth.

## When to verify

Verify against the checkout when `_meta.stale_warning` is present, when `confidence` is
`medium` or `low`, or when a hit has `search_method: bm25`. The index is built from pushed
commits. It does not show uncommitted or unpushed changes.

## Errors

If a search returns nothing, call `get_context` with explicit paths. If the tools do not
connect, use the normal search and read tools, and say that Repowise was not available.
