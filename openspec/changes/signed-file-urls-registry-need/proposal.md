---
version: 4
---

Issue: #1288.

## Why

**Persona: operator.** An operator runs two opendustpan serve instances of one installation behind a load balancer on one PostgreSQL database and one object store. Today tofu init can fail because the signed file URL that one instance made fails on the other instance. The operator must pin the registry to one instance.

The user path this change must make work:

1. **CLI.** Run tofu init against the load balancer address so requests reach any instance.
2. **Docs page.** Read the several-instances guide page for the 360 second maximum URL validity and the shared key note.
3. **API.** List provider versions and fetch the signed file URL through the API on another instance.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Database outage during the sign step leaves the file fetch without a URL. | 503 with Retry-After: 5 in the JSON:API error shape of the TFE-compatible API. Run tofu init again after the database returns. | `GET /health/ready` |
| An altered, expired or foreign-installation signed URL fails on any instance. | registry.signature-invalid with HTTP 403 and no file bytes. | `tofu init -upgrade` |

Evidence for the claims above:

- The registry protocol uses a per-instance HMAC key set through dustpan.WithRegistryURLKey with no binary flag. Proof: pkg/dustpan/registry.go:47 (func WithRegistryURLKey); issue #1288 body: A URL that one instance made fails on another instance
- Main already has the error code for a bad signature. Proof: internal/errcode/catalog_registry.go:25 registry.signature-invalid

## What Changes

- Derive the registry file URL HMAC key from the stored key-encryption key plus the stable installation database ID on every instance.
- Verify a signed URL on any instance with a 300 second expiry plus 60 second skew, a v=1 version field, and the registry.signature-invalid refusal.
- Remove the per-instance random key from the server start path; keep dustpan.WithRegistryURLKey for tests and embedders only.

Out of scope:

- Rotation UI beyond a key-encryption key change.
- Module file URLs; only provider file URLs are signed today.
- Cross-installation forwarding and replication.

Depends on:

- database-outage-503-shape

## Usability

Where the path still costs the person time or surprises them:

- tofu init works the first time behind a load balancer with no extra flag.
- An expired URL costs one retry: run tofu init -upgrade.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main provides dustpan.WithRegistryURLKey to override the registry URL key. (`pkg/dustpan/registry.go:47 (func WithRegistryURLKey)`)
- Main builds the registry protocol through newRegistryProtocol. (`pkg/dustpan/registry.go:137 (func newRegistryProtocol)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 5 (extended).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "Scope: one installation, active-active (owner). Milestone 7 makes one installation run active-active: two or more `opendustpan serve` instances behind one load balancer, on one PostgreSQL database and one S3-compatible object store."
- Effect: Signed registry file URLs verify on every instance of one installation.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1, 2).

- Decisions that apply: 1 (changed), 2 (current).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "No migration or compatibility code for untagged shapes (owner). Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: The change adds no table, no column and no migration.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Can each instance keep its own random signing key? No. ADR 280 decision 1 requires that any instance serves the installation.
- Where does the shared key live? Derived from the stored key-encryption key. ADR 322 decisions 1 and 2 forbid schema work before the first tag.
- How does rotation work? A key-encryption key change ends in-flight URLs within 360 seconds. The client asks for a fresh URL.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
