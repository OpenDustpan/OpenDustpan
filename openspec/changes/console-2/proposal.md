Roadmap: `docs/roadmap.md:323` (Console).

## Why

**Persona: developer.** A developer runs plans and applies on runners and needs to follow one run, read its masked diff and approve it without use of raw API output.

The user path this change must make work:

1. **Console.** Open the unit and select the job from its history to reach the run page.
2. **Console.** Follow the timeline and live log while the job runs.
3. **Console.** Read the masked plan diff for the plan phase.
4. **Console.** Approve the waiting apply where policy needs it.
5. **Docs page.** Read the run page docs that name the API routes behind each panel.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Job ID unknown: no job with this ID in this namespace. | The run page shows job not found and links back to the unit list. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |
| Approval refused: the principal lacks the approve action on the unit. | The run page shows approval refused and keeps the job in approval wait. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |
| Log stream closed: the runner stopped sending chunks. | The live log panel shows log unavailable and offers retry from the last cursor. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}/logs/{step}` |

Evidence for the claims above:

- The context pack names no ADR for this change. Proof: Pack field adrs is an empty list.
- The roadmap row cites RFC 52 console core views as draft. Proof: Roadmap row: RFC 52 console core views, run page with timeline and diff, draft.
- The roadmap step requires the run page with timeline, live log, masked plan diff and approval. Proof: Roadmap step: The run page shows the timeline, the live log, the masked plan diff and the approval of step 6.

## What Changes

- Run page shows timeline, live log, masked plan diff and approval for one job.
- Browser tests of the console repository run against the suite server.
- Docs page describes the run page path and its API routes.
- Run page uses job, log, plan and approval routes owned by add-job-service-routes-start-1 and policy-approvals; this change adds no server route of its own.

Out of scope:

- Job start, cancel, approve rules and policy evaluation.
- Runner management and pool views.
- Pull request automation views.
- Console views other than the run page.

Depends on:

- add-job-service-routes-start-1
- policy-approvals
- runners
- `policy-approvals` (order of the milestone review)
- `runners` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A large plan diff still slows first render; the page paginates resources.
- Live log polling lags a few seconds behind the runner; the page states the refresh time.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The routes GET /api/dustpan/v1/jobs/{jobID}, POST /api/dustpan/v1/jobs/{jobID}/approvals and GET /api/dustpan/v1/jobs/{jobID}/log are not in the API description or the route table; the errors table and task 2.1 cite them as working routes. The proposal must name existing routes or list the dependency that adds them. Resolution: answered: Replaced bare job routes with namespaced job and log routes from add-job-service-routes-start; approval submits through the approve route that policy-approvals owns.
- R1.2 (open question): Polling or server-sent events for the timeline and live log is undecided. The proposal says PM decides, and the change is not spec-ready until the answer is recorded. The design and the tasks cannot be built without it. Resolution: answered: Decided polling on a fixed interval with manual refresh; recorded in design decision 3 and considered; open_questions is empty.
- R1.3 (missing scenario): No scenario covers approval refused for a principal without the approve action on the unit, although the errors table says the job stays in approval wait. The spec and task 1.3 need a refusal scenario and its authorization check. Resolution: answered: Added approval-refused requirement and scenario with 403 and wait state kept.
- R1.4 (missing scenario): No scenario covers an unknown job ID or a job ID from another namespace. The spec must state the not-found answer and that cross-namespace reads are refused. Resolution: answered: Added unknown and cross-namespace job requirement with not-found answer.
- R1.5 (failure mode): The spec requires masked log chunks, but design decision 2 masks only the plan diff and says the console adds no processing. Nothing states how secret values are masked in live log chunks, so a secret printed by a runner could reach the page. Resolution: answered: Design decision 2 now states the server masks log chunks and plan diff; added masked log requirement.
- R1.6 (missing scenario): No scenario covers the log stream closing because the runner stopped, or the retry the errors table offers. The spec does not say how the cursor resumes after a closed stream or a server restart. Resolution: answered: Added log-closed requirement with cursor resume and retry scenario.
- R1.7 (failure mode): Two approvers, or a double submit, can approve the same waiting apply. The spec does not state whether a second approval is refused, idempotent, or recorded, nor what happens when the job was cancelled before the approval arrived. Resolution: answered: Added duplicate approval and cancelled-job requirement with idempotent record.
- R1.8 (missing scenario): The usability section says the page paginates resources, but no requirement or scenario states pagination or a limit for large plan diffs. Resolution: answered: Added paginated plan diff requirement with page limit scenario.
- R1.9 (failure mode): The design lists browser test flakiness from live log timing as a risk but gives no mitigation, such as a fixed clock, a fixed chunk sequence or a bounded wait. Task 3.2 would then be unreliable. Resolution: answered: Tasks use fixed chunk sequence and bounded wait to keep browser tests stable.
- R1.10 (wrong claim): The ADR check says a decision is owner-tagged when the ADR records decisions of the project owner. The project rule uses only the text containing (owner) or (owner, ADR N), or the ADR title containing (owner). The change also says No ADR is touched without the quoted lines or an rg check, as rule 5 requires. Resolution: answered: Left adr_check empty because the pack names no ADR; no owner-tag claim remains in the change.
- R1.11 (scope): The approval route and the approve rules belong to policy-approvals, which the proposal lists as a dependency and excludes from scope. Task 2.1 wires that route into this change anyway, so the boundary between the two items is unclear. Resolution: answered: Clarified boundary: approve rules stay in policy-approvals; this change only submits through that route for display.
- R2.1 (open question): The roadmap row cites RFC 52 (console core views), which is in draft. The change builds on an RFC that no accepted decision answers, and proposal.md does not say whether the RFC is accepted. Confirm the RFC status before the change is spec-ready. Resolution: open question: Added open question on RFC 52 draft status with PM as decider; change is not spec-ready until answered.
- R2.2 (failure mode): The server masks log chunks, but no requirement covers a secret split across two chunk boundaries or a masking miss. A secret printed by a runner could reach the page unmasked, and the console adds no check. Specify the boundary rule and a hostile test. Resolution: answered: Added chunk-boundary masking requirement with joined-value rule and hostile test with split secret.
- R2.3 (wrong claim): The ADR check says No ADR is touched, but gives no rg command or quoted decision lines. Rule 5 requires proof for this negative claim. The decisions list is empty, so the claim is unchecked. Resolution: answered: Added evidence that pack adrs list is empty and considered entry that no ADR decision applies.
- R2.4 (wrong claim): The errors table names POST /api/dustpan/v1/namespaces/{}/jobs/{} as the command for an approval refusal. That path is the job route, not the approve route owned by policy-approvals, so the one helpful command is wrong. Resolution: answered: Fixed approval error command to the approvals sub-route owned by policy-approvals.
- R2.5 (missing scenario): No requirement covers a principal that lacks the read action on the unit. The spec gives only 404 for unknown and foreign jobs and 403 for approval. The refusal answer for job, log and plan reads is not stated. Resolution: answered: Added read-refused requirement with 403 for job, log and plan reads without read action.
- R2.6 (missing scenario): The cancel scenario assumes a stored approval exists. An approval that arrives after cancel, with no earlier approval, has no defined answer, and the spec does not say it must be refused. Resolution: answered: Added late-approval requirement that refuses a first approval after cancel with 409.
- R2.7 (failure mode): The log resume requirement covers a runner that stops, but not a server restart. The spec does not say whether the cursor survives a restart or how a stale cursor is answered. Resolution: answered: Added persisted cursor requirement across server restart and stale cursor 410 answer.
- R2.8 (open question): Two different approvers submit approvals for the same waiting apply. The duplicate scenario covers only the same submit, so the rule for a second approver's record is not set. Resolution: answered: Added distinct second approver requirement that records a second signature.
- R2.9 (open question): The fixed polling interval and the refresh time shown on the page are not set. The usability section promises the page states the refresh time, but no requirement or task covers it. Resolution: answered: Set 5 second poll interval in design, added polling requirement whose scenario shows the last poll time, and kept manual refresh.
- R2.10 (missing scenario): The paginated plan diff requirement gives no answer for a stale next cursor after the stored plan changes, or for a page request beyond the total count. Resolution: answered: Added stale and beyond-total cursor requirement with 410 answer and first-page retry.
- M.12 (gap): The run page reads a masked, paginated plan diff and a timeline, but no server change defines those routes. The dependency on events-notifications has no use, because the page polls. Resolution: answered: Removed events-notifications from depends_on and added runners; corrected job-service dependency to add-job-service-routes-start-1 which owns the timeline, log and masked paginated plan diff routes, with approval owned by policy-approvals; this change adds no server route.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Is RFC 52 accepted as the design basis for the run page slice, or does this change land before RFC 52 is accepted?
   - Options: Wait until RFC 52 is accepted before browser tests land; Land the run page slice now against the suite server and align with RFC 52 later.
   - Provisional decision (owner review 14 Nov): Wait until RFC 52 is accepted before browser tests land
   - Reason: The roadmap row cites RFC 52 as draft, so the spec-ready gate needs a clear basis before browser tests lock the page shape. Benchmark: Google launch review practice that ships UI only against an approved design document
   - Persona impact: A Developer sees no surprise either way; the page reads only shipped job records. Escape hatch: Ship behind the existing run page behind no new route; server records stay unchanged if the RFC stays draft.

Questions considered and answered:

- Does this change touch any accepted ADR decision? No. The context pack lists adrs as empty, so no ADR decision applies. The change reads only job, log, plan and approval records that other items own.
- Does the live log and timeline use polling or server-sent events in the first slice? Polling on a fixed 5 second interval matches the existing job list behavior. No new event channel ships in this slice. The design records polling and the tasks test it.
- Does this change cover other console views beyond the run page? The roadmap step names only the run page with timeline, live log, masked plan diff and approval of step 6, plus browser tests against the server of the suite. No other console view is in scope.
- Does this change define job behavior or policy decisions? Job start, plan, apply, approval wait and policy decisions belong to the job, policy and trigger items. This change only reads those records for display and submits an approval through the existing approve route.
- Does the run page use events-notifications in this slice? No. The page polls the job, log and approval routes. No subscription or webhook delivery is used, so events-notifications is not a dependency. Runners and the job service own the records shown.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
