Issue: #1639.

## Why

**Persona: developer.** A developer reads outputs of another unit through the server. When the server refuses the read the denial audit entry has no client address, user agent or request ID, and a flood of refused reads writes one entry for each request. This change records the request fields and limits denial writes so the audit log stays useful.

The user path this change must make work:

1. **API.** Developer requests unit outputs through the tool-neutral API or the tool CLI.
2. **API.** Server refuses the read without a sharing rule or for client-encrypted state.
3. **Console.** Namespace administrator reads the audit log and sees the client address, user agent and request ID of each denial.
4. **Docs page.** Operator reads the output-sharing docs page for the denial fields and the limit.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| read without sharing rule gives 403 | 403 output read refused: the holder lacks a sharing rule for this consumer. Run dustpan output-sharing rules list to check the producer rules. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs` |
| read of client-encrypted state gives 409 | 409 outputs unavailable: the state is client-encrypted. Run tofu output against the workspace directly to read the values. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs` |

Evidence for the claims above:

- Denied output reads write an audit entry with subject, token ID, object, check name and reason and without SourceIP, UserAgent or RequestID. Proof: #1639: The audit entry that the output sharing checker writes for a denied read has the subject, the token ID, the object, the check name and the reason. It has no SourceIP, UserAgent or RequestID, and it has no rate limit.
- The issue states the entries of refusal.Recorder carry those fields and go through its limiter. Proof: #1639: The entries of refusal.Recorder carry those fields and go through its limiter.
- A holder of unit:read-outputs can write one denial entry for each refused request, for example the 409 of a client-encrypted state. Proof: #1639: A caller that holds unit:read-outputs can send requests that the checker refuses (for example the 409 of a client-encrypted state) and write one entry for each request.
- refusal.Recorder is in use by existing HTTP adapters. Proof: rg -n -F --glob '!*_test.go' 'refusal.Recorder' pkg internal cmd matches internal/api/runnerapi/runnerapi.go:132, internal/api/triggers/triggers.go:92, internal/api/objectcontext/objectcontext.go:84, internal/api/tofuhttp/tofuhttp.go:129

## What Changes

- Denied output-read audit entries carry SourceIP, UserAgent and RequestID.
- Denial audit writes for output reads go through refusal.Recorder with its limiter.

Out of scope:

- New audit kinds outside denied output reads
- Changes to sharing-rule evaluation or output values
- A new limiter implementation outside refusal.Recorder

Depends on:

- `output-sharing` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused output read still reports only the rule or state cause; the new fields stay in the audit entry and never leak into the read answer.
- A flood of refused reads keeps failing reads while audit writes stop, so the caller sees stable errors.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 21 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "The audit entries. Each entry has the sign-in ID and never a cookie value, a proof, a nonce, a token or a key."
- Effect: The change does not write any value that the quoted line forbids. It writes only SourceIP, UserAgent and RequestID beside the existing denial fields and writes no cookie value, proof, nonce, token or key.
- Conflict: none.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged (all decisions)).

- Decisions that apply: 23 (extended).
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decision 20; ADR 333 extends decision 23. Chain to the end: ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333.
- Quote: "Each step writes an audit entry, in the transaction of the change."
- Effect: The change touches denied output reads only. It does not remove or alter any entry that decision 23 requires for local-user steps.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The ADR check says no ADR is touched, but ADR 297 decision 21 and ADR 285 decision 23 set the audit entry rules for sign-in and the session, and decision 21 of ADR 297 says each entry never holds a cookie value, token or key. The change adds SourceIP and UserAgent to denial entries without any stated check against those rules; the ADR check must quote them or show they do not apply. Owner-tagged, ADR 297. Resolution: answered: adr_check now quotes ADR 297 decision 21 and ADR 285 decision 23 and states the effect and no conflict.
- R1.2 (wrong claim): The proof of the 'Why' cites the code by issue text only, and the claim that refusal.Recorder 'carries those fields' is not shown by the input; the rg output lists call sites, not the Recorder type or its fields. The claim that the Recorder applies a per-scope budget and that its entries hold SourceIP, UserAgent and RequestID is unchecked. Resolution: answered: Claims and evidence now repeat only what the issue states about the recorder; the per-scope budget claim is removed and the rg proof lists call sites only.
- R1.3 (open question): The spec says the flood limit stops denial writes 'until the limiter budget resets' but does not state the budget size, the scope key (holder, unit or client address), or what the caller sees when the limit is hit. The design's effect-limit test keys on 'one holder', while the proposal's issue asks for a limit on the flood; the key is not decided. Resolution: open question: Limiter key, budget and caller answer stay undecided in open questions; spec requires only that reads stay refused and writes stop, with no scope claim.
- R1.4 (failure mode): The fail-closed layer says a limiter or audit error never grants the read, but no scenario covers the case where the recorder drops the entry silently after its budget. The spec says 'further denial entries stop' while reads still fail, so a refused read may leave no record, which conflicts with the rule that nothing happens without a record. Resolution: open question: Dropped-entry accounting stays undecided in the owner open question; design fail-closed layer keeps the refusal while the record question is open.
- R1.5 (missing scenario): No scenario covers forged or empty SourceIP, UserAgent or RequestID headers, although the design's boundary layer says empty values are refused. The spec has no requirement for the refusal of an empty field, and the handler's behavior on an empty value is undefined. Resolution: answered: Added scenario Empty fields stay empty in requirement Denial entry carries request fields: when observed request fields are empty the entry holds empty values and the read stays refused.
- R1.6 (missing scenario): The spec has no scenario for the 409 client-encrypted case, which the proposal names as the flood source, or for a read that is refused and then allowed by a later sharing rule. Resolution: answered: Added scenario Denied read of client-encrypted state records request fields and scenario Read allowed after sharing rule in requirement Denial entry carries request fields: 409 records fields, allowed read returns outputs with no denial.
- R1.7 (open question): The change does not say whether the limiter's dropped denials are themselves audited, so a flood leaves an unaudited gap. The usability section says audit writes stop, which is a silent loss of records; no decision is recorded. Resolution: open question: Silent gap under flood stays in the dropped-denial open question for the owner.
- R1.8 (scope): Task 3.3 requires a docs page update naming the denial fields and the limit, but the proposal's Out of scope and the user path do not list the docs page or the console change for the audit view that the proposal's step 3 describes; the console part of the user path has no task. Resolution: answered: user_path now lists the docs page and console audit view; tasks now cover the docs update and the console check.
- R1.9 (wrong claim): The ADR check cites a rule that a decision is owner-tagged when the ADR records decisions of the project owner, but the check gives no owner tag for any ADR it lists; the ADR check section names no ADR and so cannot show the claim 'No ADR is touched' without a grep result. Resolution: answered: adr_check names ADRs 297 and 285 with quotes and owner-tagged decisions instead of an unproven no-ADR claim.
- R2.1 (open question): proposal.md and specs/output-sharing/spec.md: the limiter key, budget and caller answer when the limit is hit are not decided; the spec says only that writes stop until the budget resets, so the build cannot pick the limiter scope without the answer. Resolution: open question: Limiter key, budget and caller answer stay undecided in open questions; spec requires only that reads stay refused and writes stop, with no scope claim.
- R2.2 (open question): proposal.md: whether dropped denials are audited (summary entry or silent gap) is undecided; the provisional answer adds a summary entry, but the spec has no requirement or scenario for it, so the behavior under flood is unspecified. Resolution: open question: Dropped-denial accounting stays undecided in open questions; spec has no summary-entry requirement until the owner decides.
- R2.3 (wrong claim): proposal.md ADR check: ADR 297 decision 21 and ADR 285 decision 23 are owner-tagged; the claim that the new fields are 'request transport facts, not credentials' is an unproven judgment and the change does not quote the audit rule that each entry carries the sign-in ID; the conflict verdict rests on it. Resolution: answered: ADR check now states only what the quoted lines forbid and require, with exact quotes and owner-tagged decisions, and claims no new classification.
- R2.4 (missing scenario): specs/output-sharing/spec.md: the denial-flood requirement has no scenario for a holder who floods one unit versus many units, and no scenario for the caller response when the limit is hit, although the usability section claims stable errors. Resolution: open question: Per-unit versus per-holder flood scope stays in the limiter-key open question; caller response is now an explicit scenario that reads stay refused.
- R2.5 (failure mode): design.md layers: the fail-closed layer relies on the recorder dropping entries silently after budget; a refused read can leave no record, which the change does not reconcile with the rule that nothing happens without a record. Resolution: open question: Silent gap under flood stays in the dropped-denial open question; design fail-closed layer keeps the refusal while the record question is open.
- R2.6 (failure mode): design.md boundary layer: forged client address and user agent values are copied as observed, but the change does not say how a proxy-supplied address is trusted or how header length and content are bounded before the audit append. Resolution: open question: Address source and bounds stay in a new open question; design adds no new header parsing or proxy trust.
- R2.7 (scope): tasks.md: the failing-test mapping lists only two tests and does not map the 409 client-encrypted, the allowed-after-rule and the dropped-denial scenarios of the spec to named tests, so the spec-to-test mapping is incomplete. Resolution: answered: Tasks now map each spec scenario to a named test, including 409, allowed-after-rule, empty fields, flood, limiter-drop and append-failure cases.
- R2.8 (wrong claim): proposal.md evidence: the rg proof lists call sites of refusal.Recorder only; it does not show the recorder type, its fields or its limiter, so the claim that the recorder carries the request fields and a limiter is not supported by the input. Resolution: answered: Evidence now supports the recorder fields and limiter only from the issue text; the rg proof supports only existing use by adapters.
- R2.9 (missing scenario): specs/output-sharing/spec.md: no requirement covers a write failure of the audit append other than a limiter drop, such as a database outage, and what the caller sees then. Resolution: answered: Added requirement and scenario that an audit-append failure other than a limiter drop still refuses the read.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What limiter key and budget apply to denied output reads, and what does the caller see when the limit is hit?
   - Options: Limit by holder principal and unit with a fixed budget from the shared recorder defaults; Limit by client address with a fixed budget from the shared recorder defaults.
   - Provisional decision (owner review 14 Nov): Limit by holder principal and unit with a fixed budget from the shared recorder defaults
   - Reason: The issue asks for a rate limit but names no key and no budget. A per-holder key stops one caller from flooding one unit without muting other holders, which matches the flood in the issue. Benchmark: AWS CloudTrail stores the source IP, user agent and request ID on each denied call and throttles per principal, then records a summary of throttled calls.
   - Persona impact: A developer sees the same 403 or 409 for each refused read under either option; only the audit volume changes. Escape hatch: An operator that needs the full flood reads the request log, which keeps each request outside the audit log.
2. Does the server audit the denials that the limiter drops?
   - Options: Write one summary entry with the count of dropped denials when the budget resets; Write no entry for dropped denials.
   - Provisional decision (owner review 14 Nov): Write one summary entry with the count of dropped denials when the budget resets
   - Reason: Nothing happens without a record, so a silent gap needs an explicit owner decision; a summary count keeps the record without restoring the flood. Benchmark: Google Cloud Audit Logs records a summary count when it drops audit entries under load instead of leaving a silent gap.
   - Persona impact: A developer sees no change; a namespace administrator sees either a summary count or a gap in the audit log. Escape hatch: A namespace administrator that needs exact dropped rows reads the request log for the same time window.
   - Owner-tagged ADRs involved: ADR 297 (their words do not change).
3. Which observed address source and what length and content bounds apply to the recorded request fields?
   - Options: Pass the observed connection address and request user agent and request ID with no new proxy-header trust; Trust a proxy header for the client address.
   - Provisional decision (owner review 14 Nov): Pass the observed connection address and request user agent and request ID with no new proxy-header trust
   - Reason: The issue names the fields but no source and no bounds; forged values must not gain trust or overflow the audit store. Benchmark: AWS CloudTrail records the observed source address from the connection and bounds user-agent length before storage.
   - Persona impact: A developer sees no change in the read answer under either option; only the recorded address differs behind proxies. Escape hatch: A namespace administrator that doubts a recorded address checks the request log for the same request ID.

Questions considered and answered:

- Does any ADR decide the audit fields or the limiter for denied output reads? ADR 297 decision 21 forbids cookie values, proofs, nonces, tokens and keys in session entries and says each entry has the sign-in ID. ADR 285 decision 23 requires entries for local-user steps. Neither decides the fields or the limiter for denied output reads, so the limiter key, budget, caller answer, dropped-entry accounting, and proxy source stay open questions.
- Should the fix add a new limiter or reuse refusal.Recorder? The issue directs the fix: pass the request fields to the checker, or use the recorder for the append. The pack shows refusal.Recorder in use by existing adapters but does not show its fields or limiter, so the design claims the fields and the limiter only from the issue text and reuses the recorder with no new limiter and no new tool.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
