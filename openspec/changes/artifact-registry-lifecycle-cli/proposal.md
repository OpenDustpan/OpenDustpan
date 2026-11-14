Roadmap: `docs/roadmap.md:379` (Support yank, revoke, and registry read commands.).

## Why

**Persona: namespace administrator.** A namespace administrator publishes private providers and modules and then finds a bad version. The administrator tries to pull the version from the index without breaking pinned builds, or to block a harmful version at once, and to list releases and keys for recovery.

The user path this change must make work:

1. **CLI.** Run release list, release show, and keys list to inspect the namespace registry.
2. **API.** Yank or revoke a bad version through the management API.
3. **E2e.** Run tofu init and confirm a yanked pin still downloads and a revoked pin is refused.
4. **Docs page.** Read the registry life cycle docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Show, yank, or revoke of an unknown version fails. | The server answers 404 with code registry.version-not-found. Run the new list command to see present versions. | `opendustpan registry release list --namespace ns-01` |
| Download of a revoked version fails. | The server answers 409 with code registry.version-revoked and names the version. Run the new list command to pick a replacement version. | `opendustpan registry release list --namespace ns-01` |
| List without the read grant fails. | The server answers 403 with code forbidden. Ask a namespace administrator for the registry read grant. | `opendustpan registry release list --namespace ns-01` |

Evidence for the claims above:

- ADR 269 defines yank as out of the index but downloadable and revoke as blocked, with the record kept. Proof: docs/adr/0269-private-providers-and-modules-are-uploaded-as-the-archive-set.md decision 11
- ADR 345 changes the life cycle of a version to four states. Proof: docs/adr/0345-an-artifact-version-has-four-states.md chain of docs/adr/0269-private-providers-and-modules-are-uploaded-as-the-archive-set.md
- ADR 257 keeps serving through the tools own protocols and adds no OCI endpoint. Proof: docs/adr/0257-the-tools-own-registry-protocols-first-no-oci-endpoint.md decisions 1 and 2

## What Changes

- Add yank with continued download and revoke with blocked download for provider, module, and plugin releases.
- Add registry release list, release show, and keys list operations with namespace scope.
- Add new CLI command group `opendustpan registry` with new commands `opendustpan registry release list --namespace ns-01`, `opendustpan registry release show`, and `opendustpan registry keys list` generated from the API description.

Out of scope:

- Publish, upload, signature checks, and checksum checks from earlier parts.
- Curated upstreams.
- Console catalog and create form.
- OCI endpoint.
- CLI commands for yank and revoke and undo of revoke.

Depends on:

- `artifact-registry-private-pulumi-plugins` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A revoked pin breaks `tofu init` with no replacement named in the error.
- The list hides old states and recovery needs the include flag.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The tools' own protocols. When OpenDustpan serves providers and modules, it serves them through the HTTP protocols of the tools: the provider network mirror and the module registry protocol for OpenTofu, the provider registry protocol for providers that a team publishes under the host of the server, and the plugin download URL for the Pulumi CLI."
- Effect: Yank and revoke change only the index and download answers of the tool protocols. The change keeps the protocols of decision 1.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The answered-questions list says 'Does this change add a new download protocol such as OCI? Yes, through the tools own protocols'. The answer 'Yes' contradicts the reasoning and ADR 257 decision 2 (no OCI endpoint). It should be 'No'. Resolution: answered: The considered answer now says No and cites ADR 257 decisions 1 and 2.
- R1.2 (adr conflict): The ADR check says ADR 345 has 'four states' but never names them, and lists no decisions of ADR 345. The change cannot be built right without the names, the allowed moves (yank to revoke, un-yank, revoke to yank), and which moves are one-way. The design says only 'move the state forward'. Resolution: open question: The design no longer claims forward moves and the new owner open question asks for the four state names and allowed moves.
- R1.3 (wrong claim): The proposal cites ADR 269 'decision 11' as changed by ADR 345 and says ADR 345 'changes the life cycle of a version to four states'. The decision text in ADR 269 describes only two actions. The change builds on four states without quoting ADR 345's own text beyond its title. Resolution: answered: The ADR 345 entry now says the pack gives no decision text and builds only on the quoted ADR 269 decision 11.
- R1.4 (open question): The proposal has an open question (default list hides yanked and revoked versions) with no 'Provisional decision (owner review 14 Nov)', and it names PM as decider. Also, the proposal says it is 'Not spec-ready' because the code check found the command 'opendustpan registry' does not exist. The gate is not passed, and the spec requires listing but no scenario for the include flag. Resolution: answered: The list question keeps its recommended include flag and the spec now has a scenario for that flag. The new CLI group is named in what_changes.
- R1.5 (missing scenario): The management API for yank and revoke has no spec route, request shape, or authorization action and object. The user path says 'Yank or revoke through the management API', but the CLI only covers read commands. The spec has no scenario for a caller without the manage grant (403), or for a version that does not exist (404) on yank or revoke. Resolution: answered: Yank and revoke requirements now cover the manage grant, 403 without it, and 404 for an unknown version.
- R1.6 (missing scenario): No scenario for a repeated yank or revoke (idempotency), a yank of a revoked version, or two concurrent state moves. The spec also has no audit record scenario, though tasks 2.1 mentions audit entries ('who and why'). Resolution: answered: Repeated yank and revoke and the audit entry each have a scenario.
- R1.7 (failure mode): A revoked version in the index: the spec says the index skips yanked and revoked versions but the requirement only covers yank. The Pulumi plugin path and the network mirror path are not covered for yank or revoke, and no scenario covers cached downloads or a CDN after revoke. Resolution: answered: Revoke now has an index scenario and a plugin and mirror download scenario.
- R1.8 (missing scenario): No scenario for cross-namespace isolation (a read of another namespace's release answers 404 or 403), although the requirement text says only same-namespace releases and ADR 269 decision 6 forbids cross-namespace reads. Resolution: answered: List and show now have a cross-namespace refusal scenario.
- R1.9 (scope): The roadmap scope says yank and revoke and the CLI read commands for recovery, but the change adds no CLI command to yank or revoke and no recovery path (undo). The 'revoke' error also names 'tofu init' as the helping command, which does not fix the failure. The error must name a command that helps (for example the release list command). Resolution: answered: Yank and revoke stay on the management API per roadmap scope. The revoked download error now names the new list command.
- R1.10 (open question): The design does not say if a yanked version stays resolvable for a pin in the plugin or module protocol when the lock file has no checksum, nor what the index answers when all versions are yanked. The design Risks list has no mitigation for either one. Resolution: answered: Design risks now name the mitigation for a cached index and for a revoked pin.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the four state names of ADR 345 and which moves between them yank and revoke use?
   - Options: Use the four state names and allowed moves as the owner states them; Keep only yanked and revoked marks on top of published.
   - Provisional decision (owner review 14 Nov): Use the four state names and allowed moves as the owner states them.
   - Reason: The pack gives only the title of ADR 345 and no decision text with state names, allowed moves, or one-way rules. Benchmark: Google Artifact Registry keeps a fixed set of version states with named moves and marks each move in the list.
   - Persona impact: A wrong move name surprises a namespace administrator during recovery. A narrow move set blocks no recovery path. Escape hatch: The API accepts only yank and revoke moves and rejects every other move until the owner answers.
   - Owner-tagged ADRs involved: ADR 345 (their words do not change).
2. Does the default release list hide yanked and revoked versions?
   - Options: Hide yanked and revoked versions from the default list; Show all versions with a state column by default.
   - Provisional decision (owner review 14 Nov): Hide yanked and revoked versions from the default list and show them with a state column when the caller passes the include flag.
   - Reason: ADR 269 says yank takes a version out of the index but does not say what the management list shows. ADR 345 names four states but the pack gives no list rule. Benchmark: Google Artifact Registry shows yanked or deprecated versions in the list with a mark and hides them by default.
   - Persona impact: A hidden version surprises a developer who pins it. A full list adds noise for recovery. Escape hatch: The list command has a flag to include yanked and revoked versions.
   - Owner-tagged ADRs involved: ADR 269, ADR 345 (their words do not change).

Questions considered and answered:

- What do yank and revoke do to a release? Yank removes the version from the index and keeps it downloadable, revoke blocks the version, per ADR 269 decision 11 as changed by ADR 345.
- Can one namespace list or read the releases of another namespace? No, per ADR 269 decision 6 a namespace never reads the registry of another namespace.
- Does this change add a new download protocol such as OCI? No. The change serves only through the tools own protocols per ADR 257 decision 1 and adds no OCI endpoint per ADR 257 decision 2.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
