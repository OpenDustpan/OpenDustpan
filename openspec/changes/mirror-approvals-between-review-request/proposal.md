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
| Approval outside the fresh-authentication window | 403 with error code `mfa.fresh-step-required` and the path of a new sign-in; nothing changes. | `POST /api/dustpan/v1/change-orders/{id}/approvals` |
| Approve on a version-control unit without OAuth | 403 with a reason that names the missing OAuth link; the change-order page shows no approve control. | `GET /api/dustpan/v1/change-orders/{id}` |

Evidence for the claims above:

- Issue 895 asks for two-way mirroring with OAuth and a read-only view without OAuth. Proof: In: forge review events mapped to change-order reviews for the same principal (OAuth link); change-order approvals posted to the forge as the person's review; a read-only change-order view when the connection has no OAuth.
- Issue 903 asks for fresh authentication on destroy approvals and a namespace switch for all approvals. Proof: 13.9 (owner, 2026-10-18): fresh MFA for approvals of a destroy, always; for all approvals when a namespace switch is on.

## What Changes

- Forge approve and request-changes events map to pending reviews for OAuth-linked principals, then to signatures after the fresh check owned by policy-approvals, each with one audit entry
- Approve and request-changes refuse on version-control units without OAuth with a reason that names the link
- Namespace switch extends the fresh need to all approvals with window 60 to 900 or 0
- New route POST /api/dustpan/v1/change-orders/{id}/approvals with its authorization actions for approve and request-changes
- New route GET /api/dustpan/v1/change-orders/{id} with its read authorization action for the change-order view

Out of scope:

- Comment commands on the review request
- SCIM or group sync for the OAuth link
- Review modes 2 and 4 behavior (owned by pull-request-automation-first-slice and apply-after-merge; this change only mirrors inside those modes)
- Destroy fresh-authentication check itself (owned by policy-approvals; this change only calls it for mirrored signatures)
- Forge webhook intake and event hint flow (owned by git-host-webhook-intake-post)
- Approval defaults at a live decision point (owned by approval-defaults-take-effect-live)

Depends on:

- git-host-webhook-intake-post
- approval-defaults-take-effect-live
- policy-approvals
- `git-host-webhook-intake-post` (order of the milestone review)
- `approval-defaults-take-effect-live` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A Developer with no OAuth link sees no approve control and the refusal names the link step.
- A Developer outside the window learns the one sign-in path from the refusal.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3, 7).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Effect: Mirrored signatures follow the fresh step rule for approvals and the namespace switch is a namespace setting through the settings resource. No change to the decision words.
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
- Effect: Every mirrored signature uses the one window, the shortest window wins, and the same refusal. No change to the decision words.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A factor of OpenDustpan gives the fresh step (owner). For a person who signed in with a plain OAuth 2.0 provider or with the trusted header of a proxy, the fresh authentication of ADR 227 decision 1 is a passkey, a TOTP code or an SSH signature of that person."
- Quote: "No factor, no fresh step (owner). A person of these methods who has enrolled no factor cannot do a step that needs a fresh authentication."
- Effect: A linked OAuth person proves presence with an OpenDustpan factor; a person with no enrolled factor gets the refusal. No change to the decision words.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md Open question 1 leaves open how a forge approval meets the fresh-authentication window; the provisional answer is not recorded and the spec scenario 'Forge approve becomes signature' requires only the linked principal inside the window without saying how the window is proven for a forge event. Resolution: answered: Forge event creates only a pending review; signature needs a fresh OpenDustpan step of the linked principal at write time per design and mirror-in requirements.
- R1.2 (missing scenario): specs/change-order-approvals/spec.md has no scenario for a stale or missing fresh proof on the mirrored forge path, although design.md Layers names the handler hostile test (stale fresh proof on destroy answers 403 mfa.fresh-step-required); the spec omits the mirrored-in case. Resolution: answered: Mirror-in stale proof refused requirement with 403 scenario covers the forge path.
- R1.3 (failure mode): design.md Risks names a replayed forge event writing a duplicate signature, but no requirement, scenario or layer test covers idempotency or dedup of forge events. Resolution: answered: Replay idempotency requirement keeps one signature per event ID, including concurrent delivery.
- R1.4 (failure mode): design.md Risks names a forge identity linked to the wrong principal granting a false approval; the link resolution, its ownership check and its hostile test are not specified in the spec or layers table. Resolution: answered: Service layer plus link-ownership requirement refuse unlinked or mismatched identities at point of use.
- R1.5 (missing scenario): The 'Post OpenDustpan approval to forge' requirement does not cover a failed forge post (outage, rejected token, revoked OAuth link); it is unclear whether the signature is kept, rolled back or retried, and the audit entry for the failed direction is not defined. Resolution: answered: Forge-post failure requirement keeps signature, retries with backoff, and writes dead-letter audit entry with change order as source of truth.
- R1.6 (wrong claim): proposal.md cites ADR 227 decision 5 and decision 6 as answering the forge window question, but decision 5 covers signatures of a change order and decision 6 covers the refusal shape; neither describes how a forge review proves presence, so the claim 'answered' is unsupported. Resolution: answered: Corrected claim: ADR 227 alone never blesses forge presence; design and mirror-in requirements demand ADR 227 decision 1 proof via ADR 301 factor.
- R1.7 (adr conflict): ADR 212 decision 3 (owner): 'Fresh MFA for a change that reduces security, and for approvals.' ADR 301 decision 4 (owner) refuses a step for a person with no enrolled factor; the mirror path for a linked OAuth principal with no enrolled factor is not addressed, so the change may allow an approval ADR 301 refuses. Resolution: answered: ADR 301 in adr_check with no-enrolled-factor refusal requirement and scenario.
- R1.8 (missing scenario): The window-0 and namespace-switch scenarios name no error for an unlinked principal on a non-VCS unit, and no scenario covers a switch change in the middle of an approval. Resolution: answered: Window 0 covered in stale-proof and window-edges tests; non-VCS unlinked API case covered by link check scenario.
- R1.9 (scope): tasks.md 2.2 and proposal.md name routes POST /api/dustpan/v1/change-orders/{id}/approvals and GET /api/dustpan/v1/change-orders/{id}; the code check fails because these routes are not in the API description or route table, and no task adds them as new routes with authorization actions. Resolution: answered: what_changes and tasks name the new approve and read routes with authorization actions.
- R1.10 (open question): The spec says the switch extends the need to all approvals with window 60 to 900 or 0, but no scenario covers a namespace with an approval rule freshAuthSeconds narrower than the installation window (ADR 227 decision 4 narrower is stricter). Resolution: answered: Narrower-rule-window requirement added per ADR 227 decision 4.
- R1.11 (scope): tasks.md 3.2 asks the docs page to cover the namespace switch, which is owned by #903; the mirror change also claims the switch in What Changes, so the ownership between #895 and #903 is unclear. Resolution: answered: This change covers #895 and #903 per item source; switch settings task uses the settings resource without claiming other work of #903.
- R2.1 (open question): The change does not answer how a forge approval proves presence inside the fresh-authentication window; proposal.md leaves it as open question 1 with a provisional answer, but the spec scenario 'Forge approve becomes signature' still needs that answer to be built, and the spec-ready gate requires 'none'. Resolution: answered: Removed open question; forge event creates only a pending review and the signature needs a fresh OpenDustpan step of the linked principal at write time.
- R2.2 (wrong claim): The ADR check cites ADR 301 decision 1 and ADR 227 decision 1 as the fresh step, but the forge path is a forge event, not a sign-in of the person in OpenDustpan; the claim that the mirrored path meets decision 4 is not supported by the input, which does not show how a forge-only approval is linked to a factor. Resolution: answered: Corrected claim: design and mirror-in requirements state forge authentication alone never satisfies ADR 227; signature needs ADR 227 decision 1 proof via ADR 301 factor.
- R2.3 (adr conflict): ADR 227 decision 5 says a signature needs a fresh authentication when its approval rule has freshAuthSeconds, the change holds a destroy, or the namespace has freshAuthForApprovals. The spec's 'Namespace switch for all approvals' scenario and the 'Destroy approval outside window refused' scenario name only the window, not the approval-rule condition, so a rule-level freshAuthSeconds approval outside the window is not covered. Resolution: answered: Added approval-rule freshAuthSeconds requirement and scenario.
- R2.4 (missing scenario): No scenario covers an approval through the API by a linked principal on a non-VCS unit, nor an approval where the OAuth link is revoked or changed between the forge event and the signature write; the link ownership check is stated only at intake, not at the point of use for the API path. Resolution: answered: Added API-path link check requirement and revoked-link scenario at point of use.
- R2.5 (failure mode): The forge-post failure requirement queues one retry but names no bound, backoff or dead-letter end state; a permanently rejected token leaves the change order showing an approval the forge never received, with no defined reconciliation. Resolution: answered: Bounded retries with backoff and dead-letter audit entry; change order named as source of truth in retry-exhausted requirement.
- R2.6 (failure mode): Replay idempotency is keyed on the forge event ID only; a replay of an approve that was later followed by a request-changes or a revoked OAuth link is not addressed, and a concurrent duplicate delivery race between two intake calls is not covered. Resolution: answered: Added supersede and concurrent-delivery requirement keyed on event ID with later-event-wins and single-insert race rule.
- R2.7 (missing scenario): The request-changes path has no requirement or scenario at all, although the proposal and the Refuse-without-OAuth requirement name approve and request-changes; the mirror of request-changes from the forge is not specified. Resolution: answered: Added request-changes mirror requirement with forge and OpenDustpan scenarios.
- R2.8 (scope): Tasks 2.2 and 3.2 are the only tasks that cover the namespace switch and destroy fresh-step checks of issue 903, but the proposal's 'Depends on' lists #897 and #901 without the switch's settings API task; the namespace switch is delivered in this change without a settings-API task, although ADR 212 decision 7 requires the settings resource. Resolution: answered: Added namespace-switch settings task through PATCH namespace settings per ADR 212 decision 7.
- R2.9 (open question): Replay of a mirrored approval after the forge user later unlinks is not decided; the design Risks name the false approval case but no owner answers who may retract a mirrored signature. Resolution: answered: Decided: unlink blocks new mirrored signatures and never deletes past signatures; retract needs a new explicit signature.
- R2.10 (adr conflict): The ADR check records ADR 212 decision 3 as 'Effect: no change to the decision words', yet the change adds an approval refusal shape for units without OAuth whose error code forge.oauth-required is not in any cited ADR; the new error code is not checked against the error-code rules. Resolution: answered: Removed new error code; without-OAuth refusal uses 403 with a reason string and no new machine code.
- M.14 (gap): Review modes 2 and 4 ship in this milestone (owner, ADR 328), but no change specifies them. The destroy fresh step duplicates policy-approvals, and accept-uploaded-plans-team-s also adds GET /change-orders/{id}. Resolution: answered: Named owners for review modes 2 and 4, kept GET change-order route here, delegated destroy fresh check to policy-approvals, and replaced issue-number depends_on with change dependencies on git-host-webhook-intake-post, approval-defaults-take-effect-live and policy-approvals.

## Open questions

none

Questions considered and answered:

- How does an approval made in the forge meet the fresh-authentication window on the OpenDustpan side? No. A forge event alone never proves presence. A forge approve creates no signature until the linked principal completes a fresh OpenDustpan step inside the window; the event waits as a pending review. This follows ADR 227 decisions 1, 2, 5 and 6 and ADR 301 decision 1, so no open question stays.
- Does mirroring cover comment commands? No. Issue 895 scope keeps comment commands out; only approve, request-changes and dismiss events map, and only review posts go back to the forge.
- What happens when the forge never receives a mirrored approval? Change order is the source of truth when the forge post fails. The signature stays, retries are bounded, and a dead-letter audit entry records the divergence until a later sync repairs it.
- What supersedes or retracts a mirrored signature? A later forge event supersedes an earlier one for the same person and unit; revocation of the OAuth link blocks new mirrored signatures but never deletes a past signature, which only a new signature or an explicit retract removes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
