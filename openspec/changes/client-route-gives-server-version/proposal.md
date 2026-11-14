---
version: 1
---

Issue: #1230.

## Problem

An agent with a provider token must check its version pin before other calls. Today the server version needs server:observe and capabilities gives apiVersion only, so a least-privilege token cannot read both versions.

## Evidence

- Server version today needs server:observe via upgrade-check. Proof: internal/api/server/upgrade.go:46 (func Handler.serveUpgradeCheck) and TestUpgradeCheckDeclaresServerObserve in internal/api/server/upgrade_test.go:52
- Capabilities route exists and gives apiVersion v1 only. Proof: ANY/GET /api/dustpan/v1/capabilities in route table and internal/api/apidesc/openapi.json:415: "apiVersion"
- Document version is info.version of served description. Proof: internal/api/apidesc/document.go:15 and internal/api/apidesc/read.go:100

## Fix boundary

- Add serverVersion and descriptionVersion to GET /api/dustpan/v1/capabilities for signed-in callers.
- Keep anonymous access refused; keep server:observe on upgrade-check unchanged.
- Update user docs page in same pull request.

Out of scope:

- RFC 48 version pin comparison rule
- Provider and CLI pin enforcement
- Public version endpoint

## Check

- In NEW file internal/api/server/capabilities_versions_test.go add failing server test that calls GET /api/dustpan/v1/capabilities with access token holding caller:read and only collection actions, with no credential, and with a revoked credential. Check: `go test ./internal/api/server/ -run TestCapabilitiesVersions -count=1` fails on main
- In NEW file e2e/capabilities_versions_test.go add e2e step that reads GET /api/dustpan/v1/capabilities with access token against real binary for release build with ldflags version, dev build default, and describe form. Check: `go test ./e2e/ -run TestCapabilitiesVersions -count=1` fails on main
- Extend capabilities handler at internal/api/pulumi/pulumi.go:317 (func Handler.capabilities) to serve GET /api/dustpan/v1/capabilities with access token and add NEW fields serverVersion and descriptionVersion to the openapi.json capabilities entry after let-lock-in-static-settings lands. Check: `go test ./internal/api/... -run TestCapabilities -count=1`
- Declare authorization action caller:read on installation for GET /api/dustpan/v1/capabilities and keep 401 for anonymous with access token gate; prove collection-only token passes and token without caller:read gets 403. Check: Route test passes with declared caller:read on installation for GET /api/dustpan/v1/capabilities
- Regenerate API clients from updated description and keep Pulumi CLI contract safe. Check: `make clients-check` passes after regeneration
- Update user docs page for version check in same pull request. Check: Docs page renders and `make docs-lint` passes for changed files
- Run full checks with GET /api/dustpan/v1/capabilities using access token, e2e step against real CLI, docs page, usability check, and latency check of 95th percentile under 200 ms in-process. Check: `make check` with zero findings

Depends on:

- let-lock-in-static-settings
- `let-lock-in-static-settings` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Capabilities handler serves GET /api/dustpan/v1/capabilities. (`internal/api/pulumi/pulumi.go:317 (func Handler.capabilities)`)
- Upgrade check handler serves server version behind server:observe. (`internal/api/server/upgrade.go:46 (func Handler.serveUpgradeCheck)`)
- CLI client reads upgrade-check for server version. (`internal/cli/client/upgrade_check.go:65 (func UpgradeCheck)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Effect: Change adds client-visible fields to capabilities, so pull request updates user docs page.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Public or signed-in route for versions? Signed-in only on GET /api/dustpan/v1/capabilities. Benchmark go-tfe public ping leaks version to attackers; client needs version only after it holds token. Every signed-in caller holds caller:read, so a collection-only token passes without server:observe.
- New route or extend capabilities? Extend GET /api/dustpan/v1/capabilities with two fields. No new route. The route is the tool-neutral capabilities entry already in internal/api/apidesc/openapi.json; extra string fields are backward compatible and the real CLI ignores unknown JSON fields, checked by make e2e.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
