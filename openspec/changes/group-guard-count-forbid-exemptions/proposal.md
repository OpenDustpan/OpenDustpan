---
version: 3
---

Issue: #1160.

## Why

**Persona: site administrator.** Site administrator controls who does what. Today a caller with group management rights alone widens access by adding a person to, or removing a person from, a group that an access limit names, because the guard counts grants alone. This change defines the rights that such a change needs: the caller holds each limited action at the scope of the limit. The owner may still choose another rule (open question 1).

The user path this change must make work:

1. **API.** Site administrator lists the access limits of a scope and sees which groups they name.
2. **CLI.** A caller with group rights alone adds a person to a group that a limit names. The server answers 403.
3. **Console.** A caller who holds the limited actions makes the change. The audit log shows who, which group, which principal, and which limit when the caller can read it.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A membership change widens an access limit and the caller does not hold the limited actions. | The server refuses the change with 403 and writes an audit entry. The error names the limit ID only when the caller can read that limit. | `GET /api/server/caller/access` |

Evidence for the claims above:

- The issue reports that the group guard counts only grants and misses groups named in forbid clauses. Proof: Issue #1160 body: The group guard (`access.CheckGroupGrants`) counts only the grants a group gives. Under ADR 223, an access limit can name a group in a `forbid ... unless principal in Group::X` clause.
- The issue reports that a remove from a when-named group also widens access. Proof: Issue #1160 body: Removing a person from a group that a `forbid ... when principal in Group::X` clause names also widens access.
- The group guard exists on main as a service method and a store interface method. Proof: Pack main list: CheckGroupGrants at pkg/access/service.go:656 and pkg/principal/store.go:228.
- Tests for the group guard exist on main. Proof: Pack main list: TestCheckGroupGrantsReadsTheAccessLimits at pkg/access/group_limits_test.go:91 and TestGroupGrants at pkg/access/access_test.go:385.

## What Changes

- The group membership guard checks groups named in forbid when and unless clauses by stable group ID.
- A widening add or remove needs the caller to hold each limited action at the scope of the limit, and writes an audit entry.

Out of scope:

- New limit syntax or new Cedar effects
- Changes to grant expiry or protected group rules
- Changes to the limit save path, which keeps its own scope action and fresh authentication
- SCIM sync actor rights path and sync retry rules

Depends on:

- check-reach-when-grant-membership
- `check-reach-when-grant-membership` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A namespace administrator may not know why a routine add now needs more rights. The error names the limit ID when the caller can read it, and the docs page lists the rule.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Service method CheckGroupGrants holds the current group guard. (`pkg/access/service.go:656`)
- The principal store declares CheckGroupGrants. (`pkg/principal/store.go:228`)
- A test covers that CheckGroupGrants reads the access limits. (`pkg/access/group_limits_test.go:91`)
- A test covers that a refusal hides a limit the caller cannot read. (`pkg/access/group_limits_test.go:578`)
- A test covers group grants. (`pkg/access/access_test.go:385`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 223: Access limits: Cedar forbid policies for each scope** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 3 (current), 9 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "Forbid only (owner). An access limit is one Cedar `forbid` policy. A limit set holds `forbid` policies only. The server refuses at save a policy with the effect `permit`, with `gate.ErrInvalid`. A limit is a ceiling: it can only take away what a grant gives. It never gives an action."
- Quote: "After the grants. The `identity.Authorizer` checks the grants first. When the grants allow, it evaluates the limits of each scope that holds the object, in one Cedar request, with the fixed policy `permit (principal, action, resource);`. A `forbid` that matches refuses the call with `gate.ErrLimited`. The error names the limit ID."
- Effect: The change keeps forbid-only limits and the grant-then-limit check as they are. It adds a membership write guard for groups that saved forbid policies name. The guard rule is new and is not an ADR decision.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Is the rule 'the caller holds each limited action at the scope of the limit' the right rule for a widening membership change, and how do site administrators and break-glass sessions meet it?
   - Options: Caller holds each limited action at the scope of the limit, plus the group write action; A site administrator passes with the group write action alone; Break-glass sessions stay outside the guard, as they stay outside limits.
   - Provisional decision (owner review 14 Nov): Caller holds each limited action at the scope of the limit, plus the group write action.
   - Reason: The caller who unlocks an action through membership could already do that action. This matches the issue scope. The specs build this rule. Benchmark: AWS IAM: a change that grants an effective permission needs that permission in the caller (privilege escalation check).
   - Persona impact: Routine group work stays as today. A site administrator who lacks a limited action is refused and must hold it. Escape hatch: A holder of the limited actions makes the change, and the audit entry names the limit, the group, the principal and the caller.
   - Owner-tagged ADRs involved: ADR 223 (their words do not change).
2. Does a refused widening membership change write an audit entry only, or also a decision record?
   - Options: Audit entry only; Audit entry plus a decision record.
   - Provisional decision (owner review 14 Nov): Audit entry only.
   - Reason: The decision record rule covers refusals of calls, not membership writes. Benchmark: SOC 2: each security change and refusal keeps one audit record.
   - Persona impact: No persona loses access. Escape hatch: The audit entry names the caller in all cases.
   - Owner-tagged ADRs involved: ADR 223 (their words do not change).
3. Does a bulk membership batch with one widening entry fail as a whole batch or entry by entry?
   - Options: Atomic batch refusal with per-entry errors; Per-entry apply with per-entry refusal.
   - Provisional decision (owner review 14 Nov): Atomic batch refusal with per-entry errors.
   - Reason: Atomic refusal avoids a partial widening state. Benchmark: AWS IAM: a failed batch write applies no part and lists per-entry errors.
   - Persona impact: SCIM sync rules stay unchanged. Escape hatch: The caller resubmits the batch after the rights change.
   - Owner-tagged ADRs involved: ADR 223 (their words do not change).

Questions considered and answered:

- Does a group named in a forbid clause grant access in the sense of the group guard? No. ADR 223 decision 1 says a limit is a ceiling. Membership of a named group changes the ceiling, so the guard must check it.
- Does the guard match groups by name or by stable ID? By stable ID, with parent groups. ADR 362 says a Cedar entity for a group uses the stable ID of the group.
- Does the membership guard need a fresh authentication step? No. ADR 223 decision 9 requires it for a change of a limit, not for a membership write.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
