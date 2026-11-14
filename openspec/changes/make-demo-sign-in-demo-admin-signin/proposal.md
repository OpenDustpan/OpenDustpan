---
version: 1
---

Issue: #2122.

## Problem

Developer opens the demo console and per-person pages refuse the bootstrap token because it is not a person and has no session.

## Evidence

- make demo exists as a Make target Proof: Makefile:357
- The demo seed writes a sign-in Proof: cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn)
- The server builds a console sign-in Proof: pkg/dustpan/signin.go:224 (func newConsoleSignIn)
- The issue reports that the demo signs in with the bootstrap admin token, which is not a person and has no session Proof: #2122 body: The demo signs in with the bootstrap admin token, which is not a person and has no session

## Fix boundary

- Demo seed creates person demo-admin with admin grants on both demo namespaces.
- Demo seed issues one one-time console sign-in for demo-admin and prints it instead of the bootstrap token.
- Demo seed issues one personal token for demo-admin for API use.

Out of scope:

- Seed of workspace, state versions, saved view, approval default, trigger, and notification sink (part 2)
- Console empty state for a missing approval default
- Change of sign-in, session, or token rules outside the demo seed

## Check

- Add failing test for demo-admin per-person routes in file cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn). Check: TestDemoAdminPerPersonPages fails on main, passes after the change.
- Seed demo-admin, its grants, sign-in, and token in file cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn). A person's step. Check: `make demo` prints the one-time console sign-in for demo-admin.
- Cover session read, refusals, and expiry for the demo identity in file pkg/dustpan/signin.go:224 (func newConsoleSignIn). A person's step. Check: GET /api/dustpan/v1/me/preferences with the demo personal token answers 200, and reuse plus expiry answers token.invalid.
- Wire the seed into file Makefile:357, document the demo sign-in, run usability check. A person's step. Check: `make demo` then console check of units, preferences, and access tokens pages shows no error banner.
- Run full check for file cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn). Confirmed by: CI (make check). Check: `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- make demo target exists (`Makefile:357`)
- Demo seed writes a sign-in (`cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn)`)
- Server builds a console sign-in (`pkg/dustpan/signin.go:224 (func newConsoleSignIn)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide how the demo signs in? The pack names no ADR (adrs: []). No decision text to quote. The change touches no numbered ADR decision; it reuses the existing demo seed constructor and the existing session and token ports without changing sign-in rules.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
