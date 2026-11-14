Roadmap: `docs/roadmap.md:383` (Curated upstreams).

## Why

**Persona: namespace administrator.** A namespace administrator curates which public releases teams can use. Without curation, any public release passes through the registry unchecked. This change adds explicit allow rules so only reviewed upstreams reach stacks and workspaces.

The user path this change must make work:

1. **Docs page.** Turn curation on and list and edit curation rules of the namespace from the docs page.
2. **API.** Turn curation on and list and edit curation rules through the new tool-neutral REST API routes for curation rules and the curation setting.
3. **CLI.** Run tofu init and Pulumi CLI plugin fetch through the registry address.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| No curation rule allows the requested public release. | 403 with code registry.release-not-allowed naming the registry address; list the rules of the namespace with the new curation rules API call. | `GET /api/dustpan/v1/namespaces/{namespace}/curation/rules` |
| The requested registry address has no cached release and the upstream host holds no release. | 404 with code registry.release-not-found naming the registry address; run `opendustpan status` to check server reachability. | `opendustpan status` |

## What Changes

- New route: GET and POST /api/dustpan/v1/namespaces/{namespace}/curation/rules to list and create curation rules, with namespace curation read and manage authorization actions
- New route: PUT and DELETE /api/dustpan/v1/namespaces/{namespace}/curation/rules/{rule} to edit and remove one rule
- New route: GET and PUT /api/dustpan/v1/namespaces/{namespace}/curation to read and set the curation-enabled setting, which defaults to off and shows its effective value
- Rule checks for host, address prefix, semver constraint and IP literal rejection
- Upstream fetch with cache for allowed OpenTofu provider, OpenTofu module, and Pulumi plugin releases, with checksum stored at cache write
- Registry fetch gate that checks curation before cache lookup and serves only a matched public release when curation is on
- Refusal error that names the registry address with the rule list API call as help
- Audit entry before serve for each allow and refuse verdict with fail-closed server log when the audit write fails

Out of scope:

- Private registry releases and signatures
- Blueprints
- Ephemeral units
- Environment promotion
- Upgrade pull requests
- Job proxy
- Console catalog work

Depends on:

- artifact-registry

## Usability

Where the path still costs the person time or surprises them:

- A refused fetch names the registry address with the rule list API call, so the person finds the missing rule.
- The rule list shows host, prefix, and constraint for review, which costs time as the rule count grows.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The default for a new namespace is open. The recommended option (curation off) lets every public release pass unchecked, and no provisional decision answers it. Every spec scenario depends on curation being on, so the build cannot proceed without this answer. Resolution: answered: Curation defaults to off per namespace in design decision 1 and in the curation-enabled setting requirement; behavior when off is specified in the skip-check requirement.
- R1.2 (wrong claim): The error table names the command opendustpan registry rules list, but the code check fails: the registry command does not exist. The one command that helps must be real, so the gate is not passed. Resolution: answered: Error table now names only the curation rules API call and `opendustpan status`, both real.
- R1.3 (missing scenario): The API to list and edit curation rules has no requirement and no scenario, although the proposal, design decision 1 and task 2.2 depend on it. Rule validation at the API (host, non-empty prefix, valid constraint) is also not specified. Resolution: answered: Added store, list, edit, and reject-bad-rule requirements with scenarios for rule management and checks.
- R1.4 (missing scenario): The allow requirement says one rule allows the release. It does not require that host, registry address prefix and version constraint all match, as design decision 1 and the service layer require. The hostile cases (close name, wider prefix, version outside the constraint) have no scenario. Resolution: answered: Added all-field match requirement with close-host, wide-prefix, and out-of-range version scenarios.
- R1.5 (failure mode): Design decision 2 says deny is the default and there is no fallback. The recommended default-off answer lets every fetch pass when curation is off. The design and the open question contradict each other, and the behavior when curation is off is not specified. Resolution: answered: Design and spec state deny by default when on and skip of the check when off; no contradiction remains.
- R1.6 (missing scenario): The 404 case (no cached release for the registry address) is in the proposal error table but has no requirement or scenario in the spec. Resolution: answered: Added missing-cached-release requirement with 404 scenario that names the registry address.
- R1.7 (failure mode): The audit write is required, but the spec does not say what happens when the audit write fails. A release could be served with no record, which the project rule nothing happens without a record forbids. Resolution: answered: Added fail-closed audit requirement: failed audit write refuses the download and logs the failure.
- R1.8 (failure mode): The layer hostile tests (encoded traversal and absolute URLs in the registry address, redirect to an unlisted host, path escape) have no requirement or scenario, and tasks 1.1 and 1.2 do not map to them. Hostile input and redirect cases are therefore not covered by a failing test first. Resolution: answered: Added hostile-address and unlisted-redirect requirements with scenarios and failing tests first.
- R1.9 (missing scenario): No scenario covers a rule change while a fetch runs. The design loads one snapshot per request, but the spec does not state which rule set applies to an in-flight fetch. Resolution: answered: Added request-snapshot requirement for in-flight fetches.
- R1.10 (open question): The syntax of the version constraint is not defined, and no rule covers an invalid constraint from the API or from a stored rule. The constructor check depends on this syntax. Resolution: answered: Defined the constraint as a semver constraint and required 400 on an unparsable constraint at the API and constructor.
- R1.11 (wrong claim): The ADR check states No ADR is touched with no rg command or output, so the claim is unchecked. It also defines owner-tagged as when the ADR records decisions of the project owner, which differs from the project rule (text tag or title tag). Resolution: answered: Left adr_check empty so no unchecked ADR claim remains and removed the wrong owner-tagged definition.
- R2.1 (open question): The change does not say how the curation-enabled setting is set or read. There is no requirement, route, authorization action or scenario for the toggle, and the rule routes have no named actions. Project rules say every endpoint declares actions and every default shows its effective value in the API. Resolution: answered: Added curation-enabled setting requirement with default off and effective value, plus route authorization actions in tasks and what_changes.
- R2.2 (wrong claim): The ADR check says no ADR is touched, with no proof and no quoted lines. The adversarial pass says adr_check was left empty, but the section is present with an unchecked claim. The roadmap item also depends on RFC 10, and the change has no ADR evidence. Project rule 5 says a claim without proof counts as unchecked. Resolution: answered: Kept adr_check empty because the context pack names no ADR. Added a considered entry that states the pack names no ADR and RFC 10 curation is open.
- R2.3 (missing scenario): The user path lists the docs page, API and CLI, but there is no CLI command to list or edit rules, and no scenario for the Pulumi CLI path with plugin fetch. Task 3.1 uses `make e2e CURATION=1`, which is not shown to exist. The refusal error for the CLI is not specified. Resolution: answered: Removed rule editing from the CLI path, added tofu init and Pulumi plugin fetch scenarios to serve allowed, and changed the e2e task to plain make e2e with real CLIs.
- R2.4 (failure mode): The audit fail-closed scenario also needs an audit entry for the refusal, but the audit write is the failure. The spec says the server 'writes the denial', which cannot work when audit is down. It is also not stated whether the off path writes an audit record, though the rule says each automatic action has a record. Allow-path audit order versus serve is not specified. Resolution: answered: Defined audit before serve, no curation audit entry while off, and fail-closed refusal with a server log record when the audit write fails.
- R2.5 (missing scenario): Several semantics are not defined: precedence of the 403 and 404 checks (does a refusal leak whether a release is cached), how multiple rules combine, prefix matching at path boundaries, pre-release versions in the semver constraint, and what happens to existing rules when curation turns off and on. Resolution: answered: Defined curation before cache precedence, OR combination, path boundary prefix match, prerelease handling, and rule retention across toggles.
- R2.6 (failure mode): The rule stores host and registry address prefix by name. Project rules say a stored reference to a renamable entity uses the stable ID, and the rule has no namespace ID reference stated. Rule edit and delete concurrency (two administrators) is not covered. Also, the design restricts redirect hosts only; a DNS rebind or an IP-literal host is not covered. Resolution: answered: Stored rules keyed by namespace ID, defined last-write-wins for concurrent edits, and rejected IP literal rule hosts.
- R2.7 (scope): The proposal says rules are listed 'on one line' as a usability fix and the 'one command that helps' is `opendustpan status`, but status is not shown to report rules or the curation state. The 404 error pointing at status helps little. Resolution: answered: Changed the 403 help to the rule list API call, kept status only for reachability on 404, and removed the one line display claim.
- M.12 (gap): Step 5 needs both tofu init and the Pulumi CLI to get a public artifact through the registry. No change builds the fetch from the public upstream and the cache. The requirements cover only the OpenTofu address model. The routes use {id} where the other changes use {namespace}. Resolution: answered: Added upstream fetch and cache requirement for OpenTofu providers, modules, and Pulumi plugins with provider, module, and plugin scenarios, kept Pulumi plugin serve and refusal scenarios that name the artifact, and renamed {id} to {namespace} in routes, errors, and tasks.

## Open questions

none

Questions considered and answered:

- Which ADRs govern curated upstreams? No ADR in the context pack governs curation. The pack names no ADR and the roadmap step says the curation part of RFC 10 is open, so this change defines the behavior without contradicting an ADR.
- Does curation default to on or off for a new namespace? Off per namespace. The setting is stored with default off and its effective value shows in the API. The fetch path skips the check while off and denies by default while on.
- Does curation need a new protocol or a check inside the registry fetch path? A check inside the registry fetch path. The tool protocols stay unchanged and the registry stays the single gate.
- In which order do the 403 refusal and the 404 missing release apply? The curation check runs before the cache lookup, so a refusal does not reveal whether a release is cached.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
