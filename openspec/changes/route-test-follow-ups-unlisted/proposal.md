---
version: 1
---

Issue: #2380.

## Problem

The Embedder who maintains the routes runs the cross-adapter probe. The probe misses unlisted paths, passes 400 or 404 as safe, and its comment overstates what it covers, so a skipped sign-in can pass unseen.

## Evidence

- Main has the gated handler constructor and the no-credential probe test. Proof: pkg/dustpan/dustpan.go:1958 (func Server.Handler) and pkg/dustpan/route_actions_test.go:458 (TestNoRouteAnswersWithoutACredential)
- Main refuses a handler that hides its routes from the API description. Proof: pkg/dustpan/apidesc_internal_test.go:62 (TestDescribeOperationsRefusesAHandlerThatHidesItsRoutes)
- UngatedHandler has no non-test user on main, so the compared method is new test-support work. Proof: rg -n -F --glob '!*_test.go' 'UngatedHandler' pkg internal cmd matches 0

## Fix boundary

- Probe the NEW fixed allowlist of unlisted test probe paths for identical non-2xx answers from Handler and the NEW UngatedHandler.
- Assert the exact unauthenticated answer per route instead of only non-2xx.
- Fix the probe doc comment to state exact coverage.
- Remove duplicated per-adapter public route lists once covered.

Out of scope:

- Changes to route authorization or public route classification
- E2e coverage of tool CLIs

## Check

- In pkg/dustpan/dustpan.go add the NEW Server.UngatedHandler test-support constructor next to Server.Handler that returns the same mux without the credential gate. Check: `go test ./pkg/dustpan -run TestUnlistedPathsMatchGatedAndUngated -count=1` fails on main and passes after, proving the NEW Server.UngatedHandler exists.
- In pkg/dustpan/route_actions_test.go add the NEW test TestUnlistedPathsMatchGatedAndUngated that requests the fixed allowlist with no redirect and no public 2xx through Server.Handler and the NEW Server.UngatedHandler on a started server with all components enabled and requires identical non-2xx status and code. Check: The NEW TestUnlistedPathsMatchGatedAndUngated in pkg/dustpan/route_actions_test.go fails on main and passes after.
- In pkg/dustpan/route_actions_test.go extend the probe with one exact-answer table per route family and fail any 400 or 404 that is not the table value. Check: TestNoRouteAnswersWithoutACredential in pkg/dustpan/route_actions_test.go fails with a 400 probe and passes with the table.
- In pkg/dustpan/route_actions_test.go rewrite the probe doc comment to name the covered handler chain and the skipped misuse reporter, request cache, request log, and component gate. Check: `rg -n -F 'whole handler chain' pkg/dustpan` returns no matches and the new comment lists covered and skipped layers.
- In pkg/dustpan/route_actions_test.go show coverage by mutating one public entry and one exact-answer table value and requiring the probe to fail. Check: Temporarily remove one public entry from the table in pkg/dustpan/route_actions_test.go and `go test ./pkg/dustpan -run TestNoRouteAnswersWithoutACredential -count=1` fails, then restore it and it passes.
- In pkg/dustpan/route_actions_test.go show positive control with a temporary unlisted middleware path that makes Handler and the NEW Server.UngatedHandler differ. Check: Add a temporary middleware in pkg/dustpan/route_actions_test.go that serves one unlisted path and the NEW TestUnlistedPathsMatchGatedAndUngated fails, then remove it and it passes.
- In internal/api audit, inventory, pulumi, server, and tfe routes_test.go files delete publicPatterns only after the tightened probe and both proof tasks pass on the same tree. Check: `rg -n -F publicPatterns internal/api` returns no matches and the probe passes on the same tree.
- Run the route probe tests. Check: `go test ./pkg/dustpan -run 'TestNoRouteAnswersWithoutACredential|TestUnlistedPathsMatchGatedAndUngated' -count=1` passes.

Depends on:

- small-hardening-26-oct-architecture-route-guard-enforcement

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main provides Server.Handler for the full gated handler. (`pkg/dustpan/dustpan.go:1958 (func Server.Handler)`)
- Main probes that no route answers without a credential. (`pkg/dustpan/route_actions_test.go:458 (func TestNoRouteAnswersWithoutACredential)`)
- Main refuses a handler that hides its routes from the API description. (`pkg/dustpan/apidesc_internal_test.go:62 (func TestDescribeOperationsRefusesAHandlerThatHidesItsRoutes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should the probe keep the 2xx check or assert the exact unauthenticated answer? Replace it. The probe asserts one exact unauthenticated answer per route family from the per-family table: 401 with code auth.unauthenticated for tool-neutral routes and each tool adapter error shape for Pulumi and TFE routes. Any 2xx is a failure and any other status is a failure against the table. The doc comment states that the exact-answer check replaces the bare 2xx check.
- Should per-adapter publicPatterns stay after the new test? Remove them. The single public-route table next to the probe in pkg/dustpan/route_actions_test.go stays the one place. Per-adapter publicPatterns lists are deleted only after the tightened probe passes and the coverage-proof mutation task shows the probe fails when a public entry is removed.
- Does the unlisted-path comparison check status only or status and body? Status and error code. Bodies may carry request IDs, so the comparison checks status plus the stable error code field, not the full body bytes. Redirects are out of scope: the allowlist holds only paths with no redirect and no public 2xx.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
