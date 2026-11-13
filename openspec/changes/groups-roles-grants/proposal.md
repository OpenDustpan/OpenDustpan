Roadmap: `docs/roadmap.md:159` (Groups, roles and grants).

## Why

**Persona: site administrator.** A site administrator shares one installation between teams and CI pipelines. Each team gets only the access it needs on its own collection, each change is on record, and periodic review lists each grant with its holder, role, object, creator and time.

The user path this change must make work:

1. **API.** Create group, add member, grant writer on collection A through the API or console
2. **CLI.** Run pulumi up and tofu apply on a unit of collection A and on a unit of collection B
3. **CLI.** Run pulumi stack export and tofu state pull with a viewer grant
4. **API.** Revoke the grant and retry the CLI command
5. **Console.** Download the access-review export for snapshot and history
6. **Docs page.** Follow the roles and grants docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Write with writer on collection A to a unit of collection B | 403 with code unit.forbidden and no change; run the access-review export to see the grant | `GET /api/dustpan/v1/namespaces/{namespace}/access-review` |
| Viewer runs pulumi up or tofu apply | 403 with code unit.forbidden and no change; run stack export or state pull for a read | `pulumi stack export` |
| Use of a token after its only grant ends | 403 with code grant.revoked on the next use of the same token; make a new grant and retry | `pulumi stack export` |
| Caller without the export action asks for the access-review export | 403 with code access.export-forbidden and no export; ask an administrator for the export grant | `GET /api/dustpan/v1/namespaces/{namespace}/access-review` |

Evidence for the claims above:

- E2E covers collection access and the access-review export Proof: e2e/access_test.go, e2e/access_review_test.go
- Guides describe roles, grants and the access-review export Proof: docs/guides/roles-and-grants.md, docs/guides/access-review.md
- The keyword Holder names e2e and guide evidence for this change Proof: e2e/access_review_test.go, e2e/access_test.go, docs/guides/access-review.md, docs/guides/roles-and-grants.md

## What Changes

- Enforce writer scope per collection for Pulumi and OpenTofu writes with 403 outside the granted collection
- Enforce viewer read-only for stack export and state pull with 403 on up and apply
- Apply grant and membership revocation on the next request with no cached allow
- Serve access-review snapshot and history export with holder, role, object, creator, time, last use and end time

Out of scope:

- Planner role
- Token life cycle and expiry email
- SCIM provisioning
- Placement of new units into collections

Depends on:

- check-reach-when-grant-membership
- planner-role

## Usability

Where the path still costs the person time or surprises them:

- A writer refused on another collection sees two collection names and the grant that is missing.
- The export shows the reason a section stays closed when the caller lacks the export action.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 2 (changed), 3 (changed), 4 (changed), 5 (changed), 10 (extended), 11 (current), 13 (current), 15 (current).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "A group belongs to the whole installation, not to one namespace."
- Quote: "A grant names one holder, one role and one object."
- Quote: "A person, a service token owner and a workload can each be a member."
- Quote: "A principal can do an action on an object when a grant on that object, or on an object that holds it, gives a role with that action."
- Quote: "The export covers every grant."
- Quote: "It also holds the last sign-in or the last access token use of the principal."
- Quote: "When the principal never signed in and never used an access token, the field shows `Never`."
- Quote: "A history row also holds the time that the grant ended, if it ended."
- Quote: "Its own action. The export needs its own action, for example `namespace:read-access`."
- Quote: "Each operation on groups, memberships, roles and grants has a REST API and its own action (ADR 17)"
- Effect: The change implements the stored grant check, the collection scope rule, the group membership rule, and the snapshot and history access-review export with its own action and audit entry. It follows decisions 1, 3 and 4 as changed by ADR 67 and decisions 1 and 2 as changed by ADR 215 per the Changed-by chain to the current rule; it touches only the current text quoted.
- Conflict: none.

**ADR 67: Who creates and administers a service principal** (not owner-tagged).

- Decisions that apply: 7 (changed), 8 (current).
- Changed by: ADR 70 changes decisions 2, 3, 4 and 5; ADR 70 extends decision 6; ADR 210 changes; ADR 215 changes decision 7. Chain to the end: ADR 70, ADR 210, ADR 215, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 247, ADR 286, ADR 248, ADR 149, ADR 297, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 70, 210, 215.
- Quote: "A principal of a namespace belongs to one namespace."
- Quote: "A grant to a principal of a namespace can only name that namespace, or an object inside it."
- Effect: The change covers grants held by principals of a namespace in the export, lists holders of manage-tokens on each service principal, and refuses a grant to such a principal outside its namespace.
- Conflict: none.

**ADR 215: The namespace is the tenant, and OpenDustpan Cloud is multitenant (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 9 (current), 10 (current), 13 (current).
- Changed by: ADR 248 changes decision 21. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248.
- Quote: "Each principal and each group has a home (owner)."
- Quote: "The home of a principal or a group is the installation or one namespace."
- Quote: "The namespace owns deprovisioning (owner)."
- Effect: The change applies the principal home that the installation setting identity.principalHome sets, keeps two principals for one person in two namespaces, and lets the identity provider of the namespace end memberships.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 7 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "The installation setting `access.installationGrantsReachNamespaces` is `true` by default."
- Quote: "A setting decides whether an installation grant reaches a namespace."
- Effect: The change applies the default true of the installation-grant reach setting and maps the installation export action to the installation area.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "A grant of an act-as right needs the reach of its target."
- Quote: "a grant of a role with `principal:manage-tokens` on that principal, to a principal or to a group,"
- Effect: The change refuses a grant above the granter reach and refuses a new group member where the adder lacks reach over the group grant.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): specs/groups-roles-grants/spec.md export requirement omits the last sign-in or last access token use field (showing Never when unused) that ADR 52 decision 11 (current) requires in each row, and the history end time of a grant that ADR 52 decision 11 requires for history rows. Resolution: answered: Export requirements now state the last sign-in or token use field with Never and the history end time.
- R1.2 (adr conflict): ADR 52 decision 10 requires a history export between two caller-given timestamps; the spec has only a snapshot scenario, and tasks 1.4 and 2.2 name history without a spec scenario or requirement behind them. Resolution: answered: History export between two caller-given timestamps is now a requirement with a scenario and a task.
- R1.3 (adr conflict): ADR 52 decision 13 quotes "Its own action. The export needs its own action, for example `namespace:read-access`" and each export writes an audit entry; the spec and tasks never name the action of the export route or its audit entry, so the authorization of the export is unspecified. Resolution: answered: Export authorization and its audit entry are now a requirement with the read-access action.
- R1.4 (owner decision conflict): ADR 216 decision 7 (owner) makes access.installationGrantsReachNamespaces true by default, so a grant on @server reaches every namespace; the spec's collection-scope requirement does not say how an installation grant interacts with the collection scope, so the reach rule is undecided. Resolution: answered: Collection scope now states the installation-grant interaction under the installation setting.
- R1.5 (wrong claim): proposal.md ADR check says 'Conflict: none' and that decisions 3 and 4 are unchanged, yet ADR 52 decisions 1, 3, 4 and 5 are changed by ADR 67 decisions 7 and 8 and ADR 215 decisions 9 and 10 (principal home); the change does not state which principal home applies to the grant check, and the owner-tagged ADR 215 and ADR 216 decisions are not listed. Resolution: answered: ADR check now lists ADR 67, 215, 216 and 70, states principal home, and marks owner-tagged decisions.
- R1.6 (missing scenario): No scenario covers a grant whose holder is a group: membership revocation, a group grant reaching a member, or a membership change taking effect on the next request, although ADR 52 decision 2 allows memberships to start and end at any time. Resolution: answered: Group-holder reach and membership timing are now requirements with scenarios.
- R1.7 (missing scenario): No scenario for a writer or viewer who tries to grant a role on an object outside its reach or a role above its own; ADR 70 decision 3 requires the granter's reach to cover the target principal's reach. Resolution: answered: Granter reach over the target is now a requirement with a scenario.
- R1.8 (failure mode): The revocation requirement allows 401 or 403 with no rule for which, no error code, no missing action and no helping command, though the proposal error table names them; the behavior for a grant revoked mid-run of a long pulumi up or tofu apply, and for a lock held by a revoked principal, is not covered. Resolution: answered: Revocation now separates revoked-grant 403 from unknown-token 401 with codes, and states mid-run and lock behavior.
- R1.9 (missing scenario): No scenario for a viewer reading state of a unit in another collection, or for a caller of the access-review export without its action; the usability section requires the export to show why a section is closed, and no requirement states it. Resolution: answered: Viewer scope across collections and export without the action are now scenarios.
- R1.10 (failure mode): No requirement covers the audit entry written on a refused 403 or on a refused 401 for a revoked token, which design.md layers table and ADR 52 decision 13 require; the spec does not say which entry or its fields. Resolution: answered: Refusal audit entries for 403 and revoked-grant use are now requirements.
- R1.11 (open question): The proposal says Open questions: none, but the change does not decide whether a site administrator's export of a namespace includes grants on namespace principals (ADR 67 decision 7) or how a unit created later inherits a collection grant; no ADR answers either for this change. Resolution: answered: Namespace-principal grants in the export and inheritance by later units are now decided in considered and specs.
- R2.1 (open question): The spec does not say how the principal home setting (identity.principalHome) changes the grant check. ADR 215 decision 10 (owner) lets the installation set namespace homes, but no requirement or scenario covers namespace-home principals or groups. Resolution: answered: Principal home is now a requirement with namespace-home scenarios under ADR 215 decisions 9 and 10.
- R2.2 (open question): The spec does not state the default or the false value of access.installationGrantsReachNamespaces. ADR 216 decision 7 (owner) sets it to true by default. The Installation grant reach requirement only applies where grants reach namespaces, so the false case is undefined. Resolution: answered: Installation-grant reach now states the default true and the false case with scenarios.
- R2.3 (failure mode): The spec does not require the grant check and the write to run in one transaction. Design layer port adapter requires a live read in the store transaction, but a revoke between the check and the write is not covered by any scenario. Resolution: answered: Grant check and write atomicity is now a requirement with a revoke-between-check-and-write scenario.
- R2.4 (open question): The Lock held by revoked principal scenario says the server keeps the lock. It does not say who releases the lock, when it expires, or what happens to a half-applied unit. No ADR answers this. Resolution: open question: Lock release after revocation is now an open question for the PM; the spec keeps the lock and refuses new writes until expiry.
- R2.5 (missing scenario): ADR 70 decision 3 says a new member of a group that holds a grant needs a granter whose reach covers that grant. The Granter reach requirement covers only a direct grant, and no scenario covers a new group member. Resolution: answered: New group member above adder reach is now a scenario of the granter-reach requirement.
- R2.6 (missing scenario): ADR 215 decision 13 (owner) says the identity provider can end memberships. The Membership ends scenario covers only a change by the namespace administrator, not an end made by a directory sync. Resolution: answered: Directory-sync end of membership is now a scenario of the membership-timing requirement.
- R2.7 (open question): ADR 52 decision 13 names namespace:read-access for the export. The spec does not say which action guards an export of the installation scope, nor how ADR 216 decision 3 maps it. Resolution: answered: Installation-scope export action is now a requirement mapped under ADR 216 decision 3.
- R2.8 (missing scenario): The Refusal audit requirement covers only CLI writes. The design HTTP layer requires a refusal audit entry for every 403, including reads and export refusals, and the spec does not cover them. Resolution: answered: Refusal audit now covers refused reads and refused exports as well as refused writes.
- R2.9 (missing scenario): The proposal error table names codes collection.forbidden, unit.write-forbidden, grant.revoked and access.export-forbidden. The spec names only grant.revoked and no other code, so the 403 answers are not defined. Resolution: answered: Error codes are now a requirement that defines collection.forbidden, unit.write-forbidden, grant.revoked and access.export-forbidden.
- R2.10 (missing scenario): The export scenarios do not cover an end time before the start time, or a holder renamed after its grant. The project rule requires the stable ID for a reference that can be renamed, and the export shows names only. Resolution: answered: Export now requires stable IDs beside names and refuses an end time before the start time with 400.
- R2.11 (scope): Task 3.1 cites e2e step 4 of milestone 4. The roadmap item and the proposal do not name this milestone or step, so the acceptance check is not traceable to the item. Resolution: answered: Definition-of-done task now names this roadmap item and its step text for traceability.
- R3.1 (adr conflict): specs/groups-roles-grants/spec.md 'Principal home' requirement: ADR 52 decision 1 (not owner-tagged) says principals belong to the installation, but ADR 215 decision 10 (owner) makes identity.principalHome a setting with namespace homes; the spec does not state which home the grant check uses when the setting is namespace, leaving the grant check undefined for namespace-home principals. Resolution: answered: Principal-home requirement now states the identity.principalHome setting decides the home and the grant check uses that home for each principal.
- R3.2 (owner decision conflict): proposal.md ADR check states 'Conflict: none' for ADR 52, yet ADR 216 decision 7 (owner) sets access.installationGrantsReachNamespaces true by default and the spec's 'Installation grant reach' requirement gives 403 when false; the default-true reach of an installation grant over collection units is not reflected in the collection-scope scenarios. Resolution: answered: Collection scope now has an installation-grant scenario under the default true reach, and the reach requirement keeps the false case.
- R3.3 (failure mode): specs/groups-roles-grants/spec.md 'Atomic grant check': the scenario rolls back on a grant ending before commit, but does not define behavior for a token or grant that ends after commit while a long pulumi up or tofu apply is running; the lock and half-applied state after revocation depend on this and are only partly covered by the lock scenario. Resolution: answered: Atomic grant check now has a post-commit scenario: the running update completes its committed write and each later request is refused.
- R3.4 (missing scenario): specs/groups-roles-grants/spec.md 'Access-review snapshot export': no scenario covers a snapshot of a namespace holding grants on principals of that namespace while the caller's own grant reaches only a collection; ADR 67 decision 6 requires the export to list holders of manage-tokens on service principals, which is not stated. Resolution: answered: Snapshot export now covers namespace-principal grants, manage-tokens holders on service principals, and a collection-scoped caller.
- R3.5 (wrong claim): proposal.md ADR check says 'Each quote is copied from the file' and lists ADR 52 decision 11 quote 'It also holds the last sign-in...' as applying, but the 'Changed by' chain for ADR 52 lists ADR 215 changing decisions 1 and 2 and ADR 67 changing decisions 1, 3 and 4; the proposal does not state these changed decisions in the Conflict line, so the claim of no conflict is unsupported. Resolution: answered: ADR check effect now states the changed decisions of ADR 52 followed through ADR 67 and ADR 215 to the current rule.
- R3.6 (open question): proposal.md 'Open questions' lists the lock release after revocation as a provisional decision; the provisional answer keeps the lock until expiry, but no ADR or owner decision sets the lock expiry duration, so the expiry behavior is undefined. Resolution: open question: Open question now states the lock expiry duration is the unit-lock TTL outside this change and keeps the lock until that TTL.
- R3.7 (missing scenario): specs/groups-roles-grants/spec.md 'Group holder reach': no scenario covers a group grant whose member is in a different namespace home under the namespace principal home setting, although ADR 215 decision 12 says a person in two namespaces has two principals. Resolution: answered: Group reach now has a cross-namespace-home scenario with two principals for one person.
- R3.8 (scope): tasks.md 3.1 cites e2e milestone 4 step, which the roadmap item in the input does not name; the acceptance check is not traceable to the roadmap item as the proposal claims. Resolution: answered: Definition-of-done wiring task now cites the roadmap step text of this item for traceability.
- R3.9 (missing scenario): specs/groups-roles-grants/spec.md 'Revocation timing': no scenario covers a token used after its grant is restored or re-granted within the same second, nor a clock skew between the fenced transaction and the grant end time. Resolution: answered: Revocation timing now has a re-grant scenario decided by fenced transaction time, with no clock-skew allow.
- M.9 (gap): No change covers the exit line 'milestones 1 to 3 pass with an access token of a non-admin principal'. 'Granter reach' overlaps the cover check of check-reach-when-grant-membership. 'Refusal codes' must match the codes of planner-role. Resolution: answered: Added non-admin baseline e2e requirement, shared reach function with check-reach-when-grant-membership in depends_on and design, and single refusal code list with unit.forbidden and secret.forbidden shared with planner-role.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Who releases a unit lock held by a principal whose grant ends mid-run, when does it expire, and what happens to a half-applied update?
   - Options: Keep the lock until it expires and refuse each new write of that principal; Release the lock at once when the grant ends.
   - Provisional decision (owner review 14 Nov): Keep the lock until it expires and refuse each new write of that principal
   - Reason: No ADR names lock release after revocation and no ADR sets the lock expiry duration here; the unit-lock TTL is outside this change, so the safest rule keeps the lock and refuses new writes until that TTL. Benchmark: AWS IAM Access Analyzer: an ended grant keeps existing locks until they expire and blocks new writes at once
   - Persona impact: A developer with a revoked grant sees no surprise: the held lock expires on its own unit-lock TTL outside this change and no half-applied update stays locked without a named release path. Escape hatch: The operator releases the kept lock by hand through the unlock route and reruns the unit run after a new grant.

Questions considered and answered:

- Does writer on one collection reach units of another collection? No. ADR 52 decision 5 answers it: reach flows only from a grant on the object or on an object that holds it, so writer on collection A gives no action on a unit of collection B unless an installation grant reaches it under ADR 216 decision 7.
- Does viewer read state but refuse up and apply? Yes for reads, no for writes. ADR 52 decisions 5 and 6 answer it: viewer holds the read actions and lacks the write actions, so stack export and state pull pass and up and apply give 403.
- What does the access-review export hold and who may call it? Snapshot at the time of the request and history between two caller-given timestamps. ADR 52 decisions 10 and 11 answer it: every grant in the scope, each row with holder, role, object, creator, time, last sign-in or token use with Never, and history end time.
- Which action guards the export and does it write audit entries? Its own action with an audit entry. ADR 52 decisions 13 and 15 answer it: the export route declares its read-access action and each export writes an audit entry.
- Which principal home applies to the grant check? Installation home by default. ADR 67 decision 7 with ADR 215 decisions 9 and 10 answers it: principals and groups live in the installation or in one namespace per the principal-home setting, and a grant to a principal of a namespace names only that namespace or an object inside it.
- Does a namespace export include grants on namespace principals and do later units inherit a collection grant? Yes. ADR 52 decision 10 answers it with the export covers every grant, so the export of a namespace includes grants held by principals of that namespace and grants on objects inside it. A collection grant reaches a unit made later in that collection by ADR 52 decision 5 hierarchy, with no extra grant.
- How is the overlap with check-reach-when-grant-membership avoided? One function. The grant check and the act-as cover check use the same unexported reach function; check-reach-when-grant-membership owns the act-as edge cases and this change owns the collection scope, the viewer split, revocation timing and the export.
- Which refusal codes apply so planner-role and this change agree? One list shared with planner-role: unit.forbidden for a refused unit action, secret.forbidden for a refused secret read, plus grant.revoked for use after revocation and access.export-forbidden for the export without its action.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
