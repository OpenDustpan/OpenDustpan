---
version: 9
---

Issue: #1228, #1291.

## Why

**Persona: namespace administrator.** A namespace administrator publishes private providers for teams that run tofu init. Missing h1 hashes weaken install checks, and publisher-stated protocols let a wrong value reach OpenTofu, which then fails to start the provider.

The user path this change must make work:

1. **API.** Publish a provider release through the registry publish API with archive bytes and stated protocols.
2. **CLI.** Install the provider with tofu init against the registry URL.
3. **Docs page.** Read the publish page for h1 errors and protocol mismatch errors.
4. **E2e.** Run the registry e2e step with a mismatched protocol archive and an unsafe archive.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Unpack exceeds size, count, ratio, or path limits. | The publish failed the archive check with the NEW code registry.bad-archive and the failed limit. Fix the archive and publish again, then run tofu init to confirm the install path. | `tofu init` |
| Stated protocols differ from the manifest. | The publish failed with code registry.invalid and the stated value and the manifest value. Rebuild the publish request from the manifest and publish again, then run tofu init to confirm the install path. | `tofu init` |
| Republish of the same version with different H1. | The republish changed the archive with code registry.version-exists and the version. Publish a new version with the changed archive, then run tofu init to confirm the install path. | `tofu init` |

Evidence for the claims above:

- The registry domain has an Archive type used by the publish path. Proof: internal/registry/service/publish.go:307:	if !existing.SameContent(rel) {
- SameContent decides republish equality in the SQL adapters. Proof: pkg/store/sqlite/registry.go:137:		if relErr == nil && stored.SameContent(rel) {
- The publish path carries a protocols list from the request into stored releases. Proof: internal/api/registry/handlers.go:55:	Protocols []string      `json:"protocols"`

## What Changes

- Unpack each provider archive with limits on unpacked size, file count, ratio, and paths.
- Compute and store H1 on registry.Archive and count H1 in SameContent with a missing-H1 rule.
- Add a fourth file role `manifest` to the publish request: the release file `terraform-registry-manifest.json` (a separate release asset, as the public provider release tools publish it, listed in the SHA256SUMS file), and read it as the one source of protocols. Refuse a stated value that differs. The zip archive holds only the provider binary and is never read for a manifest (no fallback).
- Bound the manifest file: a size limit, JSON only, manifest version 1, protocol values 5.0 or 6.0 only, no empty list. Check it against the SHA256SUMS file when that file lists it.

Out of scope:

- The registry slug of each namespace (change `registry-namespace-slug`, which waits for this change)
- The route that shows the effective request and unpack limits (change `registry-effective-limits`, which waits for this change)
- Curated upstreams curation rules
- Upgrade pull requests
- Provider signature and checksum checks past the h1 addition
- Console catalog and create form

## Usability

Where the path still costs the person time or surprises them:

- Publish of a large provider fails at the unpack limit; the error names the failed limit and the exact repack step.
- A protocol mismatch surprises a publisher that hand-writes protocols; the error shows both stated and manifest values. A publisher that does not send the manifest file must state the protocols, and the publish page says so.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- SameContent decides republish equality. (`pkg/registry/registry.go:397`)
- Publish path compares existing release with SameContent. (`internal/registry/service/publish.go:307`)
- SQLite adapter compares stored release with SameContent. (`pkg/store/sqlite/registry.go:137`)
- Publish request carries protocols list. (`internal/api/registry/handlers.go:55`)
- Registrystore encodes archives for storage. (`pkg/store/internal/registrystore/registrystore.go:109`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

ADR 269 is touched (current rule: ADR 269, with ADR 345; ADR 345 changes decision 11 only; owner decisions 1 to 3). Decision 2 quotes the owner decision: "private providers and modules are uploaded through the API as the archive set of ADR 177. Checksums and the signature are checked at upload." This change keeps it: the manifest is one more file of the release, named by role like the checksums and the signature, and the checksums check covers it.

ADR 177 is touched (Changed by: ADR 257 extends the second open question; ADR 316 extends the OpenPGP library; current rule: this ADR, with ADR 257, 316). Decision 4 quotes the layout: "Next to them, `terraform-provider-<name>_<version>_SHA256SUMS` and its detached signature. This is the layout that a registry, a network mirror and a filesystem mirror of OpenTofu read." The zip holds the binary only; the manifest is a release file next to the zip, as the public release tools publish it. No owner-tagged decision changes.

## Open questions

none

Questions considered and answered:

- Where does the manifest come from, the zip or a release file? Public release tools (goreleaser with `extra_files`, the registry manifest convention) publish `terraform-registry-manifest.json` as a separate release asset, renamed `<name>_<version>_manifest.json`, and list it in SHA256SUMS; the zip holds only the provider binary. So the spec adds the role `manifest` and does not read the zip for a manifest. There is no fallback to a manifest inside the zip.
- One unpack routine or two? One bounded unpack serves the h1 hash. The manifest is read by a separate bounded reader with its own size limit.
- Count the H1 field in SameContent or in a separate check? Issue 1228 states an H1 field in registry.Archive that SameContent counts, so the spec counts H1 in SameContent. No tag shipped a release without H1 and no installation holds one, so there is no backfill of an earlier shape (no data, no tag): every stored release gets H1 at publish.
- Does an ADR answer the unpack limits or the protocol rule? The pack adrs list is empty and gives no ADR text, so no quoted decision line exists to answer the limit values or the protocol rule, and this spec states each rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
