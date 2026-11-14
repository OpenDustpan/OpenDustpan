Roadmap: `docs/roadmap.md:769` (A person delegates to Dusty).

## Why

**Persona: developer.** A developer wants to let Dusty work for them without handing over a personal token. The developer creates one delegation with a narrow scope and a short expiry, checks it in a list, and revokes it when the work ends.

The user path this change must make work:

1. **Docs page.** Read the delegation docs page and copy the create command.
2. **API.** Create one delegation to dusty for one namespace with level suggest and expiry 7 days.
3. **Console.** List own delegations and open the new row in the console.
4. **E2e.** Revoke the delegation and confirm the list shows it revoked.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Create with expiry in the past or more than 30 days after creation | 422 with the NEW error code delegation.invalid-expiry and no new row. | `POST /api/dustpan/v1/delegations` |
| Read of an unknown delegation id | 404 with the NEW error code delegation.not-found. | `GET /api/dustpan/v1/delegations/{id}` |
| List when the caller holds no delegation | 200 with an empty list. | `GET /api/dustpan/v1/delegations` |

Evidence for the claims above:

- No delegation record exists on main; keyword Dusty has no e2e, no guide and no code in the pack. Proof: keywords Dusty e2e_count 0 with e2e [], guides [], code []
- Main already pins delegation-scoped grant checks and revoke checks on objects. Proof: pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly) and pkg/access/access_test.go:1089 (TestRevokeChecksTheObject)
- Milestone exit test needs one stored delegation with holder, scope, creator and time, listable and revocable. Proof: roadmap step: Pass: 1 delegation is a stored record with a holder, a scope, a creator and a time.

## What Changes

- Make the NEW agent principal dusty of the NEW kind agent on model connect with stable ID dusty.
- Store one delegation record per delegation with holder dusty, scope namespace id, level, creator, created time, expiry and revoked time.
- NEW route POST /api/dustpan/v1/delegations to create one delegation with holder dusty and creator equal to caller and NEW action delegation:create on the delegation object.
- NEW route GET /api/dustpan/v1/delegations to list own delegations with NEW action delegation:read.
- NEW route GET /api/dustpan/v1/delegations/{id} to read one delegation with NEW action delegation:read.
- NEW route GET /api/dustpan/v1/namespaces/{namespace}/delegations to list delegations in one namespace with NEW action delegation:read.
- NEW route POST /api/dustpan/v1/delegations/{id}/revoke to revoke one delegation with idempotent repeat and NEW action delegation:revoke.
- Console delegation views call the same list and read routes with a session credential.
- Audit entries for delegation create and revoke that name holder dusty, creator and revoker.

Out of scope:

- Dusty run loop, model connection and tool catalog
- Enforcement of delegation scope, level and agent ceiling at call time
- Change orders, confirmations and approvals
- Audit entry shape for agent steps
- Outside-vendor agents and MCP endpoint

## Usability

Where the path still costs the person time or surprises them:

- The person copies the delegation id from the create answer into the read and revoke commands on the same page.
- Level words look, suggest and operate show with the expiry in each read answer.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Grants on a principal take delegation actions only in a pin test. (`pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly)`)
- Revoke checks the object before it revokes. (`pkg/access/access_test.go:1089 (TestRevokeChecksTheObject)`)
- Token scopes fit the principal kind. (`pkg/accesstoken/accesstoken_test.go:904 (TestScopesFitTheKind)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The person who delegated (Alice) may confirm a change that her own agent proposed, by default."
- Effect: The change stores the delegation that ADR 354 decision 1 assumes: a person delegates to Dusty and may later confirm what the agent proposed.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this step enforce delegated calls? No. This step stores, lists and revokes the record only. Enforcement ships with the later steps that start runs and tools.
- May a person delegate for another person? No. Creator is the person, holder is the dusty agent principal. A person delegates only for self.
- May the person who delegated later confirm? Yes, by default per ADR 354 decision 1. A two-person rule needs a later approval change, out of scope here.
- Is the 30-day expiry a setting? Fixed 30-day maximum per row in this step; a setting comes later.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
