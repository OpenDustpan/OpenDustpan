---
version: 1
---

Issue: #2580.

## Problem

e2e/e2e_test.go is a 15258-line legacy file. Moving masked-state helpers out by prefix shrinks it with zero behavior change.

## Evidence

- e2e helpers for masked state live in e2e test files, not in production packages. Proof: rg -n -F --glob '!*_test.go' 'checkMasked' pkg internal cmd matches 0; same 0 for maskedState, maskedFilter, checkDeltaMasked
- Both the source god file and the masked-helper target file exist. Proof: paths e2e/e2e_test.go exists true and e2e/e2e_masked_test.go exists true
- The stateful chain under test is TestCLIs. Proof: e2e/e2e_test.go:116 (TestCLIs)

## Fix boundary

- Move every function starting with checkMasked, maskedState, maskedFilter, or checkDeltaMasked and their unexported types from e2e/e2e_test.go to e2e/e2e_masked_test.go with same build tags and no body edits

Out of scope:

- storetest suite split
- dustpan wiring split
- live-secret fixture flag fix, which is a prerequisite owned by its own item and not moved here
- other e2e helper prefixes such as state-diff or ready helpers

## Check

- Move checkMasked, maskedState, maskedFilter, and checkDeltaMasked helpers plus solely-used unexported types from e2e/e2e_test.go to e2e/e2e_masked_test.go with exact build-tag copy and no body edits in e2e/e2e_masked_test.go; BLOCKED by no server route: this move calls no route and uses no credential. Check: go vet ./e2e/ plus empty diff of moved function bodies
- Verify e2e/e2e_test.go and e2e/e2e_masked_test.go carry the same build tags and the same test set compiles; BLOCKED by no server route: this move calls no route and uses no credential. Check: diff of build-tag lines of e2e/e2e_test.go versus e2e/e2e_masked_test.go is empty
- Report line counts of e2e/e2e_test.go and e2e/e2e_masked_test.go before and after the move in e2e/e2e_masked_test.go; BLOCKED by no server route: this move calls no route and uses no credential. Confirmed by: CI (go vet plus lint). Check: make lint plus line counts posted in the pull request body

Depends on:

- `architecture-review-7-nov-composition-storetest-area-split` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TestCLIs runs the stateful per-CLI chain on main. (`e2e/e2e_test.go:116 (func TestCLIs)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change need a behavior or architecture decision? No. The issue scope is a pure move inside the same package with the same build tags and no body edits, so no behavior decision applies.
- Does this change overlap storetest or dustpan wiring? No. This part touches only e2e helpers in e2e/e2e_test.go. Storetest suites and dustpan wiring are handled by the other parts.
- Who fixes the live-secret flag and when? Answered. The live-secret flag must be fixed before the move by its owning item. The move skips any flagged line until the fix lands, so the flag is not carried into the target file.
- Which build tags must the new file carry? Answered. Copy the build-tag lines exactly from the source file and diff them. No tag names are listed because the pack does not state them.
- What happens to shared unexported symbols that do not match the prefixes? Answered. Move only the prefix-matched functions and types used solely by them. Shared constants, variables, helpers, and imports stay in e2e/e2e_test.go.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
