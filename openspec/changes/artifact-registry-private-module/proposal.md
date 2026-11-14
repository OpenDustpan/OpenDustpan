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
| Module version missing or not served in namespace registry | Server answers 404 with error code module.not-found. Publish the version through the registry publish API, then run `tofu init` again. | `tofu init` |
| Caller without read grant on namespace registry | Server answers 403 with error code registry.forbidden. Ask the namespace administrator for read access, then run `opendustpan status` to check access. | `opendustpan status` |
| Module address names an unknown host or a name outside the registry-safe subset | Server answers 404 with error code module.not-found for a wrong host or a malformed module address. Fix the source address from the docs page, then run `tofu init` again. | `tofu init` |

Evidence for the claims above:

- Providers and modules ship through the tools own protocols and OCI stays out of scope. Proof: docs/adr/0257-the-tools-own-registry-protocols-first-no-oci-endpoint.md decision 1 and decision 2
- Private modules are uploaded through the API with checks at upload, upload comes before git tags, and namespaces never read another namespace. Proof: docs/adr/0269-private-providers-and-modules-are-uploaded-as-the-archive-set.md decisions 2, 6, and 8

## What Changes

- Publish module versions through the reused registry publish API with archive checks at upload.
- Serve discovery with modules.v1 and published module versions through the module registry protocol in the caller namespace.
- Serve module download sources for served versions only with caller auth and without added signature.
- Refuse cross-namespace reads, wrong hosts, bad names, and missing or unserved versions with stable error codes and audit entries.

Out of scope:

- Pulumi plugins
- Yank and revoke of module versions
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

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The tools' own protocols. When OpenDustpan serves providers and modules, it serves them through the HTTP protocols of the tools: the provider network mirror and the module registry protocol for OpenTofu"
- Effect: Change serves private modules through the module registry protocol for OpenTofu. It adds no OCI endpoint.
- Conflict: none.

**ADR 269: Private providers and modules: own providers first, uploaded as the archive set** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current), 6 (current), 7 (current), 8 (current).
- Changed by: ADR 345 changes decision 11. Chain to the end: ADR 345.
- Current rule: this ADR, with ADR 345.
- Quote: "An upload through the API. Issue 949, "Decided", item 10.4:  > private providers and modules are uploaded through the API as the > archive set of ADR 177. Checksums and the signature are checked at > upload."
- Quote: "Address host. A private address has the host of the server and the namespace of OpenDustpan. The adapter refuses every host except the configured host, compared exactly with the configuration."
- Quote: "Only a name in the registry-safe subset can publish: lowercase letters, digits and single dashes, with no dash at the start or the end (RFC 101 question 2)."
- Quote: "No cross-namespace reads. A namespace never reads the registry of another namespace (question 4)."
- Quote: "No re-signing. The server does not sign what it serves (question 5)."
- Quote: "Module sources. Upload first, git tags second (question 7)."
- Effect: Change reuses upload as archive set with checks at upload, server host with namespace and registry-safe name, no cross-namespace reads, no re-signing, and upload-first module sources. It adds the module publish and serve side of that model.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (scope): The roadmap scope says this part publishes and serves private modules. The change serves only and says the publish API is reused from the provider part. Module publish is not specified: no module archive layout, no module-specific upload check, no scenario that a module is accepted or refused at upload. The user path step 1 depends on it. Resolution: answered: Added publish requirement with module archive checks at upload and a publish scenario; user path and tasks now cover publish then serve.
- R1.2 (wrong claim): The error table names `opendustpan whoami`, which does not exist in the CLI. The ADR 345 quote is also not in the ADR file. The proposal itself admits both checks fail, so the gate is not passed. Resolution: answered: Replaced the unknown command with `opendustpan status` and removed the ADR 345 entry that had no decision text in the pack.
- R1.3 (missing scenario): No requirement or scenario covers a version that is not served (unpublished, yanked or revoked): it must not appear in the list or download. The design lists a cached index answer risk, but no requirement, scenario or task covers cache invalidation. The proposal's error table promises module.not-found, but the spec has no missing-version scenario, although task 1.2 tests it. Resolution: answered: Added unserved-version list and download scenarios with module.not-found and a task for index freshness after state change.
- R1.4 (missing scenario): The spec has no scenario for a caller without a read grant (403, registry.forbidden), although the proposal lists this error. It also has no scenario for unauthenticated callers or for a token scoped to another namespace. Resolution: answered: Added forbidden scenario for caller without read grant and unauthenticated scenario with audit entries.
- R1.5 (adr conflict): ADR 269 decision 4 says the adapter refuses every host except the configured host, compared exactly. The spec has no scenario for a wrong host or a malformed or non-registry-safe name. The design mentions parseModuleAddress but there is no test or scenario for it. Resolution: answered: Added wrong-host and malformed-name scenarios with exact host comparison and registry-safe name check.
- R1.6 (failure mode): Cross-namespace refusal as 'deny' may leak whether a module exists in another namespace if the answer differs from not-found. The spec does not say which code it returns or that it must match a missing module. Resolution: answered: Cross-namespace refusal now returns the same 404 module.not-found answer as a missing module.
- R1.7 (open question): The module registry protocol needs a service discovery document (/.well-known/terraform.json with modules.v1). The change does not say who serves it or how it relates to the provider part. Without it tofu init cannot find the routes. Resolution: answered: Added discovery requirement that serves modules.v1 from the same adapter used by tofu init.
- R1.8 (missing scenario): The download requirement does not say how the download is authenticated (tofu sends the token in the header; the X-Terraform-Get redirect target may be on another host or need a signed URL). It also does not cover the integrity and signature check of the archive at serve time, or ADR 269 decision 7 (no re-signing). Resolution: answered: Added download auth with caller token, no re-signing, and serve of stored archive with stored checksum.
- R1.9 (scope): Task 3.1 and task 2.2 require e2e on SQLite and PostgreSQL and over TLS, but no task covers the audit entries for successful reads or for denials, or the authorization action names. The proposal says each automatic action writes a record. Resolution: answered: Added audit requirement for reads and denials and tasks for route actions and audit checks.

## Open questions

none

Questions considered and answered:

- Through which protocol are private modules served? ADR 257 decision 1 answers it: serve modules through the module registry protocol for OpenTofu. No OCI endpoint.
- Does this change add git tag sources for modules? ADR 269 decisions 2 and 8 answer it: upload through the API as the archive set comes first with checks at upload; git tags come second and stay out of scope.
- Does a namespace read modules from another namespace? ADR 269 decision 6 answers it: no; each request reads only the registry of its own namespace, and a cross-namespace address gets the same not-found answer as a missing module.
- Does the server sign a module archive at serve time? ADR 269 decision 7 answers it: no; the server serves the uploaded archive without added signature.
- How are wrong hosts and malformed names handled? ADR 269 decision 4 answers it: the adapter refuses every other host with exact comparison, and only registry-safe names publish.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
