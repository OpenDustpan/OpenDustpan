Roadmap: `docs/roadmap.md:212` (Console).

## Why

**Persona: site administrator.** A site administrator tries to give each person and each pipeline only the access they need. They add a member, make a group, give and revoke a grant, review the access-review export, warn about a token that expires soon, and explain a locked section with the reason that the server gives.

The user path this change must make work:

1. **Console.** Open the members, groups and grants pages and add a member, make a group, give a grant on a collection and revoke it.
2. **Console.** Download the access-review export and confirm the expiring-token banner.
3. **Console.** Open a section without the grant and read the server reason for the locked section.
4. **Docs page.** Follow the docs page commands against the real server over TLS.
5. **E2e.** Run the console browser test job against the suite server and check the audit entries for each step.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A console section that the principal cannot open is refused. | 200 access answer with allowed false and a generic reason that names the grant to ask for and hides whether the object exists. | `GET /api/dustpan/v1/me/access?object={object}` |

Evidence for the claims above:

- The roadmap step requires browser tests that add a member, make a group, give and revoke a grant, download the access-review export, show the expiring-token banner and show the server reason for a locked section. Proof: The browser tests of the console repository run against the server of the suite. They add a member, make a group, give and revoke a grant, and download the access-review export. They show the banner of a token that expires soon. A section that the principal cannot open shows the reason that the server gives.
- The console scope covers members, groups, grants and the access-review export under RFC 19, and locked sections are proposed under RFC 52 which the roadmap marks as draft. Proof: | console | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | members, groups, grants, access-review export | discussing; groups, grants and export accepted (ADR 52) |

## What Changes

- Browser-test wiring for the console repository tests against the real server over TLS.
- Locked console sections show the generic reason from the access endpoint.
- Docs page for the console access views with copy-paste commands.

Out of scope:

- Member, group, grant and access-review export routes and actions, covered by groups-roles-grants.
- Token warning field and expiry banner threshold, covered by token-life-cycle-expiry-email.
- Caller access route path under /me, covered by move-caller-routes-api-server.
- Sign-in pages, MFA pages and SCIM pages.

Depends on:

- groups-roles-grants
- move-caller-routes-api-server
- planner-role
- token-life-cycle-expiry-email
- `groups-roles-grants` (order of the milestone review)
- `token-life-cycle-expiry-email` (order of the milestone review)
- `planner-role` (order of the milestone review)
- `move-caller-routes-api-server` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The path still costs the administrator one context switch between the server repository and the console repository when a browser test fails.
- The generic locked-section reason hides object names, so an administrator asks a peer for the grant name.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 14 (current).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "An endpoint answers the access of a caller. One endpoint answers what a principal can do on one object, and which grants give it. The console uses this endpoint to show what a person can do. The console does not compute access in the browser (ADR 17)."
- Effect: The change reads the access endpoint without changing it. The console never computes access in the browser.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): proposal.md ends the adversarial pass with 13 items marked 'Resolution: open', yet the Open questions section reads 'none'. The spec-ready gate (project.md rule 3) cannot pass while these items stay open. Resolution: open question: Open questions now list the undecided items instead of none.
- R1.2 (adr conflict): specs/console-views/spec.md 'Download snapshot export' lists a 'creator' field. ADR 52 decision 11 names only principal or group, role, object, grant time, last sign-in or token use, and grant end. The spec also omits the 'Never' value that decision 11 requires. Resolution: answered: Export requirement lists principal or group, role, object, grant time, last use with Never value.
- R1.3 (open question): ADR 52 decision 13 says the export needs its own action, for example namespace:read-access. design.md Decision 1 adds no action and the change does not name or check the export action. The change cannot be built without this answer. Resolution: open question: Kept as open question on the exact export action, now split by scope.
- R1.4 (open question): ADR 215 decision 9 gives every principal and group a home (installation or one namespace), and decision 10 makes the home a setting. The change does not say which home a member or group made in the console gets. Resolution: open question: Kept as open question on the home of a console-made member or group.
- R1.5 (failure mode): The locked-section 403 reason can name an object the principal cannot see, which proves the object exists. No scenario, test or hostile test covers this leak, although the Risks section names it. Resolution: answered: Design uses access endpoint with generic reason plus redaction layer and hostile test.
- R1.6 (open question): The expiring-token banner has no threshold for 'expires soon' and no owner for it. The renew step has no route or behavior, so the banner scenario cannot be written as a failing test. Resolution: open question: Kept as open question on banner threshold and renew step.
- R1.7 (adr conflict): ADR 52 decision 14 says the console uses GET .../access?object= to show what a person can do. design.md Decision 2 takes the locked-section text from the 403 reason, and the spec and tasks never use the access endpoint. The change must choose one path. Resolution: answered: Design and specs use the access endpoint, not the 403 text.
- R1.8 (missing scenario): The error table lists grant.forbidden (403) and token.expired (401), but the spec has no scenario for either error and tasks.md has no test for them. Resolution: answered: Added grant.forbidden and token.expired scenarios and tests.
- R1.9 (missing scenario): ADR 67 decision 6 requires the console to show the principals and groups that can make tokens of a service principal before a grant is sent. ADR 223 decision 9 requires the console to show access limits next to the access-review grants. The change covers neither, and the spec has no scenario for either. Resolution: answered: Added token-maker list and access-limits scenarios.
- R1.10 (failure mode): The change does not say what happens when a grant or membership ends while a console page is open, or when two administrators revoke the same grant at once. The second revoke is not defined. Resolution: open question: Second-revoke scenario kept with defined repeat answer; audit rule is now an open question.
- R1.11 (missing scenario): ADR 52 decision 13 says each export can write an audit entry. The spec has no scenario that requires one, and no history export between two timestamps (ADR 52 decision 10) is named as out of scope. Resolution: answered: Added export audit-entry scenario and marked history export out of scope.
- R1.12 (wrong claim): The ADR check defines owner-tagged as also covering ADRs that record decisions of the project owner. project.md defines an owner tag only by the text '(owner)' or '(owner, ADR N)' or the title. The definition in the change is wrong. Resolution: answered: No extra owner-tag definition is used.
- R2.1 (missing scenario): spec.md 'Probe unseen object' does not require a nonexistent object to get the same answer as an unseen object (status, body, reason and timing). Without this, the access answer still reveals existence, which design.md layer 'reason redaction' says must not happen. Resolution: answered: Locked-section requirement has same-answer scenario; redaction layer checks status, body and reason, with timing best-effort excluding payload size.
- R2.2 (missing scenario): The grant give scenario has no reach check. ADR 70 decision 3 says a grant of a role with principal:manage-tokens on a principal needs a granter whose reach covers that principal. The spec and tasks do not test this refusal. Resolution: answered: Refused-grant requirement has reach-cover scenario for principal:manage-tokens grants.
- R2.3 (open question): The token-maker list (ADR 67 decision 6) has no route, no authorization action and no rule on who may read it. Task 1.2 cannot be written as a failing test without these. Resolution: open question: Kept as open question on the token-maker list route, action and reader rule.
- R2.4 (failure mode): The CSV made by the console from principal, group and object names has no scenario for formula injection from hostile names. The hostile test layer does not cover it. Resolution: answered: Server keeps hostile names raw; console repository test covers CSV escaping.
- R2.5 (wrong claim): The error table names GET .../units as the command for token.expired. That command does not renew a token, so the error does not name the command that helps, as the voice rule requires. Resolution: answered: Token-expired error says sign in again before POST /api/dustpan/v1/me/tokens.
- R2.6 (wrong claim): The evidence cites 'Milestone 4 step 13'. The roadmap input has no milestone number, so the cited proof does not support the claim. Resolution: answered: Evidence claim now states the roadmap requirement, not an existing test.
- R2.7 (missing scenario): ADR 52 decision 10 lets the export cover every grant. The spec only covers a request for one namespace, with no scenario for an installation-wide export by a site administrator. Resolution: answered: Export requirement has an installation-wide scenario for a site administrator.
- R2.8 (failure mode): The second-revoke scenario does not state the status code or error code of the repeated revoke, and does not say whether it writes an audit entry. Clients cannot build against it. Resolution: open question: Second-revoke scenario states 200 already-ended; audit behavior moved to open question.
- R3.1 (failure mode): proposal.md error table says grant.forbidden 403 names the action and object that failed, but design.md layer 'reason redaction' requires 403 answers to never name an unseen object. The two rules conflict for a principal without the grant on an unseen object, and the change does not choose one. Resolution: answered: Error table and redaction layer now agree: 403 names the grant to ask for and hides an unseen object name.
- R3.2 (adr conflict): specs/console-views/spec.md 'Download snapshot export' and 'Installation-wide export' use the namespace-scoped action namespace:read-access on an installation-wide export. ADR 216 decision 3 says: "Operator actions keep the area `installation:`. A namespace action takes the area `namespace:` and has the namespace as its object." The change does not say which action covers an installation object. Resolution: open question: Installation-wide export uses an installation action per ADR 216; exact action is an open question.
- R3.3 (wrong claim): proposal.md ADR check defines owner-tagged as also covering ADRs that record decisions of the project owner. openspec/project.md defines an owner tag only by the text '(owner)' or '(owner, ADR N)', or by the ADR title. The extra definition is wrong. Resolution: answered: No extra owner-tag definition is used.
- R3.4 (wrong claim): proposal.md evidence says the console browser tests 'add a member, make a group ... and show the server reason' as existing proof. The input contains no such test, and the tasks list them as work to do (task 2.2). The claim is unchecked and needs its command output. Resolution: answered: Evidence claim states the roadmap requirement, not existing test proof.
- R3.5 (scope): proposal.md places the locked-section scope under RFC 52 (Console core views), but the roadmap row marks RFC 52 as draft. The locked-section requirement rests on a draft RFC, and the proposal does not say so. Resolution: answered: Design context and risks state that the locked-section view follows draft RFC 52.
- R3.6 (failure mode): specs/console-views/spec.md and tasks.md 1.2 do not say how the CSV escaping is tested on the server side. The hostile test checks only the raw JSON, and the console repository escapes the field outside this change's tests, so a missing escape would not be caught here. Resolution: answered: Server test checks raw JSON; console repository test checks CSV escaping.
- R3.7 (open question): specs/console-views/spec.md 'Second revoke of same grant' answers 200 with already-ended state and no audit entry. No ADR decides the repeat-revoke answer, and the change takes it silently. ADR 52 decision 15 requires an audit entry for each change. Resolution: open question: Repeat-revoke audit behavior is now an open question citing ADR 52 decision 15.
- R3.8 (failure mode): proposal.md error table for token.expired names POST /api/dustpan/v1/me/tokens as the command that helps. A request with an expired access token cannot create a token, since ADR 227 says an access token never gives a fresh authentication. The named command may not work, and the change does not say how the person re-signs in. Resolution: answered: Token-expired error says sign in again before token create since a token never gives fresh authentication.
- R3.9 (missing scenario): specs/console-views/spec.md 'Same answer for unseen and nonexistent' requires equal timing, but no scenario, limit or test covers pagination or size limits of the access answer and export snapshot. A large snapshot path is not specified. Resolution: open question: Timing check excludes payload size; export pagination is now an open question.
- M.12 (duplicate): The server work for the grant round trip, the access-review export, the export actions (open questions 0, 1 and 5) and the banner threshold (open question 3) belongs to groups-roles-grants and token-life-cycle-expiry-email. Locked sections read /me/access, which move-caller-routes-api-server moves. Resolution: answered: Moved grant round trip, export actions and banner threshold to depends_on changes. This change now keeps only browser-test wiring and the locked-section reason through the access endpoint.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What reason does a locked console section show?
   - Options: generic reason naming the grant to ask for; detailed reason naming the object.
   - Provisional decision (owner review 14 Nov): generic reason naming the grant to ask for
   - Reason: The server must not confirm an object the principal cannot see. Benchmark: Google Cloud consoles show access-denied without confirming resource existence, per ISO 27002 least privilege.
   - Persona impact: Generic text costs one extra ask for help. Detailed text leaks existence to an ungranted principal. Escape hatch: Full reason stays in the audit entry for administrators with the export action.

Questions considered and answered:

- Can the console compute what a person can do in the browser? No. ADR 52 decision 14 answers it. The console calls the access endpoint and never computes access in the browser.
- Does this change define grant routes, export actions or the expiry threshold? They belong to groups-roles-grants, token-life-cycle-expiry-email and move-caller-routes-api-server. This change only wires the browser tests and shows the locked-section reason from the access endpoint.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
