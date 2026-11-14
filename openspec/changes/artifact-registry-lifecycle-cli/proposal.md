---
version: 2
---

Roadmap: `docs/roadmap.md:379` (Support yank, revoke, and registry read commands.).

## Why

**Persona: namespace administrator.** A namespace administrator publishes private providers and modules and then finds a bad version. The administrator tries to pull the version from the index without breaking pinned builds, or to block a harmful version at once, and to list releases and keys for recovery.

The user path this change must make work:

1. **CLI.** Run release list, release show, and keys list to inspect the namespace registry.
2. **API.** Yank or revoke a bad version through the management API with a reason.
3. **E2e.** Run tofu init and the Pulumi plugin download over TLS with a personal token that holds the manage grant. Confirm a yanked pin still downloads and a revoked pin is refused.
4. **Docs page.** Read the registry life cycle docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Show, yank, or revoke of an unknown version fails. | The server answers 404 with code registry.not-found. Run the new list command to see present versions. | `opendustpan registry release list --namespace ns-01` |
| Download of a revoked version fails. | The server answers 409 with code registry.version-revoked and names the version. Run the new list command to see present versions. | `opendustpan registry release list --namespace ns-01` |
| List without the read grant fails. | The server answers 403 with code forbidden. Ask a namespace administrator for the registry read grant. | `opendustpan registry release list --namespace ns-01` |

Evidence for the claims above:

- ADR 269 defines yank as out of the index but downloadable and revoke as blocked, with the record kept. Proof: docs/adr/0269-private-providers-and-modules-are-uploaded-as-the-archive-set.md decision 11
- ADR 345 changes the life cycle of a version to four states. Proof: docs/adr/0345-an-artifact-version-has-four-states.md, listed in the chain of docs/adr/0269-private-providers-and-modules-are-uploaded-as-the-archive-set.md
- Registry routes for keys, providers, modules and uploads exist on main. Proof: route table: GET/POST /api/dustpan/v1/namespaces/{}/registry/keys and GET /api/dustpan/v1/namespaces/{}/registry/providers
- ADR 257 keeps serving through the tools own protocols and adds no OCI endpoint. Proof: docs/adr/0257-the-tools-own-registry-protocols-first-no-oci-endpoint.md decisions 1 and 2

## What Changes

- Add yank with continued download and revoke with blocked download for provider, module, and plugin releases.
- Add registry release list, release show, and keys list operations with namespace scope.
- Add new CLI command group `opendustpan registry` with commands `opendustpan registry release list --namespace ns-01`, `opendustpan registry release show`, and `opendustpan registry keys list`, generated from the API description.
- Provisional decision: until the owner names the four states of ADR 345, the build uses three marks: published, yanked, revoked. Yank and revoke stay behind one state type, so the owner names can replace the marks with no change to routes.

Out of scope:

- Publish, upload, signature checks, and checksum checks from earlier parts.
- Curated upstreams.
- Console catalog and create form.
- OCI endpoint.
- Undo of revoke.

Depends on:

- `artifact-registry-private-pulumi-plugins` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A revoked pin breaks `tofu init` and the error names no replacement.
- The list hides old states and recovery needs the include flag.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Registry error codes exist, including registry.not-found. (`internal/errcode/catalog_registry.go:21`)
- Registry error code registry.key-exists exists on main. (`internal/errcode/catalog_registry.go:11`)
- Object authorization is checked at point of use. (`pkg/access/access_test.go:1089 (TestRevokeChecksTheObject)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The tools' own protocols. When OpenDustpan serves providers and modules, it serves them through the HTTP protocols of the tools: the provider network mirror and the module registry protocol for OpenTofu, the provider registry protocol for providers that a team publishes under the host of the server, and the plugin download URL for the Pulumi CLI. RFC 10 and RFC 93 keep the order of these, the curation and the routes."
- Effect: Yank and revoke change only the index and download answers of the tool protocols. No OCI endpoint is added.
- Conflict: none.

**ADR 269: Private providers and modules: own providers first, uploaded as the archive set** (not owner-tagged).

- Decisions that apply: 6 (current), 11 (changed).
- Changed by: ADR 345 changes decision 11. Chain to the end: ADR 345.
- Current rule: this ADR, with ADR 345.
- Quote: "No cross-namespace reads. A namespace never reads the registry of another namespace (question 4)."
- Quote: "Life cycle. A "yank" takes a version out of the index and keeps it downloadable, so a lock file still works. A "revoke" blocks the version. Both keep the record (question 10)."
- Effect: Yank removes the version from the index and keeps download. Revoke blocks download. Both keep the record. Reads stay inside one namespace.
- Conflict: none.

**ADR 345: An artifact version has four states** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An artifact version has four states"
- Effect: The change follows the four-state life cycle. The pack gives no state names, so the build uses the provisional marks until the owner names the states. The owner is the whole ADR, and the change does not edit its words.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the default release list hide yanked and revoked versions?
   - Options: Hide yanked and revoked versions from the default list; Show all versions with a state column by default.
   - Provisional decision (owner review 14 Nov): Hide yanked and revoked versions from the default list and show them with a state column when the caller passes the include flag.
   - Reason: ADR 269 says yank takes a version out of the index but not what the management list shows. Benchmark: Google Artifact Registry shows yanked or deprecated versions in the list with a mark.
   - Persona impact: A hidden version could surprise a developer who pins it. The include flag removes that cost. Escape hatch: The include flag shows yanked and revoked versions.
   - Owner-tagged ADRs involved: ADR 269, ADR 345 (their words do not change).

Questions considered and answered:

- What do yank and revoke do to a release? Yank removes the version from the index and keeps it downloadable. Revoke blocks the version. ADR 269 decision 11, within ADR 345.
- Can one namespace read the releases of another namespace? No. ADR 269 decision 6.
- Does this change add a new download protocol such as OCI? No. ADR 257 decisions 1 and 2.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
