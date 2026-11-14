---
version: 2
---

Issue: #1010, #1014, #1144.

## Why

**Persona: developer.** A developer keeps one set of config and secrets in a profile and opens it from a workstation or a tool. Today the open, the session read, and the docs are missing, so each tool keeps its own copy.

The user path this change must make work:

1. **API.** Open a profile with paths, consumer, mode, and duration; replay with the same Idempotency-Key.
2. **API.** Read the session values or one path with the same credential.
3. **Docs page.** Follow docs/reference/profiles-api.md to open and read a session.
4. **E2e.** Run the e2e profiles step against the real server.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The caller lacks profile:open on the root or an imported profile | 403 that names the first unreadable profile; a session read after revoke answers 403 and deletes the session. | `curl -X POST /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open` |
| The open reaches a provider block before the provider port lands | 422 profile.ErrProvider naming the block; an open of a static profile with no blocks succeeds. | `curl /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}` |
| The OpenTofu data source asks a never-store leaf | 400 naming the path and the ephemeral resource. | `tofu plan` |

Evidence for the claims above:

- Profiles slice 1 exists with service, store, routes, and reference docs. Proof: pkg/profile, internal/api/profile, docs/reference/profiles-api.md
- Main has no Service.Open. Proof: rg -n -F --glob '!*_test.go' 'Service.Open' pkg internal cmd matches 0 in the code facts of the pack (the only Service.Open is the forge one in pkg/forge/service.go:332)
- The slice 1 neverStore rule is in the profile document check. Proof: pkg/profile/document_check.go
- The audit file is the place of the audit kinds that an open needs. Proof: pkg/audit/audit.go

## What Changes

- Service.Open for documents with no provider blocks, with import merge, $final, checks, pinned Expr values, and mark propagation with $neverStore on any value
- Sessions port with Put, Get, Sweep in both dialects, sealed with the root data key, with limits, Sweep, backup exclusion
- New route POST /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open with profile:open and Idempotency-Key replay holding only the session ID
- New route GET /api/dustpan/v1/namespaces/{namespace}/profile-sessions/{session} with action recheck and delete on refusal
- One audit entry of kind access for each open and each refused open, with no value
- Docs page for open and sessions

Out of scope:

- Provider blocks, provider port, rotators, and login blocks
- OpenTofu provider build, Pulumi env routes, job-token pin rules, and login leaves
- Job opens, attachments, launcher delivery, and gates at profile.open

## Usability

Where the path still costs the person time or surprises them:

- A refused path names the first unreadable profile.
- A provider block names itself.
- A never-store refusal names the path and the ephemeral resource.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- pkg/profile holds the profile domain (`pkg/profile/service.go:542`)
- The Sessions type in main is the console session (`pkg/session/service.go:64`)
- Backup has an emptied tables list (`pkg/backup/coverage.go:76`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 200: Profiles: config, secrets and credentials for every tool** (owner-tagged: decision 1).

- Decisions that apply: 1 (current).
- Changed by: ADR 210 changes decision 9; ADR 222 changes decision 3; ADR 224 changes decision 3; ADR 225 changes decision 3; ADR 286 changes; ADR 347 extends decision 10. Chain to the end: ADR 210, ADR 222, ADR 224, ADR 225, ADR 286, ADR 347, ADR 247, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 299, ADR 248, ADR 343, ADR 319, ADR 365, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 210, 222, 224, 225, 286, 347.
- Quote: "A domain package `pkg/profile` (owner). The package is tool-neutral. It imports no tool package, no store adapter and no transport. No tool package imports it."
- Effect: Open and Sessions live in pkg/profile as tool-neutral code.
- Conflict: none.

**ADR 201: The profile document, imports and expressions** (not owner-tagged).

- Decisions that apply: 4 (changed), 12 (current).
- Changed by: ADR 222 changes decisions 4, 8 and 11; ADR 224 supersedes decision 11; ADR 226 changes decision 5; ADR 228 changes decisions 4 and 8; ADR 241 extends decision 7; ADR 286 changes. Chain to the end: ADR 222, ADR 224, ADR 226, ADR 228, ADR 241, ADR 286, ADR 335, ADR 351, ADR 354, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 222, 224, 226, 228, 241, 286.
- Quote: "- **never store**: from `$neverStore` or a block with `neverStore: true`."
- Quote: "Expressions are Expr. A string that is exactly `${ <expression> }` gives the value of the expression, of any type."
- Effect: The open evaluates ${ } and propagates marks; $neverStore stands on any value.
- Conflict: none.

**ADR 203: Opening a profile: sessions, attachments, delivery, actions and audit** (not owner-tagged).

- Decisions that apply: 1 (extended), 3 (current), 9 (current).
- Changed by: ADR 210 changes decision 12; ADR 222 changes decision 5; ADR 225 changes decision 5; ADR 241 extends decision 1; ADR 286 changes; ADR 300 changes decision 5; ADR 353 extends decision 1. Chain to the end: ADR 210, ADR 222, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 247, ADR 335, ADR 351, ADR 354, ADR 299, ADR 248, ADR 313, ADR 343, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 210, 222, 225, 241, 286, 300, 353.
- Quote: "Open sessions. An open with the consumer `api`, `console` or `pulumi` stores its result as an **open session** with an ID of the prefix `pos-`."
- Quote: "The audit entry of an open. Each open writes one audit entry of the kind `access`, with the action `profile:open` and the consumer:"
- Effect: The change implements the open request, open sessions for api, console and pulumi, and one audit entry of kind access with no value.
- Conflict: none.

**ADR 209: How each tool reads a profile** (owner-tagged: decision 3).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "It is the only way for OpenTofu to read a value marked never store."
- Effect: The server refuses never-store leaves for consumer pulumi and for data delivery.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: route `/api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open` (.errors[0].command) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: task "Add the e2e profiles step: open, read the session, replay the key." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph; a code check fails: and 2 more.

Questions considered and answered:

- Which audit kind does an open write? Kind access, per ADR 203 decision 9. pkg/audit/audit.go lacks it, so the change adds it. ADR 240 covers state downloads only.
- Does this slice evaluate ${ }? Yes. ADR 201 decision 4 fixes Expr as the language and ADR 228 limits it to values.
- Does neverStore need secret or sealed? No. ADR 201 decision 12 allows it on any value; the slice 1 rule in document_check.go is corrected.
- Who refuses never-store for the data source? The server, through a mode field data or ephemeral in the open request, so an old provider build cannot skip it (ADR 209 decision 3).
- Can expressions decide access? No. ADR 228 decision 1 says Expr computes values only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
