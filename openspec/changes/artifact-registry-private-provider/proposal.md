---
version: 2
---

Roadmap: `docs/roadmap.md:379` (Serve private providers with signature and checksum checks.).

## Why

**Persona: developer.** A Developer runs `tofu init` against the server and fetches a private provider from the namespace registry. Today a bad signature or a bad checksum has no defined refusal, and per-namespace trust has no path, so teams cannot rely on private providers in plans and applies.

The user path this change must make work:

1. **API.** A namespace administrator registers a signing key and publishes the provider archive set through the tool-neutral REST API.
2. **CLI.** A Developer runs `tofu init` against the server host and fetches the private provider through the provider mirror.
3. **Docs page.** A Developer reads the registry providers docs page to publish, trust keys, and fix bad signature or bad checksum errors.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Upload with an input that the address rule does not allow. | The server answers 400 with a body that names the input. List the published providers with `GET /api/dustpan/v1/namespaces/{id}/registry/providers`. | `GET /api/dustpan/v1/namespaces/{id}/registry/providers` |
| Upload with a bad checksum, a missing signature, or a bad signature. | The server answers 422 with a body that names the bad file or key. List the signing keys with `GET /api/dustpan/v1/namespaces/{id}/registry/keys`. | `GET /api/dustpan/v1/namespaces/{id}/registry/keys` |
| `tofu init` for an unknown address or version. | The server answers 404 with a body that names the address and version. List the versions with `GET /api/dustpan/v1/namespaces/{id}/registry/providers/{addr}/versions`. | `GET /api/dustpan/v1/namespaces/{id}/registry/providers/{addr}/versions` |

Evidence for the claims above:

- The milestone step requires tofu init to fetch a private provider and refuses a provider with a bad signature or a bad checksum. Proof: Roadmap step: `tofu init` gets a private provider and a private module from the registry (ADR 257, ADR 269). The Pulumi CLI gets a resource plugin from it. A provider with a bad signature or a bad checksum is refused.
- This part covers provider archives and namespace signing keys and leaves modules, Pulumi plugins, yank, revoke, and CLI reads to later parts. Proof: Roadmap scope: This part publishes provider archives through the API and registers namespace signing keys. It leaves modules, Pulumi plugins, yank, revoke, and CLI reads to later parts.

## What Changes

- Publish provider archive sets through the API with checksum and signature checks at upload.
- Register per-namespace signing keys used only in that namespace.
- Serve the OpenTofu provider mirror and the team provider protocol for private providers.
- Refuse bad signatures, bad checksums, missing signatures, unknown versions, foreign hosts, unsafe names, traversal names, and cross-namespace reads, with audit entries.
- Keep published versions immutable in the active state and publish atomically.

Out of scope:

- Private modules through the module protocol.
- Private Pulumi plugins for download.
- Yank, revoke, and blocked handling beyond checksum-broken exclusion.
- Registry read commands of the CLI.
- Curated upstreams of public artifacts.
- Sweep of staged and unreferenced files and backup of release files.

## Usability

Where the path still costs the person time or surprises them:

- The publisher learns the safe-name rule only after a refusal; the docs page shows valid names first.
- The `tofu init` error for an unknown version names the address and version, so the Developer edits the right block.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- OpenTofu test helper exists. (`e2e/tool.go:110 (func OpenTofu)`)
- Checksum refusal test exists for backup check. (`pkg/backup/check_checksum_test.go:78 (TestCheckRefusesAManifestWithNoChecksum)`)
- Grant check test covers subject and namespace. (`pkg/access/access_test.go:783 (TestGrantChecksSubjectAndNamespace)`)
- Bad signature error code exists. (`internal/errcode/catalog_registry.go:25 (registry.signature-invalid)`)
- Untrusted key error code exists. (`internal/errcode/catalog_registry.go:27 (registry.signature-untrusted-key)`)
- Version exists error code exists. (`internal/errcode/catalog_registry.go:31 (registry.version-exists)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The tools' own protocols. When OpenDustpan serves providers and modules, it serves them through the HTTP protocols of the tools: the provider network mirror and the module registry protocol for OpenTofu, the provider registry protocol for providers that a team publishes under the host of the server, and the plugin download URL for the Pulumi CLI."
- Effect: The change serves private providers only through the two provider protocols named in the decision and adds no OCI endpoint.
- Conflict: none.

**ADR 269: Private providers and modules: own providers first, uploaded as the archive set** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 6 (current), 7 (current), 9 (current).
- Changed by: ADR 345 changes decision 11. Chain to the end: ADR 345.
- Current rule: this ADR, with ADR 345.
- Quote: "Own and team providers first. Issue 949, "Decided", items 10.1 and 10.2 (owner, 2026-10-18):"
- Quote: "An upload through the API. Issue 949, "Decided", item 10.4:"
- Quote: "Address host. A private address has the host of the server and the namespace of OpenDustpan."
- Quote: "No cross-namespace reads. A namespace never reads the registry of another namespace (question 4)."
- Quote: "No re-signing. The server does not sign what it serves (question 5)."
- Quote: "Trusted keys. Each namespace registers its own signing keys (question 8)."
- Effect: The change publishes own and team providers first by upload of the archive set with checksum and signature checks at upload, under the server host and safe-name rule, with no cross-namespace reads, no re-signing, and per-namespace signing keys.
- Conflict: none.

**ADR 345: An artifact version has four states** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "ADR 345 changes decision 11: the life cycle of a version has four states."
- Effect: The change publishes each provider version in the active state of the four-state life cycle. Yank and revoke stay in a later part. The ADR is owner-tagged and its words stay unchanged.
- Conflict: none.

## Open questions

1. How does the server unserve a published provider version when its signing key is later found stolen, before yank and revoke ship? Decides: owner.
   - Options: Keep serving the version and wait for yank and revoke in a later part; Exclude the version from the index at once through a block transition in this part.
   - Recommended: Keep serving rules unchanged in this part and exclude only checksum-broken files from the index; define full unserve in the life-cycle part. Reason: Only the active state ships here, so a compromised version has no defined removal path. The owner-tagged life-cycle ADR owns this.
2. Which hash algorithms does the provider mirror index carry for real tofu init? Decides: PM.
   - Options: Serve SHA-256 only in the mirror index; Serve SHA-256 plus h1 and zh hashes in the mirror index.
   - Recommended: Serve SHA-256 plus h1 and zh hashes derived from the stored archives. Reason: The mirror protocol uses h1 and zh hashes and e2e runs real tofu init.
3. How does OpenTofu send credentials to the mirror and discovery endpoints? Decides: PM.
   - Options: OpenTofu sends an access token bearer header to mirror and discovery endpoints; Mirror and discovery stay public and only downloads require a token.
   - Recommended: OpenTofu sends an access token bearer header to mirror, team protocol, and download endpoints with the read grant. Reason: Service discovery is shown without authorization and the token flow for the CLI is not defined.

Not spec-ready: a code check fails: ADR 345: the quote "ADR 345 changes decision 11: the life cycle of a version has four states." is not in the ADR file (copy it character for character from the decision text of the pack).

Questions considered and answered:

- Which protocols serve private providers? ADR 257 decision 1: the provider network mirror and the team provider registry protocol. This part serves these two only.
- How does a provider archive reach the registry? ADR 269 decisions 1 and 2: upload through the API as the archive set, checked at upload.
- Which addresses and namespaces can publish and read? ADR 269 decision 4 requires the exact host and the safe-name subset. Decision 6 forbids cross-namespace reads.
- Who signs and who trusts a provider archive? ADR 269 decision 9 gives per-namespace keys. Decision 7 forbids re-signing. Trust is checked at publish.
- What life-cycle state does a published version have? ADR 345 gives four states. This part publishes and serves only the active state.
- Which grant allows signing key changes? No ADR names a key-admin action. This part uses the publish grant and records each change in the audit log.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
