---
version: 2
---

Issue: #895, #903.

## Why

**Persona: developer.** A Developer reviews a change in the forge or in OpenDustpan and wants both places to show the same approval without a second manual step.

The user path this change must make work:

1. **Console.** Developer approves on the review request in the forge; the change order shows the pending review then the signature after the fresh step.
2. **API.** Developer approves on the change order; the review request shows the review of the person.
3. **Docs page.** Developer reads the approvals page that names the OAuth link and the fresh-authentication window.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Approval outside the fresh-authentication window | 403 with error code mfa.fresh-step-required. The text names the path of a new sign-in. Complete a fresh step, then retry the same approve call. | `POST /api/dustpan/v1/change-orders/{id}/approvals` |
| Approve on a version-control unit without OAuth | 403 with a reason that names the missing OAuth link. Link the forge account, then retry. | `POST /api/dustpan/v1/me/linked-account-requests` |

Evidence for the claims above:

- Issue 895 asks for two-way mirroring with OAuth and a read-only view without OAuth. Proof: In: forge review events mapped to change-order reviews for the same principal (OAuth link); change-order approvals posted to the forge as the person's review; a read-only change-order view when the connection has no OAuth.
- Issue 903 asks for fresh authentication on destroy approvals and a namespace switch for all approvals. Proof: 13.9 (owner, 2026-10-18): fresh MFA for approvals of a destroy, always; for all approvals when a namespace switch is on.
- Main has a route for linked-account requests. Proof: Route table: ANY /api/dustpan/v1/me/linked-account-requests.

## What Changes

- Forge approve and request-changes events map to pending reviews for OAuth-linked principals keyed on stable forge user ID, then to signatures after the fresh check, each with one audit entry
- Approve and request-changes refuse on version-control units without OAuth with a reason that names the link
- Namespace switch freshAuthForApprovals extends the fresh need to all approvals with window freshAuthSeconds 60 to 900 or 0
- New route POST /api/dustpan/v1/change-orders/{id}/approvals with its authorization actions for approve and request-changes
- New route GET /api/dustpan/v1/change-orders/{id} with its read authorization action for the change-order view

Out of scope:

- Comment commands on the review request
- SCIM or group sync for the OAuth link
- Forge webhook intake and event hint flow shape
- Approval rule evaluation itself

Depends on:

- git-host-webhook-intake-post
- approval-defaults-take-effect-live
- policy-approvals
- `git-host-webhook-intake-post` (order of the milestone review)
- `approval-defaults-take-effect-live` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A Developer with no OAuth link sees no approve control and the refusal names the link step.
- A Developer outside the window learns the sign-in path from the refusal.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a forge service with audit tests. (`pkg/forge/service_test.go:501 (func TestCheckAuditsItsOutcome)`)
- Main has approval audit chain tests for concurrent approvals. (`pkg/dustpan/auditchain_approval_test.go:108 (func TestConcurrentApprovalsLeaveOneEntryAndOneOperation)`)
- Main has an OAuth helper package. (`internal/oauth`)
- Main has a linked-account request route. (`route table or API description: ANY /api/dustpan/v1/me/linked-account-requests`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3, 7).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Effect: This change builds the approve route and the namespace switch through the settings resource. No change to the decision words.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships its user page in the same pull request.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2, 3).

- Decisions that apply: 1 (extended), 2 (current), 3 (current), 4 (current), 5 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A fresh authentication. A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71)."
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Quote: "0 asks every time (owner, 2026-10-18). A window of 0 asks for a fresh authentication at each step."
- Quote: "Narrower is stricter. A namespace and an approval rule (`freshAuthSeconds`, ADR 225) can set a shorter window or 0."
- Quote: "Signatures. A signature of a change order needs a fresh authentication when its approval rule has `freshAuthSeconds`, when the change holds a `destroy`, or when the namespace has `freshAuthForApprovals` (ADR 225 decision 5)."
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3)."
- Effect: Every mirrored and direct signature uses the one window, the shortest window wins, and the same refusal. No change to the decision words.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A factor of OpenDustpan gives the fresh step (owner). For a person who signed in with a plain OAuth 2.0 provider or with the trusted header of a proxy, the fresh authentication of ADR 227 decision 1 is a passkey, a TOTP code or an SSH signature of that person."
- Quote: "No factor, no fresh step (owner). A person of these methods who has enrolled no factor cannot do a step that needs a fresh authentication."
- Effect: A linked OAuth person proves presence with an OpenDustpan factor; a person with no enrolled factor gets the refusal. No change to the decision words.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: main_facts[2]: "where" is not file:line (func Name); a code check fails: main_facts[3]: "where" is not file:line (func Name).

Questions considered and answered:

- How does an approval made in the forge meet the fresh-authentication window on the OpenDustpan side? A forge event alone never proves presence. It creates a pending review. A signature needs a fresh OpenDustpan step of the linked principal inside the window (ADR 227 decisions 1, 2, 5, 6; ADR 301 decision 1).
- Does mirroring cover comment commands? No. Issue 895 keeps comment commands out.
- What happens when the forge never receives a mirrored approval? The change order is the source of truth. The signature stays, the post retries 3 times, then a dead-letter audit entry records the divergence.
- What supersedes or retracts a mirrored signature? A later event supersedes an earlier one for the same person and unit by event time, with event ID tie-break. Unlinking never deletes a past signature.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
