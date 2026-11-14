---
version: 1
---

Issue: #2932.

## Problem

The test package pkg/forgelink has no leak guard, so a test that leaves a goroutine running does not fail the package.

## Evidence

- The directory pkg/forgelink holds forgelink_test.go. A leak guard file is missing from the checkout. Proof: paths: pkg/forgelink exists true; pkg/forgelink/forgelink_test.go exists true. The paths list shows no leak_test.go entry that exists in pkg/forgelink.
- The rule asks for a leak guard whose TestMain calls goleak.VerifyTestMain in each test package. Proof: issue #2932 body: Rule CLAUDE.md, definition of done: each test package has a leak_test.go whose TestMain calls goleak.VerifyTestMain.
- A helper comment on main names goleak.VerifyTestMain. Proof: code_facts command rg -n -F --glob '!*_test.go' 'goleak.VerifyTestMain' pkg internal cmd shows 1 match: internal/api/apidesc/apidesctest/main.go:12.

## Fix boundary

- Add the NEW file pkg/forgelink/leak_test.go with TestMain that calls goleak.VerifyTestMain.

Out of scope:

- A check in make lint that lists each test package without a leak guard
- Changes to forgelink behavior or existing tests, except fixing a leak that the new guard exposes

## Check

- Confirm the existing package clause and that no TestMain exists in pkg/forgelink test files, then add the NEW file pkg/forgelink/leak_test.go with TestMain that calls goleak.VerifyTestMain. No route: the test guard runs in the test binary only. Check: rg -n TestMain pkg/forgelink/*_test.go shows only the NEW TestMain after the change
- Prove requirement fail-on-leaked-goroutine with a temporary leaked goroutine that fails the package, remove the temporary leak, then fix each leak the guard exposes in forgelink test code with no ignore option. No route: the test reads the test binary result only. Check: go test ./pkg/forgelink -count=1 fails with the temporary leak, then passes after it is removed
- Run the forgelink package tests and confirm the leak guard runs. No route: the test reads the test binary result only. Check: go test ./pkg/forgelink -count=1
- Run the definition of done check with zero findings. Confirmed by: CI (make check). Check: make check with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Another package already has the TestMain leak guard to copy. (`pkg/access/leak_test.go:10 (func TestMain)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR govern the leak test file? No. The pack lists no ADR and no RFC, and issue #2932 cites only the definition of done in CLAUDE.md. The fix adds only the missing guard file and changes no behavior.
- Should this change add the installation-wide missing-file check? No. The installation-wide lint check is a separate improvement and stays out of scope. This change adds only the guard file in pkg/forgelink.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
