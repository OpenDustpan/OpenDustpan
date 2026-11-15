---
version: 3
---

## Why

**Persona: operator.** An Operator installs and configures the server and wants to manage groups, roles, grants and settings as code, the same way each change is scripted and reviewed, instead of by console acts alone.

The user path this change must make work:

1. **API.** Operator declares groups, roles, grants and settings in code files.
2. **CLI.** Operator previews the plan and applies it through the REST API.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply against an unknown API version fails. | The server answers the stable error for the route and changes nothing. | `GET /api/dustpan/v1/capabilities` |
| Apply of a weaker setting or claim rule without a fresh authentication fails. | The server answers 403 with error code `mfa.fresh-step-required` and changes nothing. Make a new sign-in and apply again. | `GET /api/dustpan/v1/capabilities` |
| Apply that removes the last enabled break-glass principal fails. | The server answers 409 and changes nothing. Keep one enabled break-glass principal and apply again. | `GET /api/dustpan/v1/capabilities` |
| Apply without the needed action on the object fails. | The server answers 403 and changes nothing. Ask for the missing action and plan again. | `GET /api/dustpan/v1/capabilities` |

Evidence for the claims above:

- Main has tests for group grants and built-in role families. Proof: TestGroupGrants in pkg/access/access_test.go:394 and TestBuiltInRoleFamilies in pkg/access/access_test.go:596
- Main tests that unit grants follow the stable ID. Proof: TestUnitGrantsFollowTheStableID in pkg/access/security_test.go:387

## What Changes

- Name the first objects under management as code after the groups and roles API is stable.
- Define the declarative file form, plan, preview, apply and audit record in a later refinement.
- No new routes, no unit moves and no tool behavior in this change.

Out of scope:

- Stable REST API for groups and roles itself (milestone 4)
- Moving units between namespaces and installations
- Secrets, Pulumi stacks and OpenTofu workspaces under code management
- Kubernetes operator overlap: the ADR 305 operator declares units, this change declares access and settings
- Terraform CLI support and other Later items

Depends on:

- 68-installation-settings for the shape of installation settings

Builds on (not a change):

- a stable REST API for groups and roles (milestone 4)

## Usability

Where the path still costs the person time or surprises them:

- The path still costs time: no plan, preview, or apply exists until the groups and roles API is stable and the RFC is accepted.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests group grants. (`pkg/access/access_test.go:394 (TestGroupGrants)`)
- Main tests built-in role families. (`pkg/access/access_test.go:596 (TestBuiltInRoleFamilies)`)
- Main tests that unit grants follow the stable ID. (`pkg/access/security_test.go:387 (TestUnitGrantsFollowTheStableID)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3, 7).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Effect: A later apply through the settings resource keeps the fresh step for a weaker setting. No new settings path is added.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3)."
- Effect: A later apply follows the one window and the refusal when no fresh step exists.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The rules are a sensitive setting (owner, 2026-11-07). A change to a rule or to an explicit mapping is a sensitive setting."
- Effect: A later apply treats each claim rule as a sensitive setting with audit entry and fresh step.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 14 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Routes. The paths of ADR 210 decision 3 do not change."
- Effect: A later apply uses only the current route paths and built-in roles.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A site admin may disable a break-glass principal (owner, 2026-10-20, accepted as written for now). This changes ADR 70 decision 24."
- Effect: A later apply must not remove the last enabled break-glass principal.
- Conflict: none.

**ADR 47: Soft delete, and a unit never moves between namespaces** (not owner-tagged).

- Decisions that apply: 5 (extended).
- Changed by: ADR 151 changes decision 3; ADR 153 extends decisions 1 and 3; ADR 346 extends decisions 5 and 7. Chain to the end: ADR 151, ADR 153, ADR 346, ADR 154, ADR 210, ADR 217, ADR 220, ADR 213, ADR 248, ADR 309, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 300, ADR 353, ADR 315, ADR 356, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 151, 153, 346.
- Quote: "A unit never moves between namespaces. The server has no action and no endpoint for it."
- Effect: No code file moves a unit between namespaces.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What objects does management as code cover first, and what file form declares them?
   - Options: Declarative files applied through the stable REST API; Direct SQL load of grants and settings; No management as code.
   - Provisional decision (owner review 14 Nov): Cover groups, roles, grants and settings first, in declarative files applied through the stable REST API.
   - Reason: RFC 48 waits on a stable REST API for groups and roles. Benchmark: AWS: CloudFormation applies declared resources through the service API.
   - Persona impact: Upsets no persona. It scripts what administrators do in the console. Escape hatch: Apply stays through the REST API, so each change keeps its audit entry.
   - Owner-tagged ADRs involved: ADR 216, ADR 212 (their words do not change).
2. How does an apply meet the fresh authentication rule for weaker settings and claim rules?
   - Options: Require a fresh authentication for each apply that weakens a setting or rule; Refuse agent apply of weaker settings.
   - Provisional decision (owner review 14 Nov): Require a fresh authentication for each apply that weakens a setting or claim rule.
   - Reason: A token never gives a fresh step. Benchmark: AWS: IAM requires MFA for sensitive actions.
   - Persona impact: Costs the Operator a sign-in but keeps the guard. Escape hatch: An apply with only a token cannot change a rule; it waits for a fresh sign-in.
   - Owner-tagged ADRs involved: ADR 212, ADR 227, ADR 332 (their words do not change).
3. Which actions must the applying principal hold, and how does apply stop removal of the last admin or break-glass principal?
   - Options: Least actions that cover the plan, with a last-admin guard; Operator-only apply.
   - Provisional decision (owner review 14 Nov): Least actions that cover the plan, with a last-admin guard.
   - Reason: A file can create grants and roles. Benchmark: ISO 27001: least privilege.
   - Persona impact: Upsets no persona. Escape hatch: Apply checks needed actions before any write.
   - Owner-tagged ADRs involved: ADR 216, ADR 306 (their words do not change).
4. How are partial apply, a crash mid-apply, concurrent applies and console edits during a plan handled?
   - Options: Per-object writes, each with a precondition on the planned revision, and an apply lock; re-plan resumes after a crash; One transaction through a new bulk route.
   - Provisional decision (owner review 14 Nov): Per-object writes with a planned-revision precondition and an apply lock; a re-plan resumes after a crash.
   - Reason: Many API writes cannot be one transaction, and a new bulk route is outside this change. Benchmark: AWS: CloudFormation locks a stack during update and rolls forward on retry.
   - Persona impact: Upsets no persona. A stale plan is refused and re-planned. Escape hatch: The plan lists done and pending objects, so a retry continues.
5. What happens to server-only objects, and how is a revoke expressed?
   - Options: Keep and report drift, with an explicit revoke block per removed grant; Prune everything missing from the file.
   - Provisional decision (owner review 14 Nov): Keep and report drift for groups and roles; a grant removed from the file needs an explicit revoke block.
   - Reason: Keep alone never revokes access. Benchmark: AWS: IAM policy detach is explicit.
   - Persona impact: Upsets no persona. Revoke is clear in review. Escape hatch: The plan reports each grant on the server that the file does not name.
6. How do names in a file map to stable IDs on rename or recreate?
   - Options: Resolve names to stable IDs at plan time and fail on duplicate names; Store stable IDs in the file.
   - Provisional decision (owner review 14 Nov): Resolve names to stable IDs at plan time and fail on duplicate names.
   - Reason: Stored references use the stable ID. Benchmark: AWS: Terraform-style state maps names to IDs.
   - Persona impact: Upsets no persona. Escape hatch: The plan shows the stable ID beside each name.

Questions considered and answered:

- Does managing OpenDustpan as code need new API routes No new route. A declarative flow calls the stable REST API. ADR 216 keeps the route paths and ADR 212 keeps the one settings resource.
- What does this change wait on and what does it specify now It waits on a stable REST API for groups and roles in milestone 4. It stays a coarse placeholder.
- How do fresh authentication, privilege, prune, names, concurrency, and audit work They stay open questions until the stable API and an accepted RFC exist.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
