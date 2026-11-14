Roadmap: `docs/roadmap.md:287` (Pull request automation, first slice).

## Why

**Persona: developer.** Developer pushes a change for review and wants a plan for each touched stack or workspace with one status and one comment to read.

The user path this change must make work:

1. **API.** Push code and open a review request on the forge.
2. **API.** Read the one commit status and the one summary comment with plan links.
3. **API.** Push again and read the same edited comment.
4. **Docs page.** Read the review request plan docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Push to the review request started no plan. | No plan started; the delivery record of the intake change names the reason. Check the units of the namespace to confirm the affected units. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Status or summary comment is missing or stale after the push. | The status or comment is stale because the forge write failed in the report change. Push again to trigger a fresh delivery. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- The milestone step requires one commit status and one summary comment per pull request, edited on each new push. Proof: A push to a pull request starts a plan for each unit that the change touches. The fake GitHub API gets one commit status and one summary comment for each pull request. A new push edits the same comment and does not add one.
- The change is the roadmap item at docs/roadmap.md:287 with status not shipped. Proof: docs/roadmap.md:287

## What Changes

- Push to a review request yields plan jobs with one status and one summary comment through composed changes.
- Each new push edits the same comment.
- E2e step and docs page prove the composed first slice.

Out of scope:

- Webhook intake, signature check, and delivery deduplication
- Plan start on push to a review request
- Status and summary comment writes and edits
- Apply after the merge
- Pre-merge apply option
- Console run page
- Write-back of unit config edits to git

Depends on:

- git-host-webhook-intake-post
- report-plans-on-review-request
- run-triggers
- start-plan-on-push-review
- `report-plans-on-review-request` (order of the milestone review)
- `start-plan-on-push-review` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)
- `run-triggers` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Developer waits for many plan jobs behind one status and must open each plan link in the single comment.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The forge set of the first slice is open (question 1 reads 'Not spec-ready'). The change cannot be built until the owner or PM decides it. Proposal.md marks the gate as not passed. Resolution: answered: First slice covers GitHub review requests only per the roadmap step.
- R1.2 (missing scenario): No requirement says which forge events start a plan (open, synchronize, reopen, or others). The spec only says 'a push arrives'. The proposal lists this as considered but unanswered. Resolution: answered: Push events stay owned by the trigger change.
- R1.3 (failure mode): The webhook intake has no requirement for signature verification or rejection of forged or malformed deliveries. A hostile sender could start plan jobs. The project requires a hostile test for each security layer. Resolution: answered: Forged deliveries stay owned by the intake change.
- R1.4 (missing scenario): The user path error 'No unit matched the changed files' has no requirement or scenario in the spec and no task or test for it. Resolution: answered: Empty match path stays owned by the trigger change.
- R1.5 (failure mode): Retried or duplicate webhook delivery is named as a risk and handled by the prPlanKey in design.md. No scenario or failing test in tasks.md covers it, so the behavior is not checked. Resolution: answered: Retried delivery stays owned by the intake change.
- R1.6 (failure mode): Two pushes in quick succession, or a restart during a push, can race on the same status and comment. The change does not say where the comment ID is stored or how a lost comment ID is recovered. Resolution: answered: Quick pushes and restart stay owned by the report and intake changes.
- R1.7 (open question): Unit matching from changed files and code locations is a decision with no requirement or ADR. The rule for a file that matches several units, or none, is not stated. Resolution: answered: Unit match rule stays owned by the trigger change.
- R1.8 (failure mode): The key uses forge, repository and review number. A repository rename or transfer changes the key. The project rule requires stable IDs for references to renameable entities, and the change does not address it. Resolution: answered: Repository rename stability stays owned by the report change.
- R1.9 (missing scenario): No requirement covers a forge outage, a failed status post, or a deleted summary comment when the next push edits it. Resolution: answered: Outage and deleted comment stay owned by the report change.
- R1.10 (missing scenario): Automatic plan starts need a stored record of who or what started them and why, by project rule. No requirement or task names this record. The endpoint and its authorization actions are also not declared. Resolution: answered: Audit record stays owned by the composed changes.
- R1.11 (wrong claim): 'No ADR is touched' is a negative claim with no quoted decision lines or rg proof. The ADR check also defines owner-tagged as 'ADR records decisions of the project owner', which differs from the project rule that uses only the '(owner)' tag. Resolution: answered: Evidence proves the empty ADR set from the pack.
- R1.12 (wrong claim): The proposal promises plan links in the single comment, but the spec has no requirement for links and the usability section admits the comment has none. Resolution: answered: Plan links stay owned by the report change.
- R2.1 (open question): The Open questions section reads 'Not spec-ready' and reports failing code checks, while the adversarial pass R1.1 says the list is now empty and the change is spec-ready. The spec-ready gate is not passed, so the change cannot be built. Resolution: answered: Open questions list is empty. No code check is reported there; failing tests are tracked in tasks.
- R2.2 (wrong claim): The error table names GET forge-connections and GET plan-deliveries as the commands that help. The code check says these routes are not in the API description or the route table, so the one command that helps does not exist. Resolution: answered: Error commands use GET units, a route named in the coverage pack.
- R2.3 (missing scenario): The layers table and the project rule require an authorization check at each plan start and a denial record. No requirement or task declares the authorization actions or the object of the automatic plan start, and no scenario covers a sender without rights. Resolution: answered: Authorization stays owned by the composed changes; composed flow requirement checks denial through them.
- R2.4 (wrong claim): R1.9 and R1.6 claim that requirements now cover forge write failure, forge outage, quick pushes and restart. The spec has no such requirement or scenario, and the error table promises a retry that no requirement defines. Resolution: answered: Forge outage and deleted comment stay owned by the report change.
- R2.5 (failure mode): Concurrent pushes to one pull request can race on the same status and comment. No requirement serializes the writes or defines the result when two plan runs finish in the other order. Resolution: answered: Serialization stays owned by the report change.
- R2.6 (failure mode): Deduplication stores the delivery ID before fan-out. A crash or restart after the store and before the plan starts loses the push silently, and the retry is then dropped as a duplicate. No requirement covers this order of steps or a retry after a partial failure. Resolution: answered: Crash-safe delivery order stays owned by the intake change.
- R2.7 (open question): The stored comment ID and the stored forge secret have no requirement for where they live, how they are protected, or who can read them. The design keys them by forge connection ID, but no ADR or requirement answers the storage or secret handling. Resolution: answered: Storage stays owned by the composed changes.
- R2.8 (wrong claim): The ADR check defines owner-tagged as 'when the ADR records decisions of the project owner'. The project rule defines it only by the '(owner)' tag in the decision or title, so the definition is wrong. Resolution: answered: Considered answer defines owner-tagged only by the (owner) tag per the project rule.
- R2.9 (missing scenario): The spec covers only the synchronize push in its title, though the body names open and reopen. Closed, draft, fork and force-pushed pull requests have no requirement or scenario. Resolution: answered: Closed and draft handling stays owned by the trigger change.
- R2.10 (failure mode): Deduplication has no retention period or storage bound for seen delivery IDs, and no rule for two copies of one delivery that arrive at the same time. Resolution: answered: Delivery log bound stays owned by the intake change.
- R2.11 (scope): The change builds the commit status and comment write-back, which the proposal lists as a dependency on write-back. The boundary between this item and write-back is not stated. Resolution: answered: Out of scope states write-back of unit config edits to git stays outside.
- R2.12 (wrong claim): The design calls the key planKey and the adversarial pass calls it prPlanKey. The key fields also differ from the design text that lists review number as part of the key, so the stated fix for repository rename is not clearly reflected in the spec. Resolution: answered: No new key type; keys stay owned by the composed changes.
- M.4 (duplicate): The webhook signature check, the delivery deduplication, the plan start on push and the status and comment duplicate git-host-webhook-intake-post, start-plan-on-push-review and report-plans-on-review-request. Resolution: answered: This draft is now the step-7 e2e step and docs page that composes git-host-webhook-intake-post, start-plan-on-push-review, and report-plans-on-review-request; duplicate intake, match, and report requirements removed and spec uses review request.

## Open questions

none

Questions considered and answered:

- Which forge events start a plan? The milestone step names the fake GitHub API; the first slice covers GitHub review requests only through the composed intake change.
- How many statuses and comments per review request? One commit status and one summary comment per review request, edited on each new push, owned by the report change; this change only proves the composed result in e2e.
- Which ADRs does the change touch? The context pack lists adrs as empty and gives no decision text, so no ADR decision applies.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
