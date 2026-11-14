---
version: 2
---

Issue: #1228, #1291.

## Why

**Persona: namespace administrator.** A namespace administrator publishes private providers for teams that run tofu init. Unsafe namespace names break registry URLs, missing h1 hashes weaken install checks, and publisher-stated protocols let a wrong value reach OpenTofu, which then fails to start the provider.

The user path this change must make work:

1. **API.** Publish a provider release through the registry publish API with archive bytes and stated protocols.
2. **CLI.** Install the provider with tofu init against the registry URL under the namespace slug.
3. **Docs page.** Read the publish page for slug rules, h1 errors, and protocol mismatch errors.
4. **E2e.** Run the registry e2e step with an unsafe namespace name and a mismatched protocol archive.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Unpack exceeds size, count, ratio, or path limits. | The publish failed the archive check with code registry.bad-archive and the failed limit. Fix the archive and publish again, then run tofu init to confirm the install path. | `tofu init` |
| Stated protocols differ from the manifest. | The publish failed with code registry.protocol-mismatch and the stated value and the manifest value. Rebuild the publish request from the manifest and publish again, then run tofu init to confirm the install path. | `tofu init` |
| Republish of the same version with different H1. | The republish changed the archive with code registry.content-changed and the version. Publish a new version with the changed archive, then run tofu init to confirm the install path. | `tofu init` |
| Install requests an unknown namespace slug. | The registry path uses the namespace slug with code registry.unknown-slug and the requested slug. List releases to copy the slug, then run tofu init to confirm the install path. | `tofu init` |

Evidence for the claims above:

- The registry domain has an Archive type used by the publish path. Proof: internal/registry/service/publish.go:307:	if !existing.SameContent(rel) {
- SameContent decides republish equality in the SQL adapters. Proof: pkg/store/sqlite/registry.go:137:		if relErr == nil && stored.SameContent(rel) {
- The publish path carries a protocols list from the request into stored releases. Proof: internal/api/registry/handlers.go:55:	Protocols []string      `json:"protocols"`

## What Changes

- Unpack each provider archive with limits on unpacked size, file count, ratio, and paths.
- Compute and store H1 on registry.Archive and count H1 in SameContent with a missing-H1 rule.
- Read terraform-registry-manifest.json from the archive as the source of protocols and refuse a stated value that differs.
- Derive and serve a registry-safe slug for each namespace for publish and install paths with dual serve of prior paths.

Out of scope:

- Curated upstreams curation rules
- Upgrade pull requests
- Provider signature and checksum checks past the h1 addition
- Console catalog and create form

## Usability

Where the path still costs the person time or surprises them:

- Publish of a large provider fails at the unpack limit; the error names the failed limit and the exact repack step.
- A protocol mismatch surprises a publisher that hand-writes protocols; the error shows both stated and manifest values.
- A namespace with an unsafe name surprises users when the registry URL differs from the display name; the API shows the slug as the effective value.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- SameContent decides republish equality. (`pkg/registry/registry.go:397`)
- Publish path compares existing release with SameContent. (`internal/registry/service/publish.go:307`)
- SQLite adapter compares stored release with SameContent. (`pkg/store/sqlite/registry.go:137`)
- Publish request carries protocols list. (`internal/api/registry/handlers.go:55`)
- Registrystore encodes archives for storage. (`pkg/store/internal/registrystore/registrystore.go:109`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Use one unpack routine for the h1 hash and the protocol manifest or two routines? The pack lists issue 1291 that the same bounded unpack serves both the h1 hash and the manifest read, so the spec uses one unpack routine for both.
- Count the H1 field in SameContent or in a separate check? Issue 1228 states an H1 field in registry.Archive that SameContent counts, so the spec counts H1 in SameContent with a missing-H1 backfill rule.
- Does an ADR answer the unpack limits or the slug derivation rule? The pack adrs list is empty and gives no ADR text, so no quoted decision line exists to answer the limit values, the slug rule, or the protocol rule, and this spec states each rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
