---
version: 2
---

Roadmap: `docs/roadmap.md:379` (Serve private modules through the module protocol.).

## Why

**Persona: developer.** Developer runs tofu init against the server and fetches a private module from the namespace registry, without reads from other namespaces.

The user path this change must make work:

1. **API.** Platform team publishes a private module version through the registry publish API.
2. **CLI.** Developer points the module source at the server host and namespace address and runs init.
3. **Docs page.** Developer reads the private modules page for the address form and errors.
4. **E2e.** e2e suite runs tofu init against the real binary and fetches the private module.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Module version missing or not in a downloadable state | Server answers 404 with error code registry.not-found. Publish the version through the registry publish API, then run `tofu init` again. | `tofu init` |
| Caller without read grant on namespace registry | Server answers 404 with error code registry.not-found. Ask the namespace administrator for read access, then run `tofu init` again. | `tofu init` |
| Module address names an unknown host or a name outside the registry-safe subset | Server answers 404 with error code registry.not-found. Fix the source address from the docs page, then run `tofu init` again. | `tofu init` |

Evidence for the claims above:

- Providers and modules ship through the tools own protocols and OCI stays out of scope. Proof: docs/adr/0257-the-tools-own-registry-protocols-first-no-oci-endpoint.md decision 1 and decision 2
- Private modules are uploaded through the API with checks at upload, upload comes before git tags, and namespaces never read another namespace. Proof: docs/adr/0269-private-providers-and-modules-are-uploaded-as-the-archive-set.md decisions 2, 6, and 8
- The module routes exist on main under the registry path. Proof: Pack main list: route GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions and GET/PUT /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions/{}

## What Changes

- Publish module versions through the reused registry publish API shape with archive checks at upload.
- Serve discovery with modules.v1 and published module versions through the module registry protocol in the caller namespace.
- Serve module download sources for downloadable states only with caller auth and without added signature.
- Refuse cross-namespace reads, wrong hosts, bad names, and missing or blocked versions with stable error codes and audit entries.

Out of scope:

- Pulumi plugins
- Yank and revoke actions of module versions
- Registry read commands of the CLI
- Git tag sources for modules
- Curated upstreams
- Provider serving

Depends on:

- `artifact-registry-private-provider` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Developer must learn the private module source address form from the docs page alone.
- Version list errors must name the module and the one publish command that fixes the miss.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Registry already defines module archive refused error code used at module upload. (`internal/errcode/catalog_registry.go:17`)
- Registry already defines signature invalid error code used at upload. (`internal/errcode/catalog_registry.go:25`)
- Registry already defines version exists error code used for duplicate publish. (`internal/errcode/catalog_registry.go:31`)
- Registry already defines not-staged error code used when staging key is missing. (`internal/errcode/catalog_registry.go:23`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The tools' own protocols. When OpenDustpan serves providers and modules, it serves them through the HTTP protocols of the tools: the provider network mirror and the module registry protocol for OpenTofu, the provider registry protocol for providers that a team publishes under the host of the server, and the plugin download URL for the Pulumi CLI. RFC 10 and RFC 93 keep the order of these, the curation and the routes."
- Quote: "No OCI endpoint now. OpenDustpan serves no OCI Distribution API. It is not an OCI registry."
- Effect: Change serves private modules through the module registry protocol for OpenTofu. It adds no OCI endpoint.
- Conflict: none.

**ADR 269: Private providers and modules: own providers first, uploaded as the archive set** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 6 (current), 7 (current), 8 (current), 9 (current).
- Changed by: ADR 345 changes decision 11. Chain to the end: ADR 345.
- Current rule: this ADR, with ADR 345.
- Quote: "Own and team providers first. Issue 949, "Decided", items 10.1 and 10.2 (owner, 2026-10-18):  > own and team providers first, through the OpenTofu provider network > mirror protocol. An upstream proxy comes later."
- Quote: "An upload through the API. Issue 949, "Decided", item 10.4:  > private providers and modules are uploaded through the API as the > archive set of ADR 177. Checksums and the signature are checked at > upload."
- Quote: "No cross-namespace reads. A namespace never reads the registry of another namespace (question 4)."
- Quote: "Module sources. Upload first, git tags second (question 7)."
- Quote: "Trusted keys. Each namespace registers its own signing keys (question 8)."
- Effect: Change uploads modules through the API as the archive set with checks at upload, serves only the caller namespace, refuses other hosts by exact match and registry-safe names, uses per-namespace signing keys, and leaves git tags and curation out of scope. No owner decision words change.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Through which protocol are private modules served? ADR 257 decision 1 answers it: serve modules through the module registry protocol for OpenTofu. No OCI endpoint.
- Does this change add git tag sources for modules? ADR 269 decisions 2 and 8 answer it: upload through the API as the archive set comes first with checks at upload; git tags come second and stay out of scope.
- Does a namespace read modules from another namespace? ADR 269 decision 6 answers it: no; each request reads only the registry of its own namespace.
- Which download wire shape does the version list use? No owner or ADR rule answers the exact download wire shape, so the change uses the module registry protocol version list with X-Terraform-Get download source, consistent with ADR 257 decision 1.
- How are wrong hosts and malformed names handled? ADR 269 decision 4 answers it: the adapter refuses every other host with exact comparison, and only registry-safe names publish.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
