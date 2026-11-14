---
version: 2
---

Roadmap: `docs/roadmap.md:379` (Serve private Pulumi plugins for download.).

## Why

**Persona: developer.** A Developer runs the Pulumi CLI against the server and needs a private resource plugin from the team registry, with a clear refusal when the archive is bad.

The user path this change must make work:

1. **API.** Publish the private plugin archive set through the registry publish API.
2. **CLI.** Run the Pulumi CLI install command against the server; the CLI fetches the plugin from the plugin download URL.
3. **Docs page.** Read the publish and install page when a download is refused.
4. **E2e.** Run the e2e install of a private Pulumi plugin over TLS with the real binary.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Plugin version with bad signature or bad checksum is refused at download. | 403 that names the plugin and version and names the publish API call that fixes it; no archive bytes are sent. | `pulumi plugin install resource <name> <version> --server <host>` |
| Unknown plugin name, version, OS, or arch is requested. | 404 that names the plugin, version, OS, and arch and names the registry read API call that lists published versions. | `pulumi plugin install resource <name> <version> --server <host>` |
| Download request has no valid token or no read access. | 401 for a missing or invalid token and 404 for a token without read access or for another namespace, to avoid leaking existence; check the token and grants. | `opendustpan status` |

Evidence for the claims above:

- Milestone step 4 asks the Pulumi CLI to get a resource plugin from the registry and refuses a bad signature or checksum. Proof: Roadmap step: The Pulumi CLI gets a resource plugin from it. A provider with a bad signature or a bad checksum is refused.
- This part covers only private Pulumi plugin download and reuses publish, grants, audit, and refusal rules. Proof: Roadmap split scope in context pack: This part publishes and serves private Pulumi plugins through the plugin download URL. It reuses the publish API, grants, audit, and refusal rules from earlier parts.
- Yank, revoke, and CLI reads belong to the last registry part, not this part. Proof: Roadmap split scope in context pack: It leaves yank, revoke, and CLI reads to the last part.

## What Changes

- Serve private Pulumi plugins through the plugin download URL per namespace with stored checksum and signature refusal.
- Reuse the provider publish API, per-namespace trusted keys, grants, and audit for plugin archives; add no new opendustpan registry CLI command in this part.
- The plugin download route is a new route under the namespace registry path, and the e2e pins the URL template that the route documents.

Out of scope:

- Yank and revoke actions of plugin versions
- Life-cycle states beyond published
- Registry read commands of the CLI
- Curated upstreams of public artifacts
- OCI endpoint
- New opendustpan registry CLI commands

Depends on:

- artifact-registry-private-provider
- `artifact-registry-private-module` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The Pulumi CLI error for a refused plugin must name the plugin, version, and the publish API call that fixes it.
- The docs page must show the exact server setting and install command so the first install works without guesswork.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Registry refusal error code for bad signature already exists. (`internal/errcode/catalog_registry.go:25 (registry.signature-invalid)`)
- Registry refusal error code for untrusted key already exists. (`internal/errcode/catalog_registry.go:27 (registry.signature-untrusted-key)`)
- Registry refusal error code for not found already exists. (`internal/errcode/catalog_registry.go:21 (registry.not-found)`)
- Registry refusal error code for invalid archive already exists. (`internal/errcode/catalog_registry.go:9 (registry.invalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "and the plugin download URL for the Pulumi CLI."
- Quote: "No OCI endpoint now. OpenDustpan serves no OCI Distribution API. It is not an OCI registry."
- Effect: Change serves private Pulumi plugins only through the plugin download URL and adds no OCI endpoint.
- Conflict: none.

**ADR 269: Private providers and modules: own providers first, uploaded as the archive set** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current), 6 (current), 7 (current).
- Changed by: ADR 345 changes decision 11. Chain to the end: ADR 345.
- Current rule: this ADR, with ADR 345.
- Quote: "No cross-namespace reads. A namespace never reads the registry of another namespace (question 4)."
- Quote: "No re-signing. The server does not sign what it serves (question 5)."
- Effect: Change reuses upload as archive set with checks at upload, registry-safe names, no cross-namespace reads, and no re-signing. Life-cycle states ship in the last registry part.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Add e2e step against the real Pulumi CLI on SQLite and PostgreSQL, usi" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Run wiring check, route authorization check, docs page, usability walk" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- How does the Pulumi CLI fetch a private plugin? Through the plugin download URL named in ADR 257 decision 1; no OCI endpoint per ADR 257 decision 2.
- Does download re-check the signature? No; ADR 269 decision 2 checks checksums and signature at upload, and this part reuses that stored verdict at download plus a byte checksum check at stream time.
- Can one namespace download the plugin of another namespace? No; ADR 269 decision 6 forbids cross-namespace reads.
- Does this part handle yanked or revoked states? No; they ship in the last registry part.
- Is the URL template an open question that blocks the build? No. The route is new and owned by this change, so the change defines its template and the real CLI e2e proves it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
