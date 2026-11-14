---
version: 1
---

Issue: #2603.

## Problem

Developer runs namespace create, stack list, passkey setup, and server start and gets errors that do not name the missing value, so the developer cannot fix the call without extra search.

## Evidence

- Namespace create lives in the CLI on main. Proof: internal/cli/namespace_create.go:5 (func NamespaceCreateCommand)
- Passkey routes live in the server handler on main. Proof: internal/api/server/passkeys.go:184 (func Handler.passkeyRoutes)
- The issue reports all four errors on main e942f91b8. Proof: Issue #2603 body lists namespace create, stack ls URLs, passkey 404, and plan retention errors

## Fix boundary

- `opendustpan namespace create` with no name says the name is missing and shows the fix.
- Stack list shows no console URL when no console is configured.
- Passkey routes name `--public-url` when it is missing.
- Empty `--plan-retention` parse error names the flag with its default value and format.

Out of scope:

- Other missing-setting errors outside issue #2603
- Console URL configuration beyond omitting the wrong stack URL

## Check

- Write a failing test for namespace create with no name in internal/cli. Confirmed by: CI (go test ./internal/cli/). Check: go test ./internal/cli/ fails on main, passes after the fix.
- Write a failing test in internal/api/server that calls POST /api/dustpan/v1/sign-in/passkey/begin behind Handler.passkeyRoutes with a session credential; BLOCKED by gate not named in the pack. Confirmed by: CI (go test ./internal/api/server/). Check: go test ./internal/api/server/ fails on main: the 404 body names no `--public-url`.
- Write a failing test in cmd/opendustpan for empty, malformed, and negative `--plan-retention` at server start. Confirmed by: CI (go test ./cmd/opendustpan/). Check: go test ./cmd/opendustpan/ fails on main: the error names no default and no format.
- Fix the four errors at their sources; the routes keep their authorization actions. Confirmed by: CI (go test ./internal/cli/ ./internal/api/server/). Check: The four tests above pass.
- Add an e2e step that runs the real Pulumi CLI `pulumi stack ls` with an access token against a dev server with no console and checks that no console URL shows; BLOCKED by Pulumi stack-list route not named in the pack. Confirmed by: CI (make e2e). Check: The docs commands run as written in the e2e step.
- Update the docs page for the four errors and check the person path from the docs page alone against GET /api/dustpan/v1/settings with a session credential; BLOCKED by gate not named in the pack. A person's step. Check: The docs commands run as written.
- Run the full check. Confirmed by: CI (make check). Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a namespace create CLI command. (`internal/cli/namespace_create.go:5 (func NamespaceCreateCommand)`)
- Main has passkey routes on the server handler. (`internal/api/server/passkeys.go:184 (func Handler.passkeyRoutes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR fix the wording of these errors? No ADR in the pack decides error wording. The fix keeps routes and flags unchanged and only names the missing value and the fix.
- Which errors does this change fix? The four errors of issue #2603. Other missing-value errors stay as they are.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
