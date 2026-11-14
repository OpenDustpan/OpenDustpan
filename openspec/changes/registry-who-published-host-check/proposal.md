---
version: 1
---

Issue: #1294.

## Problem

As a namespace administrator I publish team providers and must show who published each version and that the mirror answers only on our host.

## Evidence

- Issue 1294 states PublishedBy and AddedBy hold the subject of the principal, not the principal ID. Proof: `Release.PublishedBy` and `KeyRecord.AddedBy` hold the subject of the principal, not the principal ID.
- Issue 1294 states the registry host check compares the Host header with --public-url and that RFC 101 section C says never to use the Host header. Proof: The host check of the registry protocol compares the `Host` header of the request with the host of `--public-url`.
- Registry key and provider version routes exist on main. Proof: GET/POST /api/dustpan/v1/namespaces/{}/registry/keys and GET/PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{} in route table

## Fix boundary

- Keep Release.PublishedBy and KeyRecord.AddedBy subject strings and store the publisher and adder principal IDs beside them, empty for bootstrap callers, with no backfill of old rows.
- Stop reading the request Host header for the mirror; build mirror URLs only from the configured public URL base including scheme, host, and port.
- Normalize the configured host to ASCII lowercase with startup validation, and document that folding as the meaning of exact.

Out of scope:

- Registry limits of RFC 101 section H
- C# and Java API clients
- GitLab forge adapter

## Check

- Add failing domain test for publisher ID beside subject on PublishRelease and AddKey in NEW file pkg/registry/publisher_test.go, with bootstrap, removed, and recreated principal cases Check: TestRegistryPublisherStoresIDBesideSubject in pkg/registry fails on main, passes after
- Add failing handler test in NEW file pkg/dustpan/registry_host_test.go that the mirror builds URLs from the configured base with mixed-case and alias Host values and writes no audit entry Check: TestRegistryMirrorIgnoresHostHeader fails on main, passes after
- Wire publisher ID beside subject through service, store, and REST versions route Check: GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{} with access token answers publisher ID and subject, including bootstrap empty-ID case
- Wire adder ID beside subject through service, store, and REST keys route Check: GET /api/dustpan/v1/namespaces/{}/registry/keys with access token answers adder ID and subject
- Update baselines with the new nullable publisher and adder ID columns Check: Edit the single baseline of each dialect in place; dialect store tests pass
- Add e2e step for the mirror read on the configured base with no credential Check: GET /api/dustpan/v1/namespaces/{}/registry/providers with no credential answers the mirror index built from the configured base
- Write docs page for who published and the configured mirror host Check: Docs page runs each curl as written from the user path
- Run usability check and full check Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- pkg/registry holds registry types with ValidFingerprint. (`pkg/registry/keys.go:40 (func ValidFingerprint)`)
- Registry key write check exists in the postgres store. (`pkg/store/postgres/registry_keys.go:37 (func checkRegistryKeyWrite)`)
- Registry protocol constructor exists in the server SDK. (`pkg/dustpan/registry.go:137 (func newRegistryProtocol)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should PublishedBy and AddedBy hold subject or principal ID? No ADR decides the publisher identity shape. RFC 101 is discussing, not a decision. The change keeps the existing subject string and stores the stable principal ID beside it with no backfill of old rows.
- May the registry host check use the Host header? No ADR decides the registry host check. The change removes the Host header from routing and URL building and serves the mirror from the configured public URL base only.
- Does the new stored ID column need a numbered migration? The schema has one baseline migration per dialect until the first tag. The change edits each baseline in place with nullable ID columns, with no numbered migration and no data conversion.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
