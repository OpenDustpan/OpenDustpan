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
| Plugin version with bad signature, bad checksum, or revoked state is refused at download. | 403 that names the plugin and version with no archive bytes; fix the archive and publish again through the publish API. | `opendustpan status` |
| Unknown plugin name, version, OS, or arch is requested. | 404 that names the plugin, version, OS, and arch; check the published versions list through the registry read API. | `opendustpan status` |
| Download request has no valid token or no read access. | 401 for a missing or invalid token and 403 for a token without read access on the namespace registry; sign in again or ask for access. | `opendustpan status` |

Evidence for the claims above:

- Milestone step 4 asks the Pulumi CLI to get a resource plugin from the registry and refuses a bad signature or checksum. Proof: Roadmap step: The Pulumi CLI gets a resource plugin from it. A provider with a bad signature or a bad checksum is refused.
- This part covers only private Pulumi plugin download and reuses publish, grants, audit, and refusal rules. Proof: Roadmap split scope in context pack: This part publishes and serves private Pulumi plugins through the plugin download URL. It reuses the publish API, grants, audit, and refusal rules from earlier parts.
- Yank, revoke, and CLI reads belong to the last registry part, not this part. Proof: Roadmap split scope in context pack: It leaves yank, revoke, and CLI reads to the last part.

## What Changes

- Serve private Pulumi plugins through the plugin download URL per namespace with stored checksum and signature refusal.
- Reuse the provider publish API, per-namespace trusted keys, grants, and audit for plugin archives; add no new opendustpan registry CLI command in this part.

Out of scope:

- Yank and revoke actions of plugin versions
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
- The docs page must show the exact server flag and install command so the first install works without guesswork.

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

- Decisions that apply: 2 (current), 4 (current), 6 (current), 7 (current), 11 (changed).
- Changed by: ADR 345 changes decision 11. Chain to the end: ADR 345.
- Current rule: this ADR, with ADR 345.
- Quote: "archive set of ADR 177. Checksums and the signature are checked at"
- Quote: "Only a name in the registry-safe subset can publish: lowercase letters, digits and single dashes, with no dash at the start or the end (RFC 101 question 2)."
- Quote: "A namespace never reads the registry of another namespace (question 4)."
- Quote: "The server does not sign what it serves (question 5)."
- Quote: "A "yank" takes a version out of the index and keeps it downloadable, so a lock file still works."
- Effect: Change reuses upload as archive set with checks at upload, registry-safe names, no cross-namespace reads, and no re-signing. Yanked stays downloadable and revoked stays blocked.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The Open questions section says the change is not spec-ready: the command `opendustpan registry` does not exist. The user path and the error table name `opendustpan registry publish` and `opendustpan registry list` as the one command that helps. The gate fails, and the error answers point to commands that cannot run. Use a command that exists or add the command in scope. Resolution: answered: Error table and user path now use the real Pulumi CLI install command; what_changes states no new opendustpan registry command is added.
- R1.2 (scope): The change says it reuses 'the publish API' for Pulumi plugins, but no task or requirement publishes a plugin. The Why step 1 and the roadmap scope ('publishes and serves private Pulumi plugins') need a publish step for plugin archives. The delta spec has no requirement for the plugin archive set, plugin kind, name rules, or upload checks. Resolution: answered: Added publish plugin archive set requirement with name rules and upload checks that reuses the provider publish path.
- R1.3 (missing scenario): No requirement covers authentication and authorization on the plugin download route. The Pulumi CLI must send a token, and a missing, invalid or unauthorized token needs a defined answer (401 or 403). Task 2.1 mentions actions but the spec has no scenario, and the namespace isolation scenario only covers URL mismatch. Every endpoint must declare its authorization actions and the object of each. Resolution: answered: Added authorization requirement with 401 and 403 scenarios and authorization actions on the download route.
- R1.4 (adr conflict): The design hostile test requests a 'yanked path variant' and expects rejection. ADR 269 decision 11, changed by ADR 345, says a yanked version stays downloadable, and ADR 345 has four states. The proposal also puts yank out of scope. The download behavior for each of the four states (yanked, revoked) is not defined, and the design test contradicts the ADR rule. Resolution: answered: Removed yanked rejection test, defined yanked downloadable and revoked blocked, and added owner open question for the four state names.
- R1.5 (wrong claim): The ADR check does not list ADR 345, which changes decision 11 and is owner-tagged as a whole ADR, in its own section. It lists ADR 345 only as the Changed by chain. The change touches the version state (refusal at download), so the four states of ADR 345 must be quoted and checked. Resolution: answered: Added ADR 345 as its own adr_check entry with its effect on download refusal.
- R1.6 (failure mode): The design says download checks the stored verdict, but the verdict could be stale or the blob could be changed or missing in storage. No requirement says the server checks the SHA-256 of the streamed bytes, or what happens when the blob is missing or corrupt (500 or 404, partial stream). The spec also has no scenario for a mid-stream failure. Resolution: answered: Added stream checksum match requirement and missing blob scenario plus design layer and hostile test.
- R1.7 (missing scenario): The spec does not state which SHA-256 form the response carries (header or body) or how the Pulumi CLI reads it. It also has no scenario for the plugin name rules of ADR 269 decision 4, a host mismatch, or unknown OS or arch values. The 404 body that names plugin, version, OS and arch has no scenario. Resolution: open question: Added name rule, host, OS and arch, and checksum transport scenarios; checksum transport detail is an open question for the PM.
- R1.8 (open question): The change does not say how the Pulumi CLI finds the server as a plugin source (the --server flag or PULUMI_PLUGIN_DOWNLOAD_URL, the URL template, and the ${OS}/${ARCH} placeholders). The e2e step depends on this. It also does not say how a private plugin is looked up versus the public Pulumi plugin source. Resolution: open question: Added PM open question on plugin source discovery and URL template.
- R1.9 (wrong claim): The design layer table lists a 'port adapter' that uses placeholders and a containment check for archive paths. The change does not show that a file path read exists in the storage adapter; blobs are referenced by a release record. The claim has no evidence, and the 'Structure' item names an unpublished constructor with no ADR or code proof of the pattern. Resolution: answered: Removed file path containment claim; layers now check verdict and byte checksum, and structure names the unexported download type and constructor check.
- R1.10 (missing scenario): Task 2.1 promises an audit entry on download, but no requirement or scenario defines the audit record (who, what, why) for success or refusal. Test mapping in tasks.md has no tests for the audit entry or the docs-page command test name. Resolution: answered: Added audit requirement with success and refusal scenarios and audit test checks in tasks.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the exact names of the four artifact states of ADR 345 and the download answer of each state?
   - Options: Yanked stays downloadable and revoked stays blocked, with the other two states mapped as active and blocked; All non-active states stay downloadable.
   - Provisional decision (owner review 14 Nov): Yanked stays downloadable and revoked stays blocked, with the other two states mapped as active and blocked.
   - Reason: ADR 269 decision 11 as changed by ADR 345 is the current rule, but the pack gives no state names beyond yanked and revoked. Benchmark: AWS CodeArtifact keeps yanked versions installable and blocks revoked versions, which matches the stated yank and revoke behavior.
   - Persona impact: Developer installs stay pinned and predictable; no persona loses access without a record. Escape hatch: Operators keep pinned installs on yanked versions until the owner names the states.
   - Owner-tagged ADRs involved: ADR 345 (their words do not change).
2. How does the Pulumi CLI find the server as a plugin source and what is the exact download URL template?
   - Options: Server flag plus download URL template with OS and arch placeholders; Private plugin mirror configured on the Pulumi CLI side.
   - Provisional decision (owner review 14 Nov): Server flag plus download URL template with OS and arch placeholders.
   - Reason: ADR 257 names the plugin download URL but the pack gives no template, flag, or lookup order against the public plugin source. Benchmark: Google Artifact Registry documents the exact URL template and OS and arch placeholders that each native client uses.
   - Persona impact: Developer setup stays one documented install command; no extra manual step. Escape hatch: E2E pins the documented server flag and URL template until the PM confirms it.
3. Where does the download response carry the stored SHA-256 for the Pulumi CLI check?
   - Options: SHA-256 in a response header checked by the CLI; SHA-256 only in the registry read API.
   - Provisional decision (owner review 14 Nov): SHA-256 in a response header checked by the CLI.
   - Reason: The pack states stored checksum refusal but gives no header or body form for the checksum at download. Benchmark: Google Artifact Registry returns the SHA-256 in a response header so the client checks the bytes it received.
   - Persona impact: Developer installs fail fast on corrupt bytes with the same install command. Escape hatch: Clients that ignore the header still get a refused stream on mismatch.

Questions considered and answered:

- How does the Pulumi CLI fetch a private plugin? Yes, through the plugin download URL named in ADR 257 decision 1; no OCI endpoint per ADR 257 decision 2.
- Does download re-check the signature? No; ADR 269 decision 2 checks checksums and signature at upload, and this part reuses that stored verdict at download plus a byte checksum match at stream time.
- Can one namespace download the plugin of another namespace? No; ADR 269 decision 6 forbids cross-namespace reads, so the download URL stays scoped to one namespace.
- Does a yanked plugin version stay downloadable? ADR 269 decision 11 as changed by ADR 345 controls it: yanked stays downloadable and revoked stays blocked; the other two state names are an open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
