---
name: change-review
description: >
  Use when you review changes before they merge: a PR, a branch diff, or the changes you
  just made. Use for "review this PR", "is this safe to merge", "what is the blast radius",
  "did I miss anything" and "what else must change with this". Uses the Repowise MCP tools.
user-invocable: false
---

# Change review with Repowise

Repowise joins git history (churn, ownership, co-change) with the dependency graph, test
gaps and recorded decisions. It shows what a change puts at risk, not only which files
changed.

The hosted index has no checkout. It knows only indexed (pushed) commits. It does not see
uncommitted work. It does not see a `base..head` range.

## Steps

1. **Get the changed files.** Use `git diff --name-only main...HEAD` for a branch. For a
   pull request, use the GitHub MCP tools to list the files. Use `git status --porcelain`
   for the working tree.
2. **Score the commits that exist in the index.** Call `get_change_risk(revspec="<sha>")`
   for one pushed commit (7 or more characters). Read `directive` and `health_delta`
   first. `risk_percentile` ranks the commit against the repository's own history. It does
   not predict bugs. If `health_delta` is null, the commit was not scanned. That does not
   mean the commit is clean.
3. **Check the changed files as a set.**
   `get_risk(targets=<changed files>, changed_files=<same files>)`. Read the `directive`
   block first:
   - `may_break`: dependents outside the diff. They may break. This is reach, not proof.
   - `missing_cochanges`: files that usually change together with these, but are not in
     the diff.
   - `missing_tests`: changed code with a test gap.
   - `tests_to_run`: the tests the change needs. If `tests_run_all` is true, run the full
     suite. State the `tests_to_run_basis` (`measured` or `inferred`).
   Add `include=["blast"]` for the full blast-radius detail.
4. **Check decisions.** For each non-trivial changed file, call `get_why(query="<file>")`.
   Report any decision that the change contradicts or replaces.
5. **Check health.** `get_health(targets=<changed files>, include=["biomarkers"])`.
   Report new complexity, deep nesting or duplication that the diff adds.
6. **Check other repositories.** If the change touches a public API or contract, call
   `get_blast_radius(targets=["<repo alias>"])`. The console repository uses the APIs of
   this repository.

## The review

Start with the risk level and the `directive` findings. Tie each finding to a file. Keep
"may break" (a dependent outside the diff) separate from "worth a look" (a co-change or
health regression). Do not add findings that the tools did not support.

## Errors

If a tool returns an error or nothing, say that Repowise data was not available and
review the raw diff. Do not claim the change is safe because a tool returned nothing.
