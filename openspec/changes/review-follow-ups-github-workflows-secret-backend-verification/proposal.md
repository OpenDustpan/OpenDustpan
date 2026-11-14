---
version: 1
---

Issue: #2654.

## Problem

Operator upgrades an installation in steps. During the upgrade an old and a new server process run behind one public URL. The operator needs proof that a secret written on one version decrypts on the other.

## Evidence

- The mixed-version e2e file exists on main. Proof: The path list of the pack shows e2e/upgrade_mixed_test.go with exists: true.
- The Make target exists on main. Proof: The main facts of the pack list the make target mixed-version-test at Makefile:310.

## Fix boundary

- Add a secret round trip to the mixed-version test in e2e/upgrade_mixed_test.go: write a secret once, then read it through each backend.
- Send each read straight to the listener of one backend, with the public URL set as the public URL of both servers. The test then picks the backend, not a balancer.
- Assert the backend of each read by the listener that answered. The test fails when a backend serves no read.
- Keep the plaintext out of test output. Delete the secret at the end of the test.

Out of scope:

- CI matrix changes
- Audit of denied publishes
- Runner credential refusal
- Any server code change

## Check

- Write the failing test for a secret read through each backend with the same public URL in e2e/upgrade_mixed_test.go. Check: `make mixed-version-test` fails on main before the helper exists
- Write the failing test that fails when one backend serves no read in e2e/upgrade_mixed_test.go. Check: `make mixed-version-test` fails when the test sends both reads to one listener
- Wire the secret round trip into the existing mixed-version run in e2e/upgrade_mixed_test.go and delete the secret at the end. Check: `make mixed-version-test` passes and prints no plaintext
- Run the full gate. Confirmed by: CI (make check). Check: `make check` with zero findings

Depends on:

- `review-follow-ups-github-workflows-mixed-version-ci-coverage` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The make target mixed-version-test exists. (`Makefile:310 (make target mixed-version-test)`)
- A readSecrets helper exists in the s3bench tool. (`cmd/s3bench/state.go:97 (func readSecrets)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR govern this test? The pack lists no ADR for this item. The change adds only test code in e2e/upgrade_mixed_test.go and changes no secret behavior, so no ADR decision applies.
- How does the test know which backend served a read? The test opens a separate listener for each server and sends each read to that listener. No server signal is needed, so no server code changes.
- Can the test pass when both reads hit one backend? No. Each read goes to a chosen listener, and the test fails if a listener served no read.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
