Issue: #1160.

## Why

**Persona: site administrator.** Site administrator controls who does what. Today a caller with group management rights alone widens access by adding or removing a person in a group that an access limit names, because the guard counts grants alone.

The user path this change must make work:

1. **API.** Site administrator lists limits and groups that limits name through the API.
2. **CLI.** Caller with group rights tries a membership add or remove; a refused call shows 403 with the limit ID.
3. **Console.** Holder of the rights for a widening change makes the change; the audit log records who, which group, which principal, and which limit.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Membership change widens an access limit and the caller lacks the rights for the widening change. | The server refuses the membership change with 403 and names the limit ID. The caller needs the rights named in the docs page for a widening change. | `GET /api/dustpan/v1/access/reach?principal=<subject>` |

Evidence for the claims above:

- Issue 1160 reports the group guard counts only grants and misses groups named in forbid when or unless clauses. Proof: Issue #1160 body: The group guard (`access.CheckGroupGrants`) counts only the grants a group gives. Under ADR 223, an access limit can name a group in a `forbid ... unless principal in Group::X` clause.
- Issue 1160 reports that remove from a when-named group also widens access. Proof: Issue #1160 body: Removing a person from a group that a `forbid ... when principal in Group::X` clause names also widens access.

## What Changes

- Group membership guard checks groups named in access limit forbid when and unless clauses by stable group ID.
- Widening add or remove needs the rights for a widening change and writes a record with the limit ID.

Out of scope:

- New limit syntax or new Cedar effects
- Changes to grant expiry, SCIM sync rules, or protected group rules
- Changes to the limit save path, which keeps its own scope action and fresh authentication

Depends on:

- check-reach-when-grant-membership
- `check-reach-when-grant-membership` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A namespace administrator may not know why a routine add now needs more rights; the error names the limit ID and the docs page lists the rights path.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 223: Access limits: Cedar forbid policies for each scope** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 3 (current), 9 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "Forbid only (owner). An access limit is one Cedar `forbid` policy. A limit set holds `forbid` policies only. The server refuses at save a policy with the effect `permit`, with `gate.ErrInvalid`. A limit is a ceiling: it can only take away what a grant gives. It never gives an action."
- Quote: "After the grants. The `identity.Authorizer` checks the grants first. When the grants allow, it evaluates the limits of each scope that holds the object, in one Cedar request, with the fixed policy `permit (principal, action, resource);`. A `forbid` that matches refuses the call with `gate.ErrLimited`. The error names the limit ID."
- Quote: "Changes. A change of a limit needs the action of its scope (ADR 230 decision 2), a fresh authentication (ADR 227), and writes an audit entry. The console shows the limits of each scope next to the grants of the access review (ADR 52)."
- Effect: The change proposes a new membership guard and asks the owner to decide the rights it needs. It keeps forbid-only limits and the grant-then-limit check unchanged and changes no limit syntax or effect.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The change assumes a widening membership change needs the limited actions and the group write action, but the owner review of 14 Nov is pending and no ADR fixes which actions apply to a member add versus a remove, so the build cannot start without that answer (openspec/changes/group-guard-count-forbid-exemptions/proposal.md, Open questions). Resolution: open question: Moved to open questions as owner-decided: which actions an add versus a remove needs; build waits for the owner answer.
- R1.2 (missing scenario): No scenario covers a limit that names a group in an unless clause and a second limit in a when clause on the same group, or a membership change that widens one limit while narrowing another (specs/access/spec.md). Resolution: answered: Covered by specs/access requirement conflicting limits: change that widens any limit is refused even when it narrows another.
- R1.3 (missing scenario): No scenario covers a membership change by a site administrator or a break-glass session, which ADR 223 decision 7 places outside the limits; the spec does not say whether the widening guard applies to them (specs/access/spec.md). Resolution: answered: Covered by specs/access requirement admin and break-glass path: site admin path is defined and break-glass outside limits is stated.
- R1.4 (failure mode): The guard reads limits and membership in separate reads unless the fenced transaction is specified; the spec does not say what happens when the limit set cannot be parsed at membership time, beyond the HTTP layer's fail-closed note. The spec has no requirement for fail-closed on a parse error in the service path (design.md, Layers, port adapter). Resolution: answered: Covered by specs/access requirement parse failure fails closed and by design decision stable ID matching with fail closed parse.
- R1.5 (failure mode): The parser extracts Group stable IDs only from saved policy text; a group referenced through a Cedar entity hierarchy or a nested group in the principal parents is not covered by the matching rule, so the risk 'parser misses a group reference form' is not closed by a test (design.md, Risks and Decisions 2). Resolution: answered: Covered by design decision stable ID matching and specs/access requirement stable ID match: parser covers direct Group stable ID tests and nested parents resolve through the same IDs; unknown forms fail closed with a test.
- R1.6 (missing scenario): No scenario covers a group rename or a remove and make again of a named group between the limit save and the membership write, although ADR 362 requires stable ID matching for that case (specs/access/spec.md, Requirement Unguarded group stays as today). Resolution: answered: Covered by specs/access requirement stable ID match scenario rename and remove and make again.
- R1.7 (wrong claim): The ADR check says ADR 223 decision 1 is owner-tagged, but the quoted decision is the only owner-tagged line; the claim that membership changes need the same rights as the limited actions is not a quote of any ADR decision, and the proposal cites ADR 223 as unchanged (proposal.md, ADR check). Resolution: answered: Fixed adr_check and considered: owner tag stays on ADR 223 decision 1 only; the guard rule is stated as the issue proposal with an open question, not as an ADR quote.
- R1.8 (scope): Task 2.2 requires the reach endpoint to show the refusal reason, but the proposal and spec do not define a change to the reach query; the item may be building a new read surface outside its own scope (tasks.md, 2.2). Resolution: answered: Fixed tasks: the reach query is an existing read used for diagnosis, with no change to its shape.
- R1.9 (open question): The spec does not say whether a refused membership change writes a decision record under ADR 223 decision 8 or only an audit entry, and the two records have different retention rules. Resolution: open question: Moved to open questions: audit entry only versus decision record, decided by PM.
- R1.10 (missing scenario): No scenario covers a bulk membership change or a group membership change made through SCIM sync, which the proposal lists as out of scope but does not exclude from the guard path. Resolution: answered: Covered by specs/access requirement bulk and sync path: bulk and SCIM writes pass through the same guard.
- R2.1 (open question): openspec/changes/group-guard-count-forbid-exemptions/proposal.md: The required rights for a widening add or remove are not decided by any ADR or owner decision; every widening requirement in specs/access/spec.md depends on this open question, so the build cannot start. Resolution: open question: Still undecided; kept as owner open question on the rights for a widening change, build waits for the answer.
- R2.2 (open question): openspec/changes/group-guard-count-forbid-exemptions/proposal.md: The record for a refused membership change (audit entry only or decision record) is undecided, yet the spec scenarios name it as the record to write; the spec cannot be built as written. Resolution: open question: Still undecided; kept as PM open question on audit entry versus decision record.
- R2.3 (adr conflict): The provisional decision gives a site administrator as escape hatch while ADR 223 decision 7 places break-glass sessions outside limits; the spec's admin path does not state whether a site administrator without the widening rights is refused, so the rule for admins is contradictory. Resolution: answered: Clarified in the open question escape hatch and the admin requirement scenarios: a site administrator without the widening rights is refused and break-glass sessions stay outside limits.
- R2.4 (wrong claim): The ADR check says ADR 223 decision 1 is owner-tagged and that the Effect line keeps the grant-then-limit check, but the proposal's rule that a group in a forbid clause needs the limited actions is not a decision of ADR 223; the Effect line presents an unratified guard as settled. Resolution: answered: Fixed the ADR check effect to state the guard as a proposal with undecided rights, not as a settled ADR decision.
- R2.5 (failure mode): Design decision 1 lists limits and membership in separate reads, and the spec does not require the limit read and the membership write to share one fenced transaction; a limit saved between the read and the write can make a widening change pass unchecked. Resolution: answered: Added design decision for a single fenced transaction and a spec requirement for atomic limit read and membership write.
- R2.6 (missing scenario): The spec has no scenario for a membership change that widens a limit by removing a group from an unless clause or adding a group to a when clause (the limit save path), so limit edits that change the guard outcome are not covered. Resolution: answered: Stated in out of scope that the limit save path keeps its own scope action and fresh authentication.
- R2.7 (failure mode): Design risk: a parser that misses a group reference form (for example a Group in a nested Cedar expression) passes a widening change; the design has no mutation test or hostile-input test listing each reference form, which the project rules require for security code. Resolution: answered: Added a hostile parser task that lists each reference form and a mutation test that removes the guard.
- R2.8 (scope): Task 2.2 adds audit entries and a refusal path to the membership routes, but the proposal's 'Out of scope' does not exclude the SCIM sync write path, and the spec's bulk and sync requirement makes it in scope without a task that tests it. Resolution: answered: Added a bulk and sync task with a conformance test for the SCIM write path.
- R2.9 (missing scenario): No scenario covers a caller who holds the widening rights but whose own session is not fresh (ADR 223 decision 9 requires a fresh authentication for a change of a limit); the membership guard does not state whether it requires fresh authentication. Resolution: answered: Answered in considered and design: the guard adds no fresh authentication beyond the membership routes.
- R3.1 (open question): The rights a caller needs for a widening membership change (limited actions plus group write, or a site admin action) are not fixed by any ADR or owner decision, so no requirement in specs/access/spec.md can be built; the provisional answer rests on a benchmark, not on an accepted decision (openspec/changes/group-guard-count-forbid-exemptions/proposal.md, Open questions 1). Resolution: open question: Kept as owner open question on the rights for a widening change; requirements use the placeholder rights for a widening change and build waits for the owner answer.
- R3.2 (open question): The record written for a refused widening membership change (audit entry only or a decision record) is undecided, yet every refusal scenario names it as the record to write; the spec cannot be implemented as written (specs/access/spec.md, Requirements Guard widening group adds and removes, Parse failure fails closed). Resolution: open question: Scenarios now name only the audit entry; whether a decision record is also written stays a PM open question.
- R3.3 (adr conflict): ADR 223 decision 7 places break-glass sessions outside the limits ("A session of a break-glass account (ADR 70) is not subject to an access limit"), but the spec applies the widening guard to the admin and break-glass path and the open question makes a site administrator without the rights refused; the scope of the guard over break-glass sessions contradicts the decision. Resolution: answered: Added break-glass exemption requirement and design decision: break-glass sessions stay outside the guard per ADR 223 decision 7; site administrator without the rights is refused.
- R3.4 (wrong claim): The ADR check cites ADR 223 decision 1 as owner-tagged and says a membership guard is consistent with ADR 223, but no quoted decision of ADR 223 states a guard on membership writes; the proposal's guard rule is a new requirement presented as a consequence of the ADR (proposal.md, ADR check, Effect line). Resolution: answered: Reworded adr_check effect to state the guard as a proposal with undecided rights, not as a consequence of ADR 223.
- R3.5 (failure mode): Decision 1 of design.md says a change that widens any limit needs the guard of the open question, but no scenario covers a membership change that is made while a limit's Cedar text changes between parse and commit beyond the fenced transaction note; the fencing claim is not tested by a named concurrency test (tasks.md, 1.3 and 2.1). Resolution: answered: Added named concurrency test task for limit saved during membership write and kept the atomic check requirement.
- R3.6 (missing scenario): No scenario covers a group nested as a parent of a principal (ADR 223 decision 4 places groups as parents of the principal), so a widening through a nested group membership is not specified; the parser rule covers only direct Group stable ID tests. Resolution: answered: Added nested parent scenario in stable ID requirement and parent chain resolution in design.
- R3.7 (scope): Task 2.3 requires the reach endpoint to explain a refused membership change, but the reach query is not shown to report membership refusals; the claim that it explains the refusal has no requirement or check behind it. Resolution: answered: Removed the claim that the reach query explains the refusal; the error names the limit ID and the task checks only that the reach query is unchanged.
- R3.8 (open question): The spec does not state whether a widening change by a caller holding the rights must also pass the fresh authentication of ADR 223 decision 9 when it is a change of a limit set; the answer is given only as 'no extra step' in the considered list, with no requirement. Resolution: answered: Added explicit no extra fresh authentication requirement and considered answer.
- M.24 (dependency): This change edits the same member-add transaction as the cover check of check-reach-when-grant-membership. Resolution: answered: Added check-reach-when-grant-membership to depends_on; design decision Single fenced transaction now runs the widening check and the reach check in the same member-add transaction.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which actions must the caller hold for a widening membership change, for an add to an unless-named group and for a remove from a when-named group?
   - Options: Caller holds each limited action at the scope of the limit plus the group write action; Caller holds a site admin action plus the group write action.
   - Provisional decision (owner review 14 Nov): Caller holds each limited action at the scope of the limit plus the group write action.
   - Reason: The caller who unlocks an action through membership already holds that action, which matches the AWS practice of checking effective reach. Benchmark: AWS IAM Access Analyzer treats a policy exception that widens reach as needing the same permission as the allowed action.
   - Persona impact: Namespace administrators keep routine work for groups named by no limit; a widening change waits for the owner answer. Escape hatch: A holder of the limited actions at each scope makes the change; a site administrator without those actions is refused; break-glass sessions stay outside limits and the audit entry names the limit, the group, the principal, and the caller.
   - Owner-tagged ADRs involved: ADR 223 (their words do not change).
2. Does a refused widening membership change write an audit entry only, or also a decision record?
   - Options: Audit entry only; Audit entry plus a decision record under the refusal rule; Decision record only.
   - Provisional decision (owner review 14 Nov): Audit entry only.
   - Reason: A membership refusal is not a limit refusal of a call, so an audit entry fits the change rule while the decision record rule stays for call refusals. Benchmark: SOC 2 requires a complete audit trail with defined retention; a refused security change keeps one record with a defined retention.
   - Persona impact: No persona loses access; the choice changes which retention rule holds the refusal record. Escape hatch: Both records name the limit ID and the caller, so either choice stays auditable until the retention rule is set.
   - Owner-tagged ADRs involved: ADR 223 (their words do not change).

Questions considered and answered:

- Does a group named in a forbid clause grant access in the sense of the group guard? No. ADR 223 decision 1 says a limit is a ceiling that only takes away what a grant gives, and decision 3 says the authorizer checks grants first then limits. A group named in a forbid clause controls the ceiling but grants nothing. The issue proposes a new guard for that control; no ADR decides the guard rule, so the exact rights are an open question.
- Does the guard match groups by name or by stable ID? By stable group ID, including transitive parents. ADR 223 decision 4 carries the principal with its groups as parents, and the ADR 362 chain says a Cedar entity for a group uses the stable ID of the group. The guard compares stable group IDs from the saved forbid policy with the membership group ID and its parent chain, never the display name.
- Does a refused membership change write a decision record, an audit entry, or both? At least an audit entry. Every membership write already writes an audit entry under the access rules. Whether a refused widening change also writes a decision record under the refusal rule is undecided and kept as a PM open question. Scenarios name only the audit entry until the PM answers.
- Does the membership guard need a fresh authentication step? No extra step. ADR 223 decision 9 requires a fresh authentication for a change of a limit, not for a membership write. The guard adds no fresh authentication beyond what the membership routes already need, stated as a requirement.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
