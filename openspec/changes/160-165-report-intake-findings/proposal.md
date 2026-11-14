---
version: 3
---

## Why

**Persona: runner operator.** A runner operator runs a pool for sweeps. The launcher must stream a sweep report to the server under its claim. The server must accept only a valid version 1 report inside the target scopes. A namespace administrator then lists and triages the findings.

The user path this change must make work:

1. **API.** The launcher streams a version 1 JSON Lines report to the NEW runner protocol path `POST /runner/v1/session/sweep-report` with a signed session proof and the header `Dustpan-Content-SHA256`.
2. **API.** A namespace administrator lists and reads findings with the NEW routes under `/api/dustpan/v1/namespaces/{namespace}/findings` and an access token.
3. **API.** The administrator sets triage on a finding with a reason through the NEW PATCH route `/api/dustpan/v1/namespaces/{namespace}/findings/{id}`.
4. **Docs page.** The administrator reads the NEW guide page for the report format, the bounds, the subset rule and triage.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Report with a bad header, a bad line or a scope outside the target. | 400 with the NEW error code `unmanaged.bad-report` or `unmanaged.scope-not-in-target`. The message names the line number and stores nothing. | `GET /api/dustpan/v1/namespaces/{namespace}/findings` |
| Report above a bound. | 413 with the NEW error code `unmanaged.report-too-large`. The message names the bound. The sweep ends failed and the staged blob is deleted. | `GET /api/dustpan/v1/namespaces/{namespace}/findings` |
| Caller without the grant tries to triage. | 403 with the NEW error code `unmanaged.forbidden`. Nothing changes. | `GET /api/dustpan/v1/namespaces/{namespace}/findings` |

Evidence for the claims above:

- Main has context routes and the pack lists no findings route. Proof: The pack lists ANY/DELETE/GET/PATCH /api/dustpan/v1/namespaces/{}/context and no findings path.
- Main keeps context error codes in a catalog file. Proof: internal/errcode/catalog_context.go:7

## What Changes

- The NEW runner protocol path `POST /runner/v1/session/sweep-report` streams a report, checks it, and stores it encrypted.
- The NEW findings routes list, get and triage findings.
- The NEW error codes `unmanaged.bad-report`, `unmanaged.report-too-large`, `unmanaged.scope-not-in-target`, `unmanaged.forbidden` and `unmanaged.finding-not-found`.

Out of scope:

- Sweep targets and the sweep states and claim (part 2).
- Schedule worker and daily state check (part 4).
- Console pages.

Depends on:

- 130-138-source-filter-sweeps
- 160-165-sweep-targets-lifecycle
- `160-165-sweep-targets-lifecycle` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A report error names the line number and the bound.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Context error codes live in a catalog file. (`internal/errcode/catalog_context.go:7 (func catalog_context)`)
- The API description adds error codes. (`internal/api/apidesc/document.go:96 (func WithErrorCodes)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 10 (current), 11 (current), 12 (current), 13 (current), 17 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "Bounds. A report is at most 1 GiB and 5,000,000 records. A line is at most 64 KiB. A record has at most 50 tags, with a key of at most 128 bytes and a value of at most 256 bytes. A header has at most 1,000 scopes."
- Quote: "Upload. The runner streams the report to the server through the runner protocol, under its claim."
- Effect: Follows the format, bounds, runner protocol upload and subset match.
- Conflict: none.

**ADR 131: The runner protocol** (not owner-tagged).

- Decisions that apply: 10 (current), 13 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Limits. A JSON body has at most 4 MiB."
- Effect: The upload uses a signed hash header and a replay nonce on the runner path.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The upload stays under /runner/v1.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the upload path live? ADR 164 decision 12 and ADR 210 decision 2: the runner protocol, not REST.
- Does a later sweep reopen a triaged finding? No. Deduplication is by namespace ID and resource key. A later sweep updates the last time seen and never changes triage.
- Which pool runs sweeps? Owner decision, 13 Nov 2026 (queue P14): only an operator pool has the sweep purpose; this change adds no pool rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
