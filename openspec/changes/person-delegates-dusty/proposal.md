Roadmap: `docs/roadmap.md:769` (A person delegates to Dusty).

## Why

**Persona: developer.** A developer wants to let Dusty work for them without handing over a personal token. The developer creates one delegation with a mode, a scope and an expiry, checks it in a list, renews it before it ends, and revokes it when the work ends.

The user path this change must make work:

1. **Docs page.** Read the delegation guide and copy the create command.
2. **API.** In a console session with a fresh authentication, create one delegation to Dusty in the mode `propose` for one namespace, with the default lifetime. The create answer shows the person, the agent, the mode, the expanded scope, the creator, the time, the expiry and the client authentication `server`.
3. **API and console.** List the own delegations and read one. The console pages are in the console repository and call the same routes (RFC 121 slice 9).
4. **API.** Renew the delegation. The same ID gets a later expiry.
5. **API.** Revoke the delegation and read the list again: the row shows its end and the cause.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Create, renew or widen with an access token, or in a break-glass session | 403 with the NEW error code `delegation.session-required`. The text says that a delegation needs a console session. No row is written or changed. | Sign in to the console, then repeat the call. |
| Create or renew with an expiry in the past or after the effective maximum | 422 with the NEW error code `delegation.invalid-expiry`. The text names the maximum in days. No row is written. | `GET /api/dustpan/v1/delegations/settings` (NEW, proposed path, see open question 3) |
| Create with a mode other than `read`, `propose` or `operate` | 422 with the NEW error code `delegation.invalid-mode`. No row is written. | `GET /api/dustpan/v1/delegations/settings` (NEW) |
| Create for an agent that does not exist or is disabled | 422 with the NEW error code `delegation.unknown-agent`. No row is written. | `GET /api/dustpan/v1/principals` |
| Read, renew or revoke of an unknown ID, or of the row of another person without `server:manage-access` | 404 with the NEW error code `delegation.not-found`. | `GET /api/dustpan/v1/delegations` (NEW) |
| Create or renew when the fresh step applies and the person has none | 403 with the error code `mfa.fresh-step-required`. | The sign-in path that the error names (see open question 7). |

Evidence for the claims above:

- No delegation record, route or type exists on main. Proof: `rg -n '/api/dustpan/v1/delegations|/api/server/delegations' -g '*.go' -g '*.json' -g '*.md' .` outside `openspec/changes/` prints nothing, and `rg -n 'type Delegation(Record)? ' -g '*.go' .` prints nothing.
- The kind `agent` is on main, and `CreatePrincipal` refuses it. Proof: `pkg/principal/principal.go:114` (`KindAgent`), `pkg/identity/identity.go:90` (`KindAgent`) and `pkg/principal/service.go:150` (`if in.Kind == KindAgent`, the kind comes from the model connect request).
- No model connect route and no store behind the agent settings port exist on main. Proof: `rg -n 'SettingsStore' -g '*.go' .` outside `internal/agent/` prints only the unrelated names `openSettingsStore` and `TestSuiteOverAMemorySettingsStore`. The port is at `internal/agent/settings.go:104`.
- No change in `openspec/changes/` makes Dusty on model connect. Proof: `ls openspec/changes | rg -i 'model|connect'` prints only `research-default-model-dusty-on`, which is a research change for the default model.
- No agent class exists on main. Proof: `rg -n 'AgentClass' -g '*.go' pkg internal` prints nothing.
- The fresh step exists as a session-only gate. Proof: `internal/api/server/freshness.go:221` (`func (h *Handler) fresh`), used at `internal/api/server/githostlinks.go:318`. The setting `freshAuthEnabled` of ADR 385 is not in production code on main. Proof: `rg -n 'freshAuthEnabled|FreshAuthEnabled' -g '*.go' .` prints only the test `internal/api/server/firstadmin_test.go:1014`.
- Main has no ID for a delegation, except an example in a test. Proof: `pkg/audit/agent_block_test.go:21` uses `DelegationID: "dlg-1"`.
- Milestone exit test step 1 needs one stored delegation with holder, scope, creator and time, listable and revocable. Proof: `docs/roadmap.md` step 1: "Pass: 1 delegation is a stored record with a holder, a scope, a creator and a time. A person can list it and revoke it."

## What Changes

This change is slice 2 of RFC 121, limited to the delegation to Dusty. The other slices are in the "Out of scope" list.

- Add the domain package `pkg/delegation` with the stored record, its port and its conformance suite, the service `internal/delegationsvc`, and one SQL adapter for each dialect. The record holds (RFC 121 decisions 11 and 13):
  - the stable ID of the delegation,
  - the person: the stable ID of the principal of the person,
  - the agent: the stable ID of the agent principal (never the name `dusty`),
  - the mode: `read`, `propose` or `operate` (RFC 121 decision 12),
  - the scope, stored expanded: the objects (a list of namespace IDs, or all namespaces, and the separate flag `installation`, both `false` by default), the list of actions that the mode expanded to when the person consented, and the audiences,
  - the creator, always the person,
  - the created time, the expiry, the last renewal time, and the end time and the cause of the end (`revoked`, `expired`, `agent-disabled`, `agent-deleted`, `person-disabled`, `person-deleted`, `person-removed` or `person-terminated`),
  - the client authentication: `server` for Dusty.
- The audiences of a Dusty delegation are an empty list: Dusty has no delegated token (RFC 121 decision 10).
- Create, renew or widen a delegation only in a console session with a fresh authentication. A request with an access token, and a request in a break-glass session, is refused (RFC 121 decision 14). The break-glass check reads `Owner.BreakGlass` of the session (`pkg/session/session.go:192`).
- The lifetime of a Dusty delegation is 30 days by default and 90 days at most (ADR 354 decision 3). The installation setting that holds the lifetimes shows its effective values in the API, and a change of it writes an audit entry.
- Renewal extends the same delegation ID, with the same or a narrower scope, and writes an audit entry (RFC 121 decision 15). A delegation never renews by itself.
- A delegation ends at the next resolve when the person revokes it, it expires, the agent is disabled or deleted, or the person is disabled, deleted, removed by SCIM or terminated. Each end writes an audit entry with the cause (RFC 121 decision 16). This change writes the end and the entry. The stop of work after the end is the change `revoke-stops-work`.
- Add the routes, all declared in the route table of the installation API (`internal/api/server/routes.go`, like the routes of `internal/api/server/githostlinks.go`). The names are proposals. Open question 1 asks for the authorization action of the own-row routes.
  - `POST /api/dustpan/v1/delegations` creates a delegation with the person equal to the caller.
  - `GET /api/dustpan/v1/delegations` lists the delegations of the caller.
  - `GET /api/dustpan/v1/delegations/{id}` reads one.
  - `POST /api/dustpan/v1/delegations/{id}/renew` renews one.
  - `POST /api/dustpan/v1/delegations/{id}/revoke` revokes one, idempotent on repeat.
  - `GET /api/server/delegations` lists every delegation, and `POST /api/server/delegations/{id}/revoke` revokes any one, both with `server:manage-access` on the installation (RFC 121 decision 17).
  - `GET /api/dustpan/v1/delegations/settings` shows the effective lifetimes (ADR 354 decision 3). The path is a proposal (open question 3).
- Each create, renew and end writes an audit entry (RFC 121 decision 51). Open question 6 asks whether these entries carry the agent block of ADR 380.
- Add the GLOSSARY entries Delegation, Delegation mode and Agent principal, fix the entry "Principal kind" (ADR 354 decision 7), and write "act-as" for the right of ADR 70 (ADR 354 changes ADR 70 decision 13). These edits are in this pull request.

Out of scope:

- Making Dusty as an agent principal on model connect. The model connect request creates Dusty with the administrator as creator and first sponsor (RFC 121 decision 7). This change needs that step first. See "Depends on".
- The consent page and the other console pages. They are in the console repository (RFC 121 slice 9). A screen starts as a design (console `CLAUDE.md`).
- The notices 7 days and 1 day before the expiry (RFC 121 decision 15). They need the notify model, and no ADR or RFC line gives their channel.
- The export of delegations in the access review (RFC 121 decision 17), and the other items of the installation agent policy (RFC 121 decision 19): the modes a person can pick, the ceiling of a public client, the namespaces that no delegation can cover, the REST audience, DPoP and the burst suspension.
- Enforcement of the scope, the mode and the ceiling at call time (RFC 121 slice 3), and the `never` class (RFC 121 decision 20).
- Sponsors, ceiling grants and the agent record (RFC 121 slice 1).
- Delegated tokens, client registrations and the MCP endpoint (RFC 121 slice 7). Dusty has none (RFC 121 decision 10).
- Dusty runs, tools, change orders, confirmations and approvals.
- Renaming the code identifier `access.FamilyDelegation` (open question 9).

Depends on:

- A model connect change that creates Dusty as an agent principal (RFC 121 decision 7, slice 6). No such change is in `openspec/changes/` on 20 November 2026. It is a dependency to be written. This proposal does not name it and does not specify it.
- A change for the agent class of each catalog action (RFC 121 decision 20, slice 1). It is needed for the table of open question 8. No such change is in `openspec/changes/` on 20 November 2026. It is a dependency to be written.
- `audit-agent-block` and `agent-shapes-before-first-tag` landed and are archived. They are not dependencies any more. The kind `agent` and the field `delegationID` of the block are on main.

## Usability

Where the path still costs the person time or surprises them:

- The person copies the delegation ID from the create answer into the read, renew and revoke commands on the same page.
- Each read answer shows the mode word, the expanded actions, the expiry, the effective maximum and the end cause. The consent page and the docs say that a temporary administrator grant widens the subject part of a live delegation for its lifetime, by design of the intersection (RFC 121 decision 14).
- A create from a script with an access token fails by design. The error names the console session, not a flag.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Grants on a principal take act-as actions only, in a pin test. (`pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly)`)
- Revoke checks the object before it revokes. (`pkg/access/access_test.go:1163 (TestRevokeChecksTheObject)`)
- Token scopes fit the principal kind. (`pkg/accesstoken/accesstoken_test.go:934 (TestScopesFitTheKind)`)
- The own-token action `token:manage-own` is on `CallerObject` in the family `FamilySelf`. (`pkg/identity/identity.go:385 (ActionTokenManageOwn)`, `pkg/access/access.go:242`, `pkg/access/access.go:113 (FamilySelf)`)
- Every signed-in person holds the self actions through `identity.SelfAllowed`. (`pkg/dustpan/dustpan.go:965`)
- A fresh step is a session-only gate that wraps a route. (`internal/api/server/freshness.go:221 (fresh)`, `internal/api/server/signin.go:672 (sessionFromRequest)`)
- A console session knows whether it is a break-glass session. (`pkg/session/session.go:192 (BreakGlass)`)
- The agent block of an audit entry holds the field `delegationID`. (`pkg/audit/audit.go:223 (Origin)`, `pkg/audit/audit.go:236 (OriginArgs)`, `pkg/audit/audit.go:256 (NewOrigin)`)
- The kinds of a principal include `agent`. (`pkg/principal/principal.go:114 (KindAgent)`, `pkg/identity/identity.go:90 (KindAgent)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged: "Decided by the project owner on 7 November 2026").

- Decisions that apply: 1, 2, 3, 6 and 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote, decision 3: "The default lifetime is 7 days, and 30 days at most, for a public client. It is 30 days and 90 days for Dusty and for confidential clients. This is configurable: an installation setting holds the lifetimes, with these values as the default. The API shows the effective values."
- Quote, decision 6: "Decisions 1 to 52 of the \"Proposal\" of the RFC apply."
- Quote, decision 7: "The first slice fixes the GLOSSARY entry \"Principal kind\". A token from a trust rule gives `workload`, as ADR 240 decision 3 says."
- Quote, "Changes": "Changes ADR 70 decision 13: a delegation is a new edge kind with intersection semantics, and the word \"delegation\" there becomes \"act-as right\"."
- Effect: the lifetimes, the RFC decisions, the GLOSSARY fix and the word "act-as right" are taken as written. The change adds no word to them.
- Conflict: none.

**RFC 121, accepted by ADR 354 decision 6 (owner, 2026-11-07).** Each decision below is quoted from `docs/rfc/0121-agent-principals.md`.

- Decision 11: "a creator (always the person)". Decision 11 also says: "Only a person delegates. A service principal, a workload or an agent never delegates."
- Decision 12: the three modes `read`, `propose` and `operate`.
- Decision 13: "The server stores the list, not the mode name alone. A later change of what a mode means never widens an existing delegation."
- Decision 14: "A person creates, renews or widens a delegation only in a console session, with a fresh authentication (ADR 227), on the consent page." and "A break-glass session cannot create a delegation, and a request from a break-glass session cannot use one."
- Decision 15: "Renewal extends the **same** delegation ID, in one click on the consent page, with a fresh authentication, with the same or a narrower scope. Each renewal writes an audit entry. A delegation never renews by itself." The lifetime numbers of decision 15 (30 and 90 days) are replaced for a public client by ADR 354 decision 3.
- Decision 16: "Each end writes an audit entry with the cause."
- Decision 17: "An administrator with `server:manage-access` lists and revokes every delegation, and the access review exports them."
- Decision 18: "A delegation is not an act-as edge."
- Decision 19: the installation agent policy "only narrows" and "The API shows the effective value of each item."
- Decision 20 (e): an action that "creates, renews, widens or revokes a delegation" has the agent class `never`.
- Effect: the record, the modes, the console-session rule, the renewal, the ends and the two list-and-revoke routes follow the decisions. The points that no decision settles are the open questions below.
- Conflict: none.

**ADR 380: The delegation in the agent block of an audit entry** (owner-tagged: "Decided by the project owner").

- Decisions that apply: 1, 2 and 3 (current). Changed by: -. Chain to the end: none. Current rule: this ADR.
- Quote, decision 1: "`delegationID` is the stable ID of the delegation of ADR 354 under which the agent acted (ADR 362), and it is empty when the step had no delegation."
- Quote, decision 2: "An entry with any of the seven fields set writes the block."
- Effect: the audit entries of this change follow the block, or not, by the answer to open question 6. The block has no version and no path for old entries.
- Conflict: none.

**ADR 385: A fresh step is opt-in** (owner-tagged: "Decided by the project owner").

- Decisions that apply: 1, 2, 3 and 7 (current). Changed by: -. Chain to the end: none. Current rule: this ADR.
- Quote, decision 2: "When the setting is off, the server asks a person for no fresh authentication."
- Quote, decision 7: "ADR 354 stays: an agent never has a fresh authentication, and an agent holds no right that a person has."
- Effect: whether the fresh step on create and renew follows `freshAuthEnabled` is open question 7. The console-session rule of RFC 121 decision 14 does not depend on the setting.
- Conflict: none known. Open question 7 asks for the answer, because the list of ADR 385 decision 2 does not name a delegation.

**ADR 227: One fresh-authentication window** (decisions 2 and 3 are owner-tagged; decision 6 is not).

- Decisions that apply: 6 (current). Changed by: ADR 301, ADR 320, ADR 339, ADR 354, ADR 373, ADR 385 (chain to the end: ADR 301, 320, 339, 354, 373, 385, 372, 332, 376). Current rule: this ADR, with ADR 301, 320, 339, 354, 373, 385.
- Quote, decision 6: "An access token never gives a fresh authentication."
- Effect: a create or renew with an access token never has a fresh authentication, so it is refused as a console-session rule.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged; this change reads decisions 1 and 13).

- Decisions that apply: 1 (extended) and 13 (changed by ADR 354). Changed by: ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354, 369, 372, 388. Chain to the end: see `docs/adr/INDEX.md`; ADR 354 is the one that changes decision 13. Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354, 369, 372, 388.
- Quote, decision 13: "delegation, that is, an act-as right on a principal of the installation".
- Effect: the old word is "act-as right" from ADR 354 on. The GLOSSARY says it.
- Conflict: none.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decisions that apply: 13, 14, 15 and 16 (changed by ADR 354). Changed by: ADR 128, 129, 210, 297, 311, 314, 354, 367. Chain to the end: ADR 128, 129, 210, 297, 311, 314, 354, 367, 190, 222, 227, 286, 247, 301, 333, 357, 225, 299, 300, 344, 365, 335, 351, 320, 339, 373, 385, 372, 328, 359, 368, 377, 332, 376. Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354, 367.
- Quote, decision 15: "Dusty is not a principal."
- Effect: ADR 354 changes it. The GLOSSARY entry "Dusty" no longer says it.
- Conflict: none, because ADR 354 changes decision 15 and RFC 121 decision 7 says so.

**ADR 125: Agent changes go through the job model as review requests or approve-first** (not owner-tagged).

- Decisions that apply: 15 (changed by ADR 354 decisions 3, 5, 9 and 15; RFC 121 decision 14 supersedes it in part). Changed by: ADR 128, 210, 299, 354. Chain to the end: see `docs/adr/INDEX.md`. Current rule: this ADR, with ADR 128, 210, 299, 354.
- Quote, decision 15: "An agent never uses break-glass."
- Effect: a delegation refuses a break-glass session at create and at use (RFC 121 decision 14).
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged).

- Decisions that apply: 1. Changed by: ADR 363 extends decision 2. Chain to the end: ADR 363. Current rule: this ADR, with ADR 363.
- Quote, decision 1: "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only."
- Effect: the record stores the stable IDs of the person, the agent and the namespaces.
- Conflict: none.

**ADR 240: A state download by a person is a reveal, and a principal has a kind** and **ADR 52: Groups, roles, grants and the access-review export** (neither is owner-tagged; ADR 354 changes and extends them).

- ADR 240 decisions 1, 3 and 4 are changed by ADR 354. Current rule: this ADR, with ADR 297, 354. ADR 52 decisions 1, 3 and 4 are extended by ADR 354. Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354, 374.
- Quote, ADR 354 "Changes": "Extends ADR 52 decisions 1, 3 and 4: a fourth record kind. An agent holds ceiling grants only and is never a member of a group."
- Effect: the agent of a delegation is a principal of the kind `agent`. The GLOSSARY entry "Agent principal" says it.
- Conflict: none.

## Open questions

Open questions: 10, parked

The questions below are not decided. No ADR line and no RFC line settles them. The owner decides each one. This change is not spec-ready until each one has an answer in a repository record. The tasks that depend on an answer say so.

1. **Which authorization action guards the own-row routes?** RFC 121 decision 17 settles only the administrator routes (`server:manage-access`). Options:
   - (a) A self action on `CallerObject` in the family `FamilySelf`, held by every signed-in person through `identity.SelfAllowed`, like `token:manage-own` (`pkg/identity/identity.go:385`) and `caller:manage-git-host-links` (`pkg/identity/identity.go:397`). Cost: one new action and one entry in the allowlist at `pkg/dustpan/dustpan.go:965`. A person cannot be denied the action by a grant, so the installation agent policy is the only brake. A site administrator cannot take the right away from one person.
   - (b) A grant-based action on the namespace objects of the scope. Cost: a person with no grant on a namespace cannot delegate there, which is the intersection rule, but it needs one check for each namespace and it makes "list own" depend on grants that can change.
   - Decides: the owner.
2. **What does the server check on the scope at create?** The intersection of person, ceiling and scope limits the reach at call time (RFC 121 decision 18). No line says what create refuses. Options:
   - (a) Only that each namespace ID exists. Cost: a person can store a scope wider than their reach. The reach at call time is still the intersection, but the consent page shows a scope that does nothing.
   - (b) The person holds at least one read action on each namespace of the scope. Cost: one read check for each namespace at create, and a later loss of access does not change the row.
   - (c) The person holds every action that the mode expands to on each namespace. Cost: the strictest check, and a person with a narrow role cannot pick `operate` even for a smaller list.
   - Decides: the owner.
3. **What is the shape and the name of the lifetimes setting?** ADR 354 decision 3 says "an installation setting holds the lifetimes" and "The API shows the effective values". RFC 121 decision 19 says that the installation agent policy is one stored policy. Options:
   - (a) One setting `agent.delegationLifetimes` with a table for each client type (`public`, `confidential`, `server`) of a default and a maximum, read at `GET /api/dustpan/v1/delegations/settings`. Cost: one more stored shape, which RFC 121 decision 19 may later absorb.
   - (b) Separate keys for each client type and value in the generic settings API of ADR 212. Cost: six keys, and the effective values are in six places.
   - (c) Put the lifetimes in the installation agent policy of RFC 121 decision 19 when its slice lands, and fix the values in code until then. Cost: the setting that ADR 354 decision 3 asks for does not exist in this change.
   - Decides: the owner. This change uses only the client type `server` (Dusty).
4. **What are the rules of renewal?** RFC 121 decision 15 gives "the same delegation ID", "the same or a narrower scope", an audit entry and no automatic renewal. It does not say:
   - whether the maximum lifetime counts from the creation or from each renewal,
   - whether an expired delegation can be renewed, or only a live one,
   - whether a revoked or an ended delegation can be renewed.
   Options:
   - (a) The maximum counts from each renewal, only a live delegation renews, and an ended one needs a new delegation. Cost: a delegation can last for ever by renewal, with a fresh authentication each time. A new ID after an end keeps the audit chain clear.
   - (b) The maximum counts from the creation, and only a live delegation renews. Cost: a person must delegate again after 90 days in all cases.
   - (c) As (a), and an expired delegation can renew for a grace time that a setting holds. Cost: one more setting, and a row that was dead is live again with the same ID.
   - Decides: the owner.
5. **What writes the end entry when no request causes the end?** RFC 121 decision 16 says that each end writes an audit entry with the cause and that the work stops "at the next resolve". Expiry and a disabled person or agent are not requests. Options:
   - (a) A sweep job of the server writes the entry at the end time. Cost: a durable operation, a fence, and a delay of up to one sweep period between the end and the entry.
   - (b) The first resolve after the end writes the entry, in the same transaction as its refusal. Cost: a delegation that nobody uses never gets an entry until someone reads it, so the list route must also write it.
   - (c) Both: the resolve writes it at once and a sweep writes the rest. Cost: the two writers need one idempotency key, the delegation ID with the cause.
   - Decides: the owner.
6. **Do the lifecycle entries of a person carry the agent block?** The owner relayed that the entries for create, renew and end use the block of ADR 380. ADR 380 decision 1 defines `delegationID` as the delegation "under which the agent acted". A create or a revoke by a person is not a step of an agent. On main, `audit.NewOrigin` refuses a block with only `delegationID` set (`pkg/audit/audit.go:256`: a block with no run and no session is partial unless `breakGlass` is set). ADR 380 decision 2 says that an entry with any field set writes the block. Options:
   - (a) The lifecycle entries carry the block with only `delegationID`, and `NewOrigin` accepts that. Cost: a change of the constructor that ADR 380 decision 2 asks for, and the filter "everything under this delegation" finds the lifecycle entries too, which RFC 121 decision 51 asks for.
   - (b) The lifecycle entries carry no block. The delegation is the object of the entry, and the filter of RFC 121 decision 51 matches the object too. Cost: two places to match in the audit query.
   - Decides: the owner.
7. **Does the fresh step of create and renew follow `freshAuthEnabled`?** RFC 121 decision 14 says "with a fresh authentication (ADR 227)". ADR 385 decision 2 lists the steps that the setting turns off and does not name a delegation. The console-session rule holds in each option. Options:
   - (a) Create and renew need the fresh step only when `freshAuthEnabled` is on, as a confirmation does (ADR 385 decision 2). Cost: with the default off, a stolen console session can create a delegation with no second factor.
   - (b) Create and renew always need the fresh step. Cost: an installation with no factor enrolled cannot delegate (ADR 301 decision 4: "No factor, no fresh step"), and it contradicts "off means no step".
   - Decides: the owner. Revoke needs no fresh step in both options (see the change `revoke-stops-work`).
8. **What is the table of mode to actions?** RFC 121 decision 13 stores the expanded list. The expansion needs the agent class of each catalog action (RFC 121 decision 20). The class is not on main, and no change in `openspec/changes/` makes it. Options:
   - (a) This change depends on the change for the agent class, and the table is its output. Cost: this change waits.
   - (b) This change stores the list that the caller sends and checks it against the modes by a fixed table in this change. Cost: the table is a decision on security semantics that the owner has not taken.
   - Decides: the owner.
9. **Does the code identifier `access.FamilyDelegation` change now?** The GLOSSARY now says "act-as" and keeps the code word. `FamilyDelegation` has the wire value `delegation` (`pkg/access/access.go:110`) and is used in 11 files of `pkg` and `internal`. Item 2 of the one-way doors of RFC 121 lists "the names of the members of the group `delegation`" among the shapes that are hard to change after the first tag. Options:
   - (a) Rename the identifier and the wire value to `act-as` in a separate preflight before the first tag. Cost: one mechanical change, the guide `docs/guides/roles-and-grants.md:47` and the pin test change, and the wire value of stored roles changes before any tag.
   - (b) Keep the wire value `delegation` and say "act-as" only in the docs. Cost: the word means two things in code, and a reader of the API sees `delegation` for the act-as family and for the new record.
   - Decides: the owner.
10. **What happens to the scope when a listed namespace is deleted?** The record stores the stable namespace ID (ADR 362). No decision says what a delete does to a live delegation that names it. Options:
   - (a) The store refuses to delete a namespace that a live delegation names. Cost: a delete needs a revoke first, and a person who left blocks the delete until an administrator revokes the row.
   - (b) The row keeps the ID, and the intersection gives no reach there. Cost: the list shows a scope that names nothing, and a new namespace never reuses an ID, so there is no leak.
   - (c) The delete removes the ID from the list of each live delegation and writes one audit entry for each. Cost: a write inside the delete, and a delegation can end up with an empty scope.
   - Decides: the owner.

Questions considered and answered:

- May a person delegate for another person? No. RFC 121 decision 11: "Only a person delegates", and the creator is always the person.
- May an agent create, renew, widen or revoke a delegation? No. RFC 121 decision 20 (e) gives such an action the agent class `never`.
- May a break-glass session create a delegation? No. RFC 121 decision 14.
- Does this change enforce a delegated call? No. Enforcement is RFC 121 slice 3.
- May the person who delegated later confirm what the agent proposed? Yes by default (ADR 354 decision 1). It is the subject of the change `person-confirms`.
- Is the lifetime a fixed number? No. ADR 354 decision 3 makes it an installation setting with the effective values in the API. The shape is open question 3.
- Does a delegation need the model connect change in this pull request? No. The record stores the stable ID of the agent principal. Creating Dusty is a dependency to be written.
