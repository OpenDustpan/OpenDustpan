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

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md open question 1 (unpack limits) is not answered by the owner; the spec-ready gate reads not ready and the change cannot be built right without the limit values. Resolution: answered: Limits now live in the spec requirement text and design layers as 64 MB unpacked, 1024 files, 16 MB per file, with scenarios for each refusal.
- R1.2 (wrong claim): proposal.md states the spec-ready gate result with a failing code check (command opendustpan registry does not exist) and a truncated benchmark sentence; the ADR check lists no ADR and the decisions list is empty, so the claim that no ADR is touched has no quoted proof. Resolution: answered: Error commands use existing commands only, open_questions reads none, and considered records that the pack adrs list is empty so no ADR is touched.
- R1.3 (missing scenario): No scenario covers an archive without terraform-registry-manifest.json when the publisher states protocols; design layer 'service' says missing manifest fails closed, but spec and tasks do not test it. Resolution: answered: Requirement protocols-without-manifest covers stated protocols without a manifest with a mismatch refusal scenario.
- R1.4 (missing scenario): Spec has no scenario for an archive without a manifest and with no stated protocols, nor for a manifest that is malformed or has an unknown protocol value. Resolution: answered: Requirements protocols-without-manifest, malformed-manifest and unknown-protocol-value cover empty protocols without a manifest, malformed JSON, and unknown protocol values.
- R1.5 (failure mode): Hostile archive cases from design layer 'port adapter' (symlink bits, zip bomb ratio, oversize output) have no spec scenario; only tasks mention path checks, so the 400 and no-file-left-in-staging behavior is untested in the spec. Resolution: answered: Requirement reject-unsafe-archive covers traversal, symlink, oversize, count, and ratio cases with hostile scenarios tied to 400 registry.bad-archive.
- R1.6 (missing scenario): HTTP handler layer (413 size limit, 415 content type, 403 before unpacking) has no requirement or scenario in the spec. Resolution: answered: Requirement request-guards covers 413 size, 415 content type, and 403 before unpack with scenarios.
- R1.7 (missing scenario): The proposal's error table names 400 registry.bad-archive, 400 registry.protocol-mismatch and 409 registry.content-changed, but the spec uses the words content-changed and protocol-mismatch without the codes and omits the exact 400 status; the bad-archive error has no scenario. Resolution: answered: Each requirement text and scenario names the exact status and code for bad-archive, protocol-mismatch, and content-changed.
- R1.8 (open question): The slug derivation rule (how a stable ID becomes a registry-safe slug, collisions, and whether the slug is stored for existing namespaces) is not specified; design decision 3 and the spec leave it open and no ADR answers it. Resolution: answered: Requirement serve-releases-under-slug states the derivation rule and the stored slug for existing namespaces.
- R1.9 (failure mode): Concurrent republish of the same version with different bytes is not covered; the check between existing release and new release may race without a stated transaction rule. Resolution: answered: Requirements for concurrent republish state the single-transaction compare-and-store rule with race scenarios.
- R1.10 (scope): Tasks 2.3 and 3.x require an e2e step and docs page, but the proposal's Usability section and the authorization actions for the publish route are not named, so the route's action and object are unchecked. Resolution: answered: Tasks name the publish route with unit:manage action on the namespace object, plus the e2e step, docs page, and usability check.
- R2.1 (wrong claim): The error table names `opendustpan doctor --help` and `opendustpan status --help` as the one command that helps. The input does not show that these commands exist or that they help with an archive, protocol or content-changed error. The earlier second-pass answer claims existing commands only, with no proof. Resolution: answered: Error commands use tofu init, a real CLI named in the milestone exit test, and each answer names the code and the fix step.
- R2.2 (open question): The slug rule (lowercase namespace ID without the ns- prefix) is not proven. No evidence shows that the ID has an ns- prefix, that the result is registry-safe, or how a namespace that already has releases under its old name migrates to the slug. The spec says it states the collision property and stored slug for existing namespaces, but no such text exists in the spec. Design says the slug is stored on the release record, not on the namespace, so the path lookup from slug to namespace is undefined. Resolution: answered: Slug rule derives from the stable ID, stores the slug on the namespace record, indexes slug to namespace, and keeps the prior name path for existing releases.
- R2.3 (wrong claim): The spec requirement 'Serve releases under slug' applies only where the name is unsafe. Issue #1228 and the design say the slug is derived and served for each namespace, and the Slug-stable-after-rename scenario implies that safe names also keep the same path. Two URL schemes then exist for one namespace, and a rename from a safe to an unsafe name changes the path. Resolution: answered: Serve requirement applies to each namespace under its slug, with dual serve of the prior name path for existing releases.
- R2.4 (missing scenario): The ratio limit is claimed in the second-pass answer and tasks 1.3 (zip bomb ratio), but the spec and the design give no ratio value. The 'Limit exceeded' scenario lists only size, count and per-file limits. The unpack must also bound the declared size against the real decompressed size, since zip headers can lie; this is not stated. Resolution: answered: Ratio limit of 100 times expansion is in the unpack requirement scenarios and design layers.
- R2.5 (failure mode): The 32 MB request limit is lower than the 64 MB unpacked limit, but the spec does not say how the request size is enforced when the Content-Length header is absent or false (chunked body). It also does not say what happens to staged bytes after a refusal, or how the staging directory is cleaned up after a crash or restart. Resolution: answered: Request guard requirement counts bytes during read for chunked bodies and deletes staged bytes on refusal; the sweep requirement removes orphans at startup.
- R2.6 (open question): Authorization is inconsistent. The spec and design say 'publish grant', and tasks say action unit:manage on the namespace object. Neither is proven to exist or to be right for a registry publish. The order of the 403, 413 and 415 checks is not specified, so a request that is oversize and denied has an undefined answer. The 'refuse before unpack' claim for 403 conflicts with the design, which stages bytes before the service check. Resolution: answered: Authorization order is auth first then content type then size, staged only after checks, with unit:manage on the namespace object in tasks.
- R2.7 (missing scenario): The spec has no scenario for an existing release stored before this change that has no H1. SameContent now counts H1, so a same-bytes republish of an old release would return 409 content-changed. A migration rule or a backfill is needed, but a numbered migration is forbidden by project.md. Resolution: answered: Missing-H1 requirement treats a SHA256 match as identical and backfills H1 with no numbered migration.
- R2.8 (failure mode): The concurrent-republish scenario says one request succeeds and the other gets 409 'or the identical result', which is not testable. The h1 hash definition is also missing: the algorithm (dirhash over the file list, symlink handling, ordering) is not stated, and the digest must match what OpenTofu computes for the lock file. No scenario checks that the digest equals the CLI value. Resolution: answered: Concurrent republish is two testable scenarios and the h1 requirement names the dirhash.h1 algorithm with a lock-file equality scenario.
- R2.9 (scope): The publish path today takes protocols from the request and the zip must be unpacked, but the existing e2e with the real CLI is only named in tasks 2.3. The proposal says 'Unknown value refused' returns registry.protocol-mismatch while 'Bad JSON' returns registry.bad-archive; the split is not explained, and the known protocol set is not listed. The hostile tests for the service and handler layers have no named tasks. Resolution: answered: Known protocol set 5.0 and 6.0 is listed, bad JSON and unknown value have separate codes, and tasks name the service and handler hostile tests.
- R2.10 (wrong claim): The ADR check says 'No ADR is touched' with an empty decisions list and no proof. The project rule asks for the quoted decision lines or an index check, and a registry feature with a 'Depends on: artifact-registry' is likely to touch an ADR. The text also copies the stock boilerplate about owner tags. Resolution: answered: The pack adrs list is empty, so adr_check has no entry and the draft changes no owner decision. The considered list now has a question that records this empty list as the proof.
- M.10 (duplicate): The requirement 'Sweep orphaned staging' duplicates the staged file sweep of sweep-staged-files-unreferenced-files. The change also depends on artifact-registry, but the provider code that it changes already exists. Resolution: answered: Removed the Sweep orphaned staging requirement and the depends_on entry on artifact-registry; orphaned staged file cleanup stays with sweep-staged-files-unreferenced-files, and this change lands first with no dependency.

## Open questions

none

Questions considered and answered:

- Use one unpack routine for the h1 hash and the protocol manifest or two routines? The pack states the same unpacking serves the h1 hash and the protocol manifest in issue 1291, so one shared unpack routine answers both needs.
- Count the H1 field in SameContent or in a separate check? Issue 1228 states an H1 field in registry.Archive that SameContent counts, so a republish with changed bytes refuses as changed content.
- Does an ADR answer the unpack limits or the slug derivation rule? The pack adrs list is empty and no ADR text is given, so no quoted decision answers the limit values or the slug rule and both belong in this spec.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
