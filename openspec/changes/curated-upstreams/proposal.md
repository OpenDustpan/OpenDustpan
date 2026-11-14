---
version: 2
---

Roadmap: `docs/roadmap.md:383` (Curated upstreams).

## Why

**Persona: namespace administrator.** A namespace administrator curates which public releases teams can use. Without curation, any public release passes through the registry unchecked. This change adds explicit allow rules so only reviewed upstreams reach stacks and workspaces.

The user path this change must make work:

1. **Docs page.** Turn curation on and list and edit curation rules of the namespace from the docs page.
2. **API.** Turn curation on and list and edit curation rules through the new tool-neutral REST API routes for curation rules and the curation setting.
3. **CLI.** Run tofu init and Pulumi CLI plugin fetch through the registry and tool-compatible fetch endpoints.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| No curation rule allows the requested public release. | 403 with code registry.release-not-allowed naming the registry address; list the rules of the namespace with the curation rules API call. | `GET /api/dustpan/v1/namespaces/{namespace}/curation/rules` |
| The requested registry address has no cached release and the upstream host holds no release. | 404 with code registry.not-found naming the registry address; list the allowed releases of the namespace with the rule list API call. | `GET /api/dustpan/v1/namespaces/{namespace}/curation/rules` |
| The matched upstream host fails with timeout, DNS failure, or 5xx. | 502 with a transient upstream error naming the registry address; retry the fetch after the upstream recovers. | `GET /api/dustpan/v1/namespaces/{namespace}/curation/rules` |

## What Changes

- New route: GET and POST /api/dustpan/v1/namespaces/{namespace}/curation/rules to list and create curation rules, with namespace curation read and manage authorization actions
- New route: PUT and DELETE /api/dustpan/v1/namespaces/{namespace}/curation/rules/{rule} to edit and remove one rule
- New route: GET and PUT /api/dustpan/v1/namespaces/{namespace}/curation to read and set the curation-enabled setting, which defaults to off and shows its effective value
- Rule checks for exact host without subdomain wildcard, address prefix at path boundary, semver constraint, HTTPS-only fetch, and IP literal rejection
- Single-flight upstream fetch with checksum verify, size and time limits, and namespace-keyed cache for allowed OpenTofu provider, OpenTofu module, and Pulumi plugin releases
- Registry fetch gate wired into registry and tool-compatible fetch endpoints that checks curation before cache lookup and serves only a matched public release when curation is on
- Refusal error that names the registry address with the rule list API call as help, plus 404 for missing release and 502 for upstream 5xx, timeout, or DNS failure
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

- artifact-registry-private-provider
- artifact-registry-private-module
- artifact-registry-private-pulumi-plugins

## Usability

Where the path still costs the person time or surprises them:

- A refused fetch names the registry address with the rule list API call, so the person finds the missing rule.
- The rule list shows host, prefix, and constraint for review, which costs time as the rule count grows.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Error code for missing release exists. (`internal/errcode/catalog_registry.go:21 (registry.not-found)`)
- Error code for invalid signature exists. (`internal/errcode/catalog_registry.go:25 (registry.signature-invalid)`)
- Error code for untrusted signature key exists. (`internal/errcode/catalog_registry.go:27 (registry.signature-untrusted-key)`)
- Registry conformance test for provider registry exists. (`pkg/profile/provider/registry_test.go:20 (TestNewRegistry)`)
- Artifact sweep schedule test exists. (`pkg/dustpan/schedules_internal_test.go:537 (TestJobScheduleArtifactSweepRuns)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does PUT of a curation rule need an ETag or version check to detect a lost edit?
   - Options: Keep last-write-wins; Require ETag or version check on PUT of a rule.
   - Provisional decision (owner review 14 Nov): Require ETag or version check on PUT of a rule
   - Reason: AWS practice versions mutable policy documents for concurrent edits. Benchmark: AWS
   - Persona impact: A lost edit upsets a namespace administrator; the ETag option keeps the escape hatch of re-list and retry. Escape hatch: Keep last-write-wins until the check ships; administrators re-list rules after each edit.
2. Does the curation gate need a rate limit for repeated refused fetches?
   - Options: Keep one audit entry per curation decision; Add rate limit or dedupe for refused fetches from one caller.
   - Provisional decision (owner review 14 Nov): Keep one audit entry per decision and add a per-caller rate limit for refusals
   - Reason: SOC 2 practice keeps a complete record while limiting log flooding. Benchmark: SOC 2
   - Persona impact: Flooding refusals upset no persona while kept; a limit keeps the record complete for SOC 2. Escape hatch: Keep one audit entry per decision until a limit ships; the audit log already records each verdict.

Questions considered and answered:

- Which ADRs govern curated upstreams? No ADR in the context pack governs curation. The pack names no ADR and the roadmap step says the curation part of RFC 10 is open, so this change defines the behavior and links the open curation questions below.
- Does curation default to on or off for a new namespace? Off per namespace. The setting is stored with default off and its effective value shows in the API.
- Does curation need a new protocol or a check inside the registry fetch path? A check inside the registry fetch path that also gates the tool-compatible fetch endpoints. The tool protocols stay unchanged.
- In which order do the 403 refusal and the 404 missing release apply, and what happens to old cache entries? The curation check runs before the cache lookup, so a refusal does not reveal whether a release is cached. Cache entries are keyed by namespace ID and re-checked on serve.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
