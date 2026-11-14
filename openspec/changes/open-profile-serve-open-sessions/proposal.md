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
| Caller lacks profile:open on root or imported profile | 403 profile:open denied naming the first unreadable profile; a session read after revoke answers 403 and deletes the session. | `curl /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open` |
| Open reaches a provider block before the provider port lands | 422 profile.ErrProvider naming the block; an open of a static profile with no blocks succeeds. | `curl /api/dustpan/v1/namespaces/{namespace}/profile-sessions/{session}` |
| OpenTofu data source asks a never-store leaf | 400 naming the path; the data source never answers never-store leaves and names the ephemeral resource. | `tofu plan` |

Evidence for the claims above:

- Profiles slice 1 exists with service, store, routes, and reference docs. Proof: pkg/profile, internal/api/profile, docs/reference/profiles-api.md
- Main has no Open and no resolve. Proof: rg -n -F 'Service.Open' pkg internal cmd matches 0
- Slice 1 neverStore rule needs secret or sealed. Proof: pkg/profile/document_check.go
- Audit kinds in main lack the access kind for opens. Proof: pkg/audit/audit.go

## What Changes

- Service.Open for documents with no provider blocks, with import merge, $final, checks, pinned Expr values, and mark propagation with $neverStore on any value
- Sessions port with Put, Get, Sweep in both dialects, sealed with the root data key, with limits, sweep, backup exclusion, and rotation refusal
- New route POST /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open with profile:open, mode data or ephemeral, and Idempotency-Key replay holding only the session ID
- New route GET /api/dustpan/v1/namespaces/{namespace}/profile-sessions/{session} with session recheck and delete on refusal
- One audit entry of kind access in the same transaction for each open and each refused open with no value
- Docs page for open and sessions

Out of scope:

- Provider blocks, provider port, rotators, and login blocks
- OpenTofu provider build, Pulumi env paths, job-token pin rules, and login leaves
- Issuer paths, job opens, attachments, and approval gates at profile.open beyond the stored check

## Usability

Where the path still costs the person time or surprises them:

- A refused path names the first unreadable profile and the grant that fixes it.
- A provider block names itself instead of a generic open failure.
- A never-store refusal names the path and the ephemeral resource to use.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 200: Profiles: config, secrets and credentials for every tool** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: ADR 210 changes decision 9; ADR 222 changes decision 3; ADR 224 changes decision 3; ADR 225 changes decision 3; ADR 286 changes; ADR 347 extends decision 10. Chain to the end: ADR 210, ADR 222, ADR 224, ADR 225, ADR 286, ADR 347, ADR 247, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 299, ADR 248, ADR 343, ADR 319, ADR 365, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 210, 222, 224, 225, 286, 347.
- Quote: "A domain package `pkg/profile` (owner). The package is tool-neutral. It imports no tool package, no store adapter and no transport. No tool package imports it."
- Effect: Open and Sessions live in pkg/profile as tool-neutral code with Store and Sessions ports; no tool package imports profile.
- Conflict: none.

**ADR 201: The profile document, imports and expressions** (not owner-tagged).

- Decisions that apply: 3 (current), 4 (changed), 6 (current), 12 (current), 13 (current).
- Changed by: ADR 222 changes decisions 4, 8 and 11; ADR 224 supersedes decision 11; ADR 226 changes decision 5; ADR 228 changes decisions 4 and 8; ADR 241 extends decision 7; ADR 286 changes. Chain to the end: ADR 222, ADR 224, ADR 226, ADR 228, ADR 241, ADR 286, ADR 335, ADR 351, ADR 354, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 222, 224, 226, 228, 241, 286.
- Quote: "Marks propagate. Each value has two marks:"
- Quote: "- **never store**: from `$neverStore` or a block with `neverStore: true`."
- Quote: "Expressions are Expr. A string that is exactly `${ <expression> }` gives the value of the expression, of any type."
- Effect: The open merges imports, honors $final, evaluates ${ } with pinned Expr, propagates secret and never-store marks where $neverStore stands on any value, and applies document limits. The never-store refusal for consumer pulumi and data delivery follows ADR 209, not a change to this ADR.
- Conflict: none.

**ADR 203: Opening a profile: sessions, attachments, delivery, actions and audit** (not owner-tagged).

- Decisions that apply: 1 (extended), 2 (current), 3 (current), 4 (current), 9 (current), 12 (changed).
- Changed by: ADR 210 changes decision 12; ADR 222 changes decision 5; ADR 225 changes decision 5; ADR 241 extends decision 1; ADR 286 changes; ADR 300 changes decision 5; ADR 353 extends decision 1. Chain to the end: ADR 210, ADR 222, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 247, ADR 335, ADR 351, ADR 354, ADR 299, ADR 248, ADR 313, ADR 343, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 210, 222, 225, 241, 286, 300, 353.
- Quote: "The open request. `profile.Service.Open` takes an actor, the profile with an optional `@tag`, `@number` or `@draft:<ID>`, the paths to answer (all paths by default), the consumer and the duration of the session. The consumer is one of `api`, `console`, `run` (the run command), `pulumi`, `opentofu`, `launcher` and `observe`."
- Quote: "Open sessions. An open with the consumer `api`, `console` or `pulumi` stores its result as an **open session** with an ID of the prefix `pos-`."
- Quote: "The audit entry of an open. Each open writes one audit entry of the kind `access`, with the action `profile:open` and the consumer:"
- Effect: The change implements the open request with consumer and delivery mode, the resolve without provider blocks, open sessions, the direct-open check, one audit entry of kind access with no value, and the open routes.
- Conflict: none.

**ADR 209: How each tool reads a profile** (owner-tagged: decision 3, 5).

- Decisions that apply: 2 (current), 3 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Never store, and the ephemeral resource (owner). `ephemeral "opendustpan_profile"` has the same inputs and outputs. OpenTofu keeps an ephemeral value out of the plan and the state. It is the only way for OpenTofu to read a value marked never store."
- Effect: Consumer pulumi and data delivery refuse never-store leaves on the server so an old provider build cannot skip the check; the Pulumi env paths stay out of this slice.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: New tool-neutral routes use the prefix and style; the open replay record holds only the session ID.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Six decision points (owner). `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2)."
- Effect: Profile open stays a decision point profile.open; this change does not alter gate behavior.
- Conflict: none.

**ADR 228: Expr computes values only** (owner-tagged: decision 1, 3).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Values only (owner). Expr computes values: a profile value in `${ }` and the `unitName` of a blueprint."
- Effect: Expr computes open values only with the one builtin set; no access decision uses Expr.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The change leaves open which audit kind an open writes (access per ADR 203 decision 9, or reveal/tool-operation per ADR 240); no ADR answer is given and the audit code has no access kind. The change cannot be built without this answer. File: proposal.md, Open questions 1. Resolution: answered: Spec profile-open now writes kind access with no value; audit scenario covers allowed and refused opens.
- R1.2 (open question): Whether ${ } expressions are evaluated (requires adding the pinned Expr dependency) or refused is undecided; merge, marks and checks depend on it. File: proposal.md, Open questions 2 and tasks.md 2.1. Resolution: answered: Design and spec now add the pinned Expr and evaluate ${ }; tasks add Expr table tests.
- R1.3 (owner decision conflict): The spec and design keep the slice 1 rule that neverStore needs secret or sealed, while ADR 201 decision 12 allows neverStore on any value; this is an open owner question (Open questions 3) that the design takes silently. File: design.md Decisions 1 and proposal.md Open questions 3. Resolution: answered: Design and spec now allow $neverStore on any value per ADR 201 decision 12.
- R1.4 (adr conflict): ADR 201 decision 12 says marks propagate and never-store comes from $neverStore on any value, while the change relies on the slice 1 secret-or-sealed rule; the conflict with the ADR decision line is not resolved in the ADR check which says 'Conflict: none'. File: proposal.md ADR check. Resolution: answered: ADR check for ADR 201 now states the ADR rule and follows it; conflict stays none.
- R1.5 (wrong claim): The proposal says the open routes are in the route table, but the code check reports that the routes /profiles/{profile}/open and /profile-sessions/{session} are not in the API description or route table; the spec is not spec-ready. File: proposal.md (Not spec-ready note) and tasks.md 2.3. Resolution: answered: what_changes now names both routes as new; route test task covers the table and %2F cases.
- R1.6 (failure mode): Session sealing with the root profile data key is specified, but no requirement covers key rotation or a revoked key, so sessions may stay readable after a key change. File: design.md Decisions 2 and specs/profile-sessions/spec.md. Resolution: answered: Sessions lifecycle requirement now states a rotated or revoked data key makes old sessions unreadable, with a scenario.
- R1.7 (missing scenario): No scenario covers a job-token open or a session read by a different principal with the same ID; the spec only tests the same credential and a revoked caller. File: specs/profile-sessions/spec.md. Resolution: answered: Session read requirement now covers job-token opens and reads by another principal.
- R1.8 (failure mode): Concurrent replays of the same Idempotency-Key racing with a first open are not specified; two sessions could be stored. File: specs/profile-open/spec.md Idempotent open replay. Resolution: answered: Idempotent replay requirement now states concurrent replays store one session and answer one ID.
- R1.9 (missing scenario): The resolve limits of ADR 201 decision 13 (64 revisions, 128 provider calls, 60 seconds, 4 MiB result) have no scenario; a limit breach is not tested. File: specs/profile-open/spec.md. Resolution: answered: Open limits requirement now states 64 revisions, 128 provider calls, 60 seconds, and 4 MiB with a breach scenario.
- R1.10 (scope): The sweep schedule and the session exclusion from backups are in tasks but no spec scenario states the sweep behavior on restart or a failed sweep. File: tasks.md 2.2 and specs/profile-sessions/spec.md. Resolution: answered: Sessions lifecycle requirement now states sweep deletes ended sessions and backups hold no sessions, with scenarios.
- R1.11 (open question): Whether the Pulumi consumer refusal of never-store leaves and the OpenTofu server-side refusal (open question 4) are decided; the spec names only the pulumi case. File: proposal.md Open questions 4. Resolution: answered: Spec now refuses never-store leaves for consumer pulumi and for data-source mode on the server.
- R2.1 (adr conflict): The ADR check for ADR 201 says Conflict: none, but the change's spec still refuses never-store on pulumi and data modes while proposal/design claim neverStore on any value; the quote 'Marks propagate' is truncated and the decision 12 text on $neverStore is not quoted, so the claimed agreement is unsupported. File: proposal.md ADR check (ADR 201). Resolution: answered: ADR 201 entry now quotes the never-store line and the Expr line verbatim and lists decisions 3, 4, 6, 12, 13; effect states the pulumi and data refusal follows ADR 209.
- R2.2 (open question): Issue #1010 open question 3 (slice 1 $neverStore deviation) is answered in the proposal only by assertion; the design says the strict secret-or-sealed rule is a deviation that this change follows the ADR on, but no owner decision is recorded, and ADR 201 decision 12 is cited only as 'any value'. The change takes a deviation from the slice 1 rule silently. File: design.md Decisions 1, proposal.md Open questions. Resolution: answered: Considered states $neverStore on any value per ADR 201 decision 12 and the slice 1 strict rule in document_check.go as a code deviation corrected here; no owner change to ADR words.
- R2.3 (open question): Issue #1010 open question 2 asks whether the open request names data or ephemeral so the server refuses; the change refuses never-store for 'data-source mode' on the server without a field in the open request, so the server cannot know the mode. The spec and API request do not define how the server learns data-source mode. File: specs/profile-open/spec.md, Never-store for pulumi and data. Resolution: answered: Design, specs, and tasks now define open mode data or ephemeral in the request and server-side refusal for mode data.
- R2.4 (owner decision conflict): Issue #1010 lists the audit kind as an owner decision (access vs reveal/tool-operation). The proposal answers 'kind access' by quoting ADR 203 decision 9, but audit.go has no access kind and ADR 240 assigns reveal and tool-operation to downloads; the owner question remains unanswered in the change. File: proposal.md Open questions / ADR check. Resolution: answered: Considered keeps kind access per ADR 203 decision 9 in the same transaction; evidence cites pkg/audit/audit.go as the gap to close; ADR 240 covers downloads only.
- R2.5 (wrong claim): The proposal's Evidence section says 'Audit kinds in main lack the access kind', but the Adversarial pass R1.5 says the routes are missing from the route table; the proposal's Why still lists the open and session routes as existing behavior in the error table. The claim that the routes are in the route table is not supported. File: proposal.md Why error table. Resolution: answered: Evidence no longer claims routes exist; what_changes states both routes are new and tasks cover the route table with %2F cases.
- R2.6 (failure mode): Session read after revoke deletes the session, but the replay of Idempotency-Key for a deleted or refused session is not specified; a replay may return a dead session ID. File: specs/profile-open/spec.md Idempotent open replay. Resolution: answered: Idempotent replay requirement now states replay after delete or refusal answers the stored ID while the session read answers 404 or 403.
- R2.7 (missing scenario): No scenario covers a sweep failing, or a crash between session put and the audit entry; ADR 203 says the audit entry is written in the transaction that records the open, but the spec does not require it. File: specs/profile-sessions/spec.md Bound session lifecycle. Resolution: answered: Open scenario now states the audit entry is written in the same transaction as the open and a crash writes neither.
- R2.8 (failure mode): Data-key rotation: the design seals sessions with the root profile data key, but the spec does not say what happens to sessions of an imported profile whose key rotates while the root key does not. File: design.md Decisions 2. Resolution: answered: Sessions design and scenario now state sealing uses only the root data key so imported-profile rotation changes nothing.
- R2.9 (scope): Tasks 2.1 implement ErrNeverStore for consumer pulumi, but issue #1010 places the Pulumi CLI paths out of scope; the change names the consumer without defining the pulumi open path. File: tasks.md 2.1. Resolution: answered: Considered and out_of_scope state consumer pulumi is a plain field value and Pulumi env paths stay out of scope.
- R2.10 (open question): Expr dependency addition is decided in the change ('Add the pinned Expr now') although CLAUDE/project.md says do not add a dependency without the owner; no owner decision is cited. File: proposal.md Questions considered. Resolution: answered: Considered states the pinned Expr is the dependency that ADR 201 decision 4 already needs with ADR 228 values-only limits.
- M.40 (gap): No change opens the profile in a job. The tool read per ADR 209, the masking of profile secrets in the job log and the revision for the next job of step 4 have no owner. Resolution: answered: Job opens stay out of scope of this slice; out_of_scope names job opens, attachments, and launcher delivery, and the job-service-unit-settings-pool change owns the job open, log masking, and fixed-revision e2e for both tools.

## Open questions

none

Questions considered and answered:

- Does the open need new behavior beyond ADRs 200, 201, 203, 209 and 228? No. ADR 203 decisions 1 to 4, 9 and 12 fix request, resolve, sessions, direct-open check, audit kind access, and routes; ADR 201 decisions 4 and 12 fix Expr and neverStore on any value; ADR 228 fixes Expr values only; ADR 209 decision 3 fixes the never-store refusal.
- Can expressions decide access? No. ADR 228 decision 1 says Expr computes values only and never decides whether a change may go on.
- Does this change ship the OpenTofu provider build or the Pulumi env routes? No. The OpenTofu provider build and the Pulumi env routes stay out until the open exists; this slice serves only the tool-neutral open and session read with consumer pulumi or opentofu as a plain field value.
- Which audit kind does an open write? Kind access for each open, written in the same transaction as the open. ADR 203 decision 9 fixes kind access with no value. The missing access kind in pkg/audit/audit.go is a gap to close, not a new decision; ADR 240 covers state downloads, not profile opens.
- Does this slice evaluate ${ } or refuse it? Evaluate ${ } with the pinned Expr now. ADR 201 decision 4 fixes Expr as the expression language, so this is the dependency that the ADR already needs; a refusal would break merge, marks, and checks.
- Does neverStore need secret or sealed? Any value. ADR 201 decision 12 allows $neverStore on any value; the slice 1 strict rule needs secret or sealed only in pkg/profile/document_check.go and is a code deviation that this change corrects to the ADR.
- How does the server learn data-source mode? The open request names the delivery on the server with a mode field data or ephemeral alongside the consumer, so an old provider build cannot skip the check. This answers issue 1010 question 2 with ADR 203 decision 1 plus ADR 209 decision 3.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
