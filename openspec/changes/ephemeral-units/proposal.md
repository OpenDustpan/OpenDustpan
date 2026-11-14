Roadmap: `docs/roadmap.md:367` (Ephemeral units).

## Why

**Persona: developer.** A Developer makes a short lived stack or workspace from a blueprint for a test. The Developer wants the server to warn before the end and remove the stack or workspace after the end, without manual cleanup.

The user path this change must make work:

1. **API.** Request a stack or workspace from a blueprint with a time to live through the API.
2. **API.** Read the expiry and the warning in the unit read and in the webhook sink.
3. **API.** Approve or cancel the waiting destroy through the review request where an approval rule applies.
4. **Docs page.** Follow the create and extend commands on the docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Create without a time to live fails with 422 time-to-live required. | The server refuses the request and names the field. Run the create call again with a time to live inside the maximum. | `POST /api/dustpan/v1/namespaces/{namespace}/units` |
| Extend past the maximum fails with 422 time-to-live past maximum. | The server refuses the extension and names the maximum. Run the extend call again with a smaller value. | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend` |
| Destroy with an unmet approval rule waits with 409 approval wait. | The server holds the destroy in approval wait and names the review request. Approve the review request, then check the job status. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}` |

Evidence for the claims above:

- A guide for blueprints exists in the docs. Proof: docs/guides/publish-a-blueprint.md
- Runner e2e tests exist for jobs and claims. Proof: e2e/jobs_api_test.go
- Approval rule e2e tests exist. Proof: e2e/approval_rules_test.go
- No e2e test in the pack names ephemeral units. Proof: Blueprint e2e_count 0, Runner e2e list holds no ephemeral file, Approval rule e2e list holds no ephemeral file
- The pack names no ADR for this item. Proof: adrs []
- Coverage matrix touches unit list routes with weak refusal cells. Proof: GET /api/dustpan/v1/namespaces/{namespace}/units op listUnits

## What Changes

- Ephemeral stacks and workspaces from blueprints carry a time to live and an expiry time inside 30 days plus one 7 day extension.
- One warning with idempotency key goes to the webhook sink 24 hours before expiry, once per unit with retry after outage.
- A destroy job runs on a runner at expiry in the same transaction as the expiry state change, with approval wait on the RFC 114 review request and no self approval where an approval rule applies.
- The time to live extends inside the maximum, or expiry is removed before destroy create.
- New POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend extends the time to live.

Out of scope:

- environment-promotion
- artifact-registry
- curated-upstreams
- upgrade-pull-requests
- job-proxy
- console-3

Depends on:

- blueprints
- review-requests
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The Developer learns the expiry only at create time when the unit list hides it.
- The warning in the webhook sink names no next step to extend or approve.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The maximum time to live and who extends it are open (Decides: PM). The spec sets no limit, so the 422 time-to-live past maximum scenario cannot be built or tested. Resolution: open question: Kept as open question 1 on maximum time to live. Spec uses 30 days plus one 7 day extension and refuses past-maximum with 422 until the PM decides.
- R1.2 (open question): Whether the destroy at expiry waits for approval, and whether it can self-approve or time out, is open (Decides: PM). The approval wait scenario cannot be built without this answer. Resolution: open question: Kept as open question 2 on approval wait. Spec holds destroy without self approval until the PM decides.
- R1.3 (open question): The warning timing is open (Decides: PM). The spec says nears its expiry time with no window, so the warning requirement has no testable trigger. Resolution: open question: Kept as open question 3 on warning timing. Spec uses 24 hours before expiry with a testable trigger until the PM decides.
- R1.4 (wrong claim): The error table gives POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job}/approvals as the approve command. The change does not show this route in the route table or the API description, and the gate reports it as a failed code check. Resolution: answered: what_changes, errors, tasks, and specs now name POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend as the one new route of this change with declared authorization actions. The jobs approvals route is removed; approval uses the RFC 114 review request.
- R1.5 (open question): RFC 43 is a draft, and the roadmap says the design is still needed. The change builds the destroy and approval behavior on it without an accepted RFC or ADR, and the ADR check states No ADR is touched without proof. Resolution: answered: Design context states RFC 43 is a draft and this step proves the behavior the design gives. Evidence proves adrs [] from the pack, so adr_check stays empty.
- R1.6 (missing scenario): No scenario covers the 409 approval wait response for a destroy that is held. The error table names it, but the spec only says the server holds the destroy. Resolution: answered: Destroy at expiry requirement now has a 409 approval wait scenario.
- R1.7 (missing scenario): No scenario covers a destroy job that no runner claims, or that fails. The design lists this risk, but the spec says the unit leaves the list only after completion, with no state for a unit that stays past expiry. Resolution: answered: Destroy at expiry requirement now covers unclaimed or failed destroy, failed destroy retry, and visible expiry-expired state past expiry.
- R1.8 (failure mode): The warning is not durable. A restart, a sink outage or a missed tick can skip the single warning, and the design's warned flag has no requirement for retries or for a warning sent once per unit. Resolution: answered: Warning requirement now sends once per unit with idempotency key and retries until sink accept after restart or outage.
- R1.9 (missing scenario): No scenario covers an extend call on a unit that is already past expiry, in approval wait, or being destroyed. A race between extend and the destroy at expiry is not handled. Resolution: answered: Extend requirement now covers extend during approval wait, extend past expiry with destroy created refused with 409, and remove expiry before destroy create.
- R1.10 (missing scenario): The spec does not list the authorization actions and objects for the create, extend and approve endpoints, as the project rules require for every endpoint. Resolution: answered: Added authorization requirement that declares create on collection, manage on unit, and approve on unit for the new endpoints.
- R1.11 (missing scenario): The design requires a constructor that rejects missing or past expiry times and normalizes to Unix milliseconds. The spec has no scenario for a past expiry or for the normalized value. Resolution: answered: Time to live requirement now covers missing, zero, past, past-maximum, and Unix millisecond normalization scenarios.
- R1.12 (scope): The tasks list an e2e step that runs against the real CLI on SQLite and PostgreSQL, but no task names the test file or a mapping from each scenario to a named failing test on main. Resolution: answered: Tasks now map each spec scenario to a named failing test in e2e/ephemeral_units_test.go and the domain suite, with the e2e step on SQLite and PostgreSQL.
- R2.1 (wrong claim): The proposal has a contradiction in the section "Open questions". The second pass says R1.1 to R1.3 are "kept as open question", and a note says the TTL limit "stays as an open question". The section also says "none" and gives provisional answers. The spec-ready gate cannot be read right. Resolution: open question: The section Open questions now lists 3 open PM questions with recommended values. The considered section points to them and no longer says none. The spec uses the recommended values until the PM decides, so the gate reads open, not none.
- R2.2 (scope): The spec has no requirement or scenario for the new approve route POST .../jobs/{job}/approvals. It has no approve success, no refusal for a non-approver, no self-approval refusal, no cancel of a waiting destroy, and no approval after the unit is extended. The user path step 3 is not built. Resolution: answered: Added approve requirement with approve success, non-approver refusal, self-approval refusal, cancel, and extend-then-approve scenarios on the new approve route.
- R2.3 (wrong claim): The spec does not use the provisional decisions. It sets no maximum (30 days, one 7 day extension), no 24 hour warning window, and no rule for the number of extensions. The scenarios "within the maximum" and "nears its expiry time" cannot be tested. The design says the destroy at expiry never self approves, but the spec does not say this. Resolution: answered: Spec now uses provisional values: 30 days plus one 7 day extension, 24 hour warning, never self approve.
- R2.4 (wrong claim): The provisional decision on extension says that a namespace administrator can "remove the expiry", and the persona impact says the Developer's long test is helped by an extension. The spec lets only a namespace administrator extend, so a Developer cannot extend their own unit. The spec has no remove-expiry route and no unit-page action to cancel a waiting destroy. Resolution: answered: Extend uses manage on the unit so a Developer with the grant extends; added remove-expiry and cancel-waiting-destroy scenarios.
- R2.5 (wrong claim): The ADR check says "No ADR is touched" with no proof, and the file list of decisions is empty. Rule 5 needs the proof for this claim. The change touches jobs, approval, webhook sinks, authorization and units, which ADRs may cover. Resolution: answered: Evidence now proves adrs [] from the pack; adr_check stays empty with that proof.
- R2.6 (failure mode): The design says the warning is recorded "in the same store write as the warned flag" and then sent with retry until it succeeds. Marking the warning as sent before the send is a lost warning on a crash, or a double send when the flag is set after the send. The design does not say which order or what idempotency key the sink gets. Resolution: answered: Design now persists warned flag only after sink accept and dedupes retry by idempotency key unit ID plus expiry.
- R2.7 (failure mode): The extend race with the destroy at expiry has no atomic rule. The spec refuses extend only when the unit is "past expiry" or in destroy, but it does not say what happens when the extend and the expiry tick happen at the same time. The spec has no rule on whether the destroy job is created in the same transaction as the state change. Resolution: answered: Design and destroy requirement now create the destroy job in the same transaction as the expiry state change; extend after commit is refused.
- R2.8 (missing scenario): Several behaviors have no scenario: the destroy job of a unit that is already deleted by hand, a destroy that fails and then a retry, the unit state name "expiry-expired" (not defined in the API), a restart in the middle of a destroy, the warning for a unit created with a time to live shorter than the warning window, and a unit past expiry at server start. Resolution: answered: Added scenarios for hand-deleted unit, failed destroy retry, restart mid-destroy, short TTL below warning window, and past-expiry at start.
- R2.9 (missing scenario): The Usability section says that the unit list hides the expiry. The spec has no scenario for the unit list showing the expiry, and the warning payload has no next step. Create with an expiry in the past is refused, but a time to live of zero or a negative one is not covered. Resolution: answered: Added unit-list expiry scenario, warning next-step payload, and zero TTL refusal scenario.
- R2.10 (open question): The spec uses the actions unit:create and unit:manage, and approval by unit:manage. No ADR or code evidence supports these names, and it is not clear that the approver of a destroy has the same action as the one who extends. The design does not say whether the destroy job runs under a system principal with an audit record of who and why. Resolution: answered: Added authorization and audit requirements: declared actions on named objects plus destroy under namespace service principal with audit entry.
- R2.11 (scope): The tasks do not give a mapping of each scenario to a named test, and they run the real OpenTofu and Pulumi step only in 3.1. The hostile tests for the defense layers (a user who calls approve without the grant) are missing. The docs page task has no docs-lint check. Resolution: answered: Tasks now map each scenario to a named test, add hostile approve-without-grant tests, and add docs-lint check.
- M.6 (consistency): The new route POST /jobs/{job}/approvals makes a second approval model. RFC 114 review requests (modes 1 and 3, ADR 299, ADR 328) ship in this milestone, and no change implements them. Resolution: answered: Dropped the POST jobs approvals route. The waiting destroy now waits on the RFC 114 review request. Added depends_on review-requests. ADR 299 and ADR 328 texts are not in the pack (adrs []), so adr_check stays empty with proof and no quote is invented.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the maximum time to live and who extends it?
   - Options: 30 days maximum with one 7 day extension; 90 days maximum with repeated extensions by a namespace administrator.
   - Provisional decision (owner review 14 Nov): 30 days maximum with one 7 day extension.
   - Reason: The roadmap step and RFC 43 draft give no limit. The pack names no ADR for this item. Benchmark: AWS practice for temporary environments: a fixed time to live, one warning before expiry, and an explicit extend call with a maximum.
   - Persona impact: A short maximum upsets a Developer with a long test. An extension path keeps the work going. Escape hatch: A principal with manage on the unit extends the time to live or removes expiry where the API allows it.
2. Does the destroy at expiry wait for approval when an approval rule applies?
   - Options: Destroy waits for approval and never self approves; Destroy proceeds after a timeout when no approver acts.
   - Provisional decision (owner review 14 Nov): Destroy waits for approval and never self approves.
   - Reason: The roadmap step says a unit with an approval rule waits for approval, but it names no approver and no timeout. Benchmark: PCI DSS practice: a destructive action waits for an independent approval when a rule requires it.
   - Persona impact: Waiting upsets a Developer who wants cleanup now. The waiting destroy stays visible with its approver and next step. Escape hatch: A principal with manage on the unit approves or cancels the waiting destroy from the unit read.
3. When does the warning before expiry go to the webhook sink?
   - Options: Warning 24 hours before expiry to the webhook sink; Warning 1 hour before expiry to the webhook sink.
   - Provisional decision (owner review 14 Nov): Warning 24 hours before expiry to the webhook sink.
   - Reason: The roadmap step gives no time and no payload. Benchmark: AWS practice: one warning event before expiry to a configured sink.
   - Persona impact: A late warning upsets a Developer who loses work. A 24 hour warning leaves time to extend. Escape hatch: The unit list shows the expiry time even when the notification fails.

Questions considered and answered:

- What limits the time to live of a unit from a blueprint? No ADR in the pack answers it. It stays as open question 1. The spec uses 30 days from create plus one 7 day extension until the PM decides.
- How does the destroy at expiry run and where does approval wait apply? Pack lists e2e/jobs_api_test.go and e2e/approval_rules_test.go with no ephemeral file. The destroy reuses the job path and waits on an RFC 114 review request. No new approvals route is added by this change.
- When does the warning before expiry go to the webhook sink? The roadmap step gives no time. It stays as open question 3. The spec uses 24 hours before expiry until the PM decides.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
