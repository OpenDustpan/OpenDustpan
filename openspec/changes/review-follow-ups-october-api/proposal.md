---
version: 1
---

Issue: #2705.

## Problem

A developer uses the API to list releases, connect a git host, and recover from 400 and 403 errors. The list hides served state, the connection request accepts fields it must refuse, error text leaks wrapper prefixes, audit entries miss the connection ID, and the 403 lacks a usable next step.

## Evidence

- The registry handler path exists on main. Proof: internal/api/registry/handlers.go
- The git-host connection handler paths exist on main. Proof: internal/api/server/githost_connections.go
- The git-host handler path exists on main. Proof: internal/api/server/githost.go
- The Pulumi freshness handler path exists on main. Proof: internal/api/pulumi/freshness.go
- The API description schema paths exist on main. Proof: internal/api/apidesc/schema.go

## Fix boundary

- Expose served boolean per release in management versions list and retain unserved releases
- Refuse operator-owned fields by presence including null values
- Return public forge invalid reason without wrapper prefixes
- Carry target connection ID into refused-operation audit entries
- Include usable sign-in start path in reauthenticate field on 403
- Apply nullable rule to nil maps without omitempty
- Refuse dustpan required on fields promoted through embedded pointers

Out of scope:

- Items outside issue #2705
- Console, e2e, and docs beyond the named handler tests

## Check

- Add NEW test TestServedIndicator in internal/api/registry for served before and after signing-key removal Check: `go test ./internal/api/registry -run TestServedIndicator` fails on main
- Add NEW test TestOperatorFieldPresence in internal/api/server for each operator-owned field Check: `go test ./internal/api/server -run TestOperatorFieldPresence` fails on main
- Add NEW test TestForgeInvalidHidesWrappers in internal/api/server Check: `go test ./internal/api/server -run TestForgeInvalidHidesWrappers` fails on main
- Add NEW test TestRefusedOperationCarriesConnectionId in internal/api/server Check: `go test ./internal/api/server -run TestRefusedOperationCarriesConnectionId` fails on main
- Add NEW test TestFreshAuthIncludesSignInPath in internal/api/pulumi Check: `go test ./internal/api/pulumi -run TestFreshAuthIncludesSignInPath` fails on main
- Add NEW test TestNilMapNullableRule in internal/api/apidesc Check: `go test ./internal/api/apidesc -run TestNilMapNullableRule` fails on main
- Add NEW test TestEmbeddedPointerRequiredRefused in internal/api/apidesc Check: `go test ./internal/api/apidesc -run TestEmbeddedPointerRequiredRefused` fails on main
- Expose served boolean and retain unserved releases in internal/api/registry/handlers.go Check: TestServedIndicator passes

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main lists git-host connections through listGitHostConnections. (`internal/api/server/githost_connections.go:172 (func Handler.listGitHostConnections)`)
- Generated Go client tracks field presence. (`internal/clientgen/golang/golang.go:581 (func fieldPresence)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR decide the served indicator, field presence, error text, audit ID, sign-in path, or schema rule? No ADR text in the pack decides these shapes. The pack lists no ADRs. Each fix keeps its route, status, error code, and namespace scope.
- Do the fixes preserve existing contracts and scopes? Yes. Releases stay listed, 400 and 403 codes stay, namespace scope stays, and stale-session 401 behavior stays. Null stays present for generated clients.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
