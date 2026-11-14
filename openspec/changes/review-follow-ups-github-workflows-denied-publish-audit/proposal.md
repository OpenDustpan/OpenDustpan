---
version: 1
---

Issue: #2666.

## Problem

A namespace administrator reviews who tried to publish a module or provider and needs each denied publish in the namespace audit log with nothing stored.

## Evidence

- The registry publish path lives in internal/registry/service/module.go. Proof: internal/registry/service/module.go
- The issue asks to record denied module and provider archive publishes in the namespace audit log with no stored release or content blob. Proof: Review follow-ups: internal/registry/service/module.go, acceptance: Publish refusal tests must assert a denied audit entry and no stored release or content blob.

## Fix boundary

- Write one denied audit entry for each refused module and provider archive publish in internal/registry/service/module.go.
- Store no release record and no content blob for denied publishes.

Out of scope:

- CI jobs
- Secret backend verification
- Runner credential refusal

## Check

- Add failing module refusal test in NEW internal/registry/service/module_test.go for the refusal path in internal/registry/service/module.go Check: NEW TestDeniedModulePublishWritesAuditAndStoresNothing fails on main
- Add failing provider refusal test in NEW internal/registry/service/module_test.go for the same path Check: NEW TestDeniedProviderPublishWritesAuditAndStoresNothing fails on main
- Record denied publishes in the namespace audit log without storage in internal/registry/service/module.go Check: The two new tests pass
- Wire the audit write into the publish refusal path and check the audit read Check: `GET /api/audit/entries` with an access token shows the denied publish entry
- Confirm no release for a denied publish Check: `GET /api/dustpan/v1/namespaces/{namespace}/registry/modules` with an access token lists no denied version
- Run the full checks Check: `make check` with zero findings

Depends on:

- `review-follow-ups-github-workflows-secret-backend-verification` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main keeps a namespace audit path on the server. (`internal/api/server/server.go:411 (func keepsNamespaceAudit)`)
- Main builds the registry service from dustpan composition. (`pkg/dustpan/registry.go:60 (func newRegistryService)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph; a code check fails: a security change needs STRIDE threats for each trust boundary ("threats.items"); a code check fails: contract method PublishModuleArchive: no requirement or scenario names it; a code check fails: and 1 more.

Questions considered and answered:

- Where must a denied module or provider publish be recorded? No ADR in the pack touches registry publishes. The issue text sets the boundary: write one denied entry to the namespace audit log and store no release record and no blob.
- What happens when the denied audit write fails? No ADR in the pack answers it. The change keeps the current refusal error and keeps the publish refused when the audit write fails.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
