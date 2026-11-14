---
version: 1
---

## Problem

An Embedder builds a server binary with the SDK. Exported pkg packages hold service and adapter code that belongs in internal. The move keeps the SDK surface to ports and constructors.

## Evidence

- NamespaceServiceGuard is defined in pkg/principal on main Proof: pkg/principal/namespace_service_guard.go:12
- pkg/forge/github exists on main Proof: pkg/forge/github (pack paths: exists true)
- The mattermost and matrix renderers exist on main Proof: pkg/notify/chat/mattermost and pkg/notify/chat/matrix (pack main list)
- The teams renderer has a test on main Proof: pkg/notify/chat/teams/teams_test.go:512 (TestMaxBodyIsUnderTheLimitOfTeams)

## Fix boundary

- Move NamespaceServiceGuard to the NEW package internal/principalsvc
- Move pkg/forge/github to the NEW package internal/forgegithub
- Move the teams, mattermost and matrix renderers to NEW packages under internal/notify
- Add a NEW pkg/dustpan constructor for each moved adapter
- Add all NEW internal packages of this change to the depguard domain rule in .golangci.yml

Out of scope:

- Behavior change of the principal guard, the forge adapter or the renderers
- The other service moves of the split

## Check

- Write a test that builds each NEW pkg/dustpan constructor and gets a working adapter. Check: `go test ./pkg/dustpan/...` fails on main
- Move NamespaceServiceGuard to internal/principalsvc and update the callers Check: `go build ./...` passes and `rg -n 'principal\.NamespaceServiceGuard'` has no output
- Move pkg/forge/github to internal/forgegithub and the three renderers to NEW packages under internal/notify Check: `go build ./...` passes and `rg -n 'pkg/forge/github|pkg/notify/chat/(teams|mattermost|matrix)'` has no output in code, docs and config
- Add the NEW pkg/dustpan constructors and wire them in the server Check: `go test ./pkg/dustpan/...` passes
- Add the NEW internal packages to the depguard domain rule in .golangci.yml. Run `make check`. Confirmed by: the driver's gates. Check: `make check` with zero findings
- Run the e2e step with the real CLI. The CLI calls the existing route GET /api/dustpan/v1/capabilities with a session credential. The route passes the sign-in gate of the server, and the moved adapters are wired. Confirmed by: the driver's gates. Check: `make e2e` passes. Credential kind: session.

Depends on:

- `new-services-adapters-land-in-gate-internal` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines NamespaceServiceGuard in the exported principal package (`pkg/principal/namespace_service_guard.go:12 (type NamespaceServiceGuard)`)
- Main keeps a test of the teams renderer in the exported notify tree (`pkg/notify/chat/teams/teams_test.go:512 (func TestMaxBodyIsUnderTheLimitOfTeams)`)
- Main keeps a test of the matrix renderer in the exported notify tree (`pkg/notify/chat/matrix/matrix_test.go:109 (func TestMatrixNeedsAnAccessToken)`)
- Main wires the server through the dustpan package (`pkg/dustpan/devguard_internal_test.go:36 (func TestWireDevGuard)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the owner decision on the default collection of a new namespace apply? No. This change is a code move and creates no namespace. Owner decision, 13 Nov 2026 (queue P5) is not touched.
- Is an ADR touched? The pack lists no ADR for this item. The move changes no decision. It keeps the rule that domain packages never import adapters, in the depguard domain rule.
- How are Embedders told about the removed paths? The build fails at the old import path. Before the first tag the SDK may break. The doc comment of each NEW constructor names the replacement.
- How is an import cycle avoided? The NEW internal packages import only the ports in pkg/principal, pkg/forge and pkg/notify. pkg/dustpan imports them, and they never import pkg/dustpan. `go build ./...` fails on a cycle.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
