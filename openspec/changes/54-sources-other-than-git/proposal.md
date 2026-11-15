---
version: 2
---

## Why

**Persona: developer.** Developer keeps code outside git and wants a run from that source with the same record as a git run.

The user path this change must make work:

1. **API.** Namespace administrator opens the source filter of the namespace to see which sources the server accepts.
2. **Docs page.** Developer reads the docs page to learn that git is the only accepted source until RFC 54 is accepted.
3. **Console.** Namespace administrator writes a source value outside the allowlist and sees the refusal in the console.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Namespace administrator writes a source filter value the server does not allow. | Server answers 400 with error code source.filter-invalid and changes nothing. | `PUT /api/dustpan/v1/namespaces/{namespace}/source-filter` |

Evidence for the claims above:

- Main has a source filter error catalog. Proof: internal/errcode/catalog_source.go:8
- Main has a namespace source-filter route. Proof: ANY/GET/PUT /api/dustpan/v1/namespaces/{}/source-filter

## What Changes

- Name RFC 54 as a later change that waits on runs and fixes no source kind yet.

Out of scope:

- Run model, job kinds, executors, and triggers
- Forge connections and webhook handling
- Kubernetes operator resources

Depends on:

- none

Builds on (not a change):

- runs (milestone 5)

## Usability

Where the path still costs the person time or surprises them:

- Developer cannot tell from the docs which non-git sources work; the page must name the allowlist once the RFC fixes it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main reports an invalid source filter with a stable code. (`internal/errcode/catalog_source.go:8 (source.filter-invalid)`)
- Main reports an unreadable source filter with a stable code. (`internal/errcode/catalog_source.go:11 (source.filter-unreadable)`)
- Main covers the portable source filter in a backup test. (`pkg/backup/portable_test.go:489 (TestReadPortableSourceFilter)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 13 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A source in the `spec`: a Git repository, a Flux source, or an inline program."
- Quote: "A resource kind declares a unit in tool-neutral terms: the source, the inputs, the placement, the apply policy and a `tool` field."
- Effect: Decision 2 names Git, Flux source, or inline program as the source in the spec of an operator resource only. Decision 13 names a source field on a tool-neutral resource kind only. This change adds no source kind and changes no operator word.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which source kinds beyond git does RFC 54 cover?
   - Options: S3 or OCI bundle; Flux source; inline program; upload.
   - Provisional decision (owner review 14 Nov): Decide the allowlist in the RFC after runs land, with no kind fixed by this change.
   - Reason: A fixed allowlist keeps supply-chain checks exact and reviewable. Benchmark: AWS CodeBuild keeps an explicit allowlist of source kinds per project and pins each build to one revision.
   - Persona impact: Developer waits for a supported non-git source; git keeps working so no path is lost. Escape hatch: Keep git as the only accepted source until the RFC is accepted.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).
2. How is a non-git source pinned and recorded for audit?
   - Options: digest pin for every non-git source; tag or branch with a recorded digest; no pin.
   - Provisional decision (owner review 14 Nov): Require a digest pin and record source, revision, and principal in the audit entry.
   - Reason: A digest pin makes each run repeatable and reviewable after the run ends. Benchmark: SOC 2 keeps a digest pin and an audit record of source, revision, and principal for each run.
   - Persona impact: Namespace administrator gets repeatable runs; pinning costs one extra lookup. Escape hatch: Pin to a commit digest for git and refuse mutable refs where risk is high.
3. Which action guards a non-git source, does the source filter apply to it, and where does its credential live?
   - Options: reuse the source-filter route and its checks for non-git sources; new per-unit source setting with its own action; no stored credential; runner supplies the source.
   - Provisional decision (owner review 14 Nov): Decide the action, filter scope, and credential store in the RFC after runs land.
   - Reason: One named action, one filter scope, and one credential store keep the check exact. Benchmark: ISO 27001 guards each source change with an authorization check at the point of use and keeps credentials in a stored secret.
   - Persona impact: Namespace administrator sees one clear place to allow a source; an extra approval step is the cost. Escape hatch: Refuse non-git sources until the action, filter scope, and credential store are decided.

Questions considered and answered:

- Does an ADR fix the set of sources other than git? No. ADR 305 decision 2 names a Git repository, a Flux source, or an inline program as the source in the spec of an operator resource. Decision 13 names a source field on a tool-neutral resource kind. Neither fixes the allowlist of sources for runs. No other ADR in the pack fixes a non-git source kind.
- What does this change wait on? Runs in milestone 5. The roadmap row for RFC 54 waits on runs, so this coarse change specifies no run, job, executor, or trigger behavior.
- Does an ADR say how a non-git source is authorized, filtered, or credentialed? No. The pack holds a source filter port and route plus two stable error codes, and no decided rule that the filter applies to non-git sources, which action guards a non-git source, or where a source credential lives. Those stay open questions for the RFC.
- Does a source change need a fence rule? No. Fencing and the failover version guard writes in a namespace. This coarse change makes no write-path change, so no fence rule changes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
