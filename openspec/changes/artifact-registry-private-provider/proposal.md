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
| Upload with an input that the address rule does not allow. | The server answers 400 with a body that names the input and says to run `opendustpan doctor`. | `opendustpan doctor` |
| Upload with a bad checksum, a missing signature, or a bad signature. | The server answers 422 with a body that names the bad checksum or bad signature and says to run `opendustpan doctor`. | `opendustpan doctor` |
| `tofu init` for an unknown address or version. | The server answers 404 with a body that names the address and version and says to check the published versions with `opendustpan doctor`. | `opendustpan doctor` |

Evidence for the claims above:

- The milestone step requires tofu init to fetch a private provider and refuses a provider with a bad signature or a bad checksum. Proof: Roadmap step: `tofu init` gets a private provider and a private module from the registry (ADR 257, ADR 269). The Pulumi CLI gets a resource plugin from it. A provider with a bad signature or a bad checksum is refused.
- This part covers provider archives and namespace signing keys and leaves modules, Pulumi plugins, yank, revoke, and CLI reads to later parts. Proof: Roadmap scope: This part publishes provider archives through the API and registers namespace signing keys. It serves the OpenTofu provider mirror and the team provider protocol, and it refuses bad signatures, bad checksums, and unknown versions. It leaves modules, Pulumi plugins, yank, revoke, and CLI reads to later parts.

## What Changes

- Publish provider archive sets through the API with checksum and signature checks at upload.
- Register per-namespace signing keys used only in that namespace.
- Serve the OpenTofu provider mirror and the team provider protocol for private providers.
- Refuse bad signatures, bad checksums, missing signatures, unknown versions, foreign hosts, unsafe names, traversal names, and cross-namespace reads, with audit entries.
- Keep published versions immutable in the active state and publish atomically.

Out of scope:

- Private modules through the module protocol.
- Private Pulumi plugins for download.
- Yank, revoke, and other states of the four-state life cycle.
- Registry read commands of the CLI.
- Curated upstreams of public artifacts.
- Sweep of staged and unreferenced files and backup of release files.
- h1 hash and registry slug beyond SHA-256 checks in this part.

## Usability

Where the path still costs the person time or surprises them:

- The publisher must learn the safe-name rule only after a refusal; the docs page shows valid names first.
- The `tofu init` error for an unknown version must name the address and version, or the Developer edits the wrong block.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "When OpenDustpan serves providers and modules, it serves them through the HTTP protocols of the tools:"
- Quote: "the provider network mirror and the module registry protocol for OpenTofu, the provider registry protocol for providers that a team publishes under the host of the server, and the plugin download URL for the Pulumi CLI."
- Effect: The change serves private providers only through the two provider protocols named in the decision: the provider network mirror and the team provider protocol under the host of the server. It adds no OCI endpoint and leaves the plugin download URL to a later part.
- Conflict: none.

**ADR 269: Private providers and modules: own providers first, uploaded as the archive set** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 6 (current), 7 (current), 9 (current).
- Changed by: ADR 345 changes decision 11. Chain to the end: ADR 345.
- Current rule: this ADR, with ADR 345.
- Quote: "Own and team providers first."
- Quote: "An upload through the API."
- Quote: "Address host. A private address has the host of the server and the namespace of OpenDustpan."
- Quote: "No cross-namespace reads. A namespace never reads the registry of another namespace (question 4)."
- Quote: "No re-signing. The server does not sign what it serves (question 5)."
- Quote: "Trusted keys. Each namespace registers its own signing keys (question 8)."
- Effect: The change publishes own and team providers first through the mirror by upload of the archive set with checksum and signature checks at upload, under the server host and safe-name rule, with no cross-namespace reads, no re-signing, and per-namespace signing keys.
- Conflict: none.

**ADR 345: An artifact version has four states** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "the life cycle of a version has four states."
- Effect: The change publishes each provider version in the active state of the four-state life cycle. Yank and revoke stay in a later part and the serve path reads only the active state.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The error table names the command `opendustpan registry check --namespace <namespace>`, but this command does not exist; the proposal itself says the code check fails. The spec cannot be built or tested as written, and the change adds no task to create the command. Resolution: answered: Errors now name only `opendustpan doctor`, a command that exists; no new command is named.
- R1.2 (wrong claim): The 'Open questions' section reads 'Not spec-ready' with a failed code check, so the spec-ready gate (section reads 'none') is not met. The failure must be fixed before implementation. Resolution: answered: Code check failure fixed and open questions read none; considered answers cover the prior failure.
- R1.3 (wrong claim): The proposal says ADR 257 decision 1 names 'the plugin download URL' and then says 'two provider protocols only'; its first quote is only a fragment, and 'Conflict: none' for the Pulumi plugin URL is not proven. The wording also differs from the decision, which lists the mirror, the module protocol, and the team registry protocol. Resolution: answered: ADR 257 quotes now copy exact spans that name the mirror, the module protocol, the team provider protocol, and the plugin URL; effect states the plugin URL stays in a later part.
- R1.4 (missing scenario): The spec has no scenario for the team provider protocol (service discovery, versions list, download metadata with shasums and signature URLs), although the proposal says it serves it. The spec covers the mirror index only. Resolution: answered: New requirement Serve team provider protocol covers discovery, versions list, and download metadata.
- R1.5 (missing scenario): No scenario for cross-namespace reads, although ADR 269 decision 6 requires refusal and the tasks list a cross-namespace isolation test. Also no scenario for a namespace A key trusted for a namespace B archive. Resolution: answered: New scenarios cover cross-namespace serve refusal and cross-namespace key refusal.
- R1.6 (missing scenario): No scenario for a bad SHA-256 at publish (only at serve), for a missing signature, for an archive that is not in the archive set, or for an unknown version at publish. The proposal's 422 error row has no spec scenario with a status code. Resolution: answered: New scenarios cover bad SHA-256 at publish, missing signature, archive outside the set, and unknown version at serve with status codes.
- R1.7 (open question): The change does not say how a key is registered or removed: no key format, key ID, expiry, or rotation. It does not say what happens to published versions when a key is removed or expires (re-verify at serve time or not). The design says the download rechecks 'key trust', so this decides if old versions still serve. Resolution: answered: Key register requirement defines armored OpenPGP key with key ID, removal semantics, and that trust applies at publish only so published versions keep serving.
- R1.8 (failure mode): Concurrent publishes of the same address and version are not covered; the design says only 'unique key' and retry. Two parallel publishes with different files must give one winner and one immutable refusal, with no orphan blob. Blob write versus database transaction failure (blob stored, commit fails) is also not covered, and the sweep is out of scope. Resolution: answered: Design and immutable-version scenarios define one winner for parallel publishes and staged-blob cleanup on failed commit.
- R1.9 (failure mode): Hostile upload limits are not specified: the size limit value, a zip bomb, many files, a path traversal in file names, and a malformed OpenPGP key or signature packet. The hostile test lists only an oversized file. Resolution: answered: Handler layer and safe-address requirement define size limit, traversal refusal, and malformed packet refusal with hostile tests.
- R1.10 (scope): ADR 345 (four states, owner-tagged whole ADR) changes the life cycle. The change leaves yank and revoke out, but the spec does not say what state a published version has now, or how the serve check reads the state; the design mentions 'version state' without a definition. Resolution: answered: ADR 345 check added; published versions are in the active state and serve reads only that state.
- R1.11 (missing scenario): The 404 error row says to run `tofu init -upgrade` after a new publish, but the 'Withdrawn address' case has no spec scenario, and withdrawal is out of scope (yank and revoke are later). Also, the read grant for serving is not defined: the spec names only the publish grant, so who can run `tofu init` is open. Resolution: answered: Withdrawal removed from errors; read grant defined so any authenticated principal with the read grant in the namespace can run tofu init.
- R1.12 (missing scenario): The 'Bad checksum at serve time' audit entry has no failure-mode detail: whether the server answers 5xx or 4xx, and whether the file is flagged. Also the spec does not require authorization actions and objects per endpoint, as project.md requires. Resolution: answered: Serve checksum scenario defines 500 plus audit entry and flagged file; each requirement states its authorization action and object.

## Open questions

none

Questions considered and answered:

- Which protocols serve private providers? ADR 257 decision 1 names the provider network mirror, the module registry protocol, the team provider registry protocol under the host of the server, and the plugin download URL. This part serves the two provider protocols only.
- How does a provider archive reach the registry? ADR 269 decisions 1 and 2 require own and team providers first through the mirror, uploaded through the API as the archive set with checksums and signature checked at upload.
- Which addresses and namespaces can publish and read? ADR 269 decision 4 requires the host of the server compared exactly and the registry-safe subset, and decision 6 forbids cross-namespace reads.
- Who signs and who trusts a provider archive? ADR 269 decision 9 requires per-namespace signing keys, decision 7 forbids re-signing, and trust is checked at publish so removal of a key never unserves a published version.
- What life-cycle state does a published version have? ADR 345 defines four version states. This part publishes only the active state and serves only that state; yank and revoke are later parts.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
