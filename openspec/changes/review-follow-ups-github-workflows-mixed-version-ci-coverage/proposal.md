---
version: 1
---

Issue: #2645.

## Problem

The mixed-version upgrade tests exist, but CI does not run them on both stores with full git history. Upgrade regressions can reach operators late.

## Evidence

- TestMixedVersion exists on main. Proof: e2e/upgrade_mixed_test.go:74
- TestMixedVersionBelowMinimum exists on main. Proof: e2e/upgrade_mixed_test.go:219
- The make target mixed-version-test exists on main. Proof: Makefile:310
- The review asks for a CI job with complete history and PostgreSQL at ci.yml:449. Proof: #2645 body: Files: .github/workflows/ci.yml:449 on cd18846

## Fix boundary

- Add one mixed-version upgrade CI job on SQLite with full git history.
- Add one mixed-version upgrade CI job on PostgreSQL with full git history.
- Both jobs run TestMixedVersion and TestMixedVersionBelowMinimum, including the zero-tag fallback when it applies.

Out of scope:

- Secret backend verification across both backends
- Denied module and provider publish audit
- Refusal of restored runner credentials with missing runner

## Check

- Add the SQLite mixed-version job with full history and a 30 minute timeout to .github/workflows/ci.yml (NEW job, near line 449). It runs `make mixed-version-test` for TestMixedVersion. Check: The CI log of the SQLite job shows fetch-depth 0 with tags and a pass. Confirmed by: CI (the SQLite mixed-version job).
- Add the PostgreSQL mixed-version job with the existing service wiring and a 30 minute timeout to .github/workflows/ci.yml (NEW job). It runs `make mixed-version-test` for TestMixedVersion. Check: The CI log shows the service ready, the existing DSN in use and a pass with no SQLite fallback. Confirmed by: CI (the PostgreSQL mixed-version job).
- Add a step to each new job in .github/workflows/ci.yml that prints the fetched tag count before the tests run. Check: The CI logs of both jobs show the tag count line. Confirmed by: CI (both mixed-version jobs).
- Confirm TestMixedVersionBelowMinimum in e2e/upgrade_mixed_test.go:219 passes in both new jobs, including the simulated zero-tag subcase. Check: The CI logs of both jobs show the pass. Confirmed by: CI (both mixed-version jobs).
- Run `make check` for the workflow edit. The edit touches .github/workflows/ci.yml only. Check: `make check` with zero findings. Confirmed by: the driver's gates.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has TestMixedVersion as an e2e upgrade test. (`e2e/upgrade_mixed_test.go:74 (func TestMixedVersion)`)
- Main has TestMixedVersionBelowMinimum as an e2e upgrade test. (`e2e/upgrade_mixed_test.go:219 (func TestMixedVersionBelowMinimum)`)
- Main provides the mixed-version-test make target. (`Makefile:310 (mixed-version-test)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph; a code check fails: a security change needs STRIDE threats for each trust boundary ("threats.items"); a code check fails: `.github/workflows/ci.yml` is named by the design or a task but is not in the touches of the stack plan.

Questions considered and answered:

- Does any ADR govern mixed-version CI coverage? No. The pack names no ADR, and the change adds CI jobs only with no product behavior change.
- Does the pack prove what CI runs today on both stores? No. The pack does not quote the CI workflow, so this change makes no claim about it beyond the linked review comment.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
