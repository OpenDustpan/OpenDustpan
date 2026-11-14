Roadmap: `docs/roadmap.md:300` (Write-back).

## Why

**Persona: developer.** A Developer keeps unit config in git and edits one value from the API. The server commits the edit with the trailer of the job, so the team sees the change in git and the next plan shows it.

The user path this change must make work:

1. **API.** Developer edits a config value of a stack or workspace through the tool-neutral API.
2. **Server start.** Server validates the edit, starts a write-back job, and commits the edit on the forge with the trailer of the job.
3. **API.** Developer reads the job, the commit, and the next plan that shows the edit.
4. **Docs page.** Developer follows the docs page for API config edits and write-back.
5. **E2e.** E2e drives the real binary over TLS against the fake forge and checks the commit trailer and the next plan.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| API edit names a file outside the editable config files | Edit refused: path is not an editable config file. List editable files first. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/settings` |
| Base commit is stale at deliver time | Job failed: key changed after the base commit. Read the settings, rebase the edit on the latest commit, and send the edit again. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/settings` |
| Forge call fails during the deliver phase | Write-back failed on the forge. Read the job record for the error class, then send the edit again. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |

Evidence for the claims above:

- An e2e file names unit settings. Proof: e2e/unit_settings_test.go
- A guide names the forge connection step. Proof: docs/guides/connect-github.md
- No e2e file in the pack names Forge. Proof: Forge e2e_count 0
- The Forge port and its conformance suite exist in the tree. Proof: pkg/forge and pkg/forge/forgetest

## What Changes

- API config edit of a unit starts a write-back job that commits only the edited keys at the base commit with the trailer of the job.
- Edits outside the editable config files fail before any forge call.
- A key that changed after the base fails the job with the key named.
- A retryable forge error retries, and a refused forge error fails the job.

Out of scope:

- Runner jobs, plan phases, and stored plans
- Pull request automation and apply after merge
- Profile opens and secret handling in jobs

Depends on:

- git-host-webhook-intake-post
- add-job-service-routes-start-1
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The Developer learns the editable files only after a refusal; the read route lists them.
- The trailer is opaque until the docs page shows one commit with the job trailer.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 101: Two executors: write-back and runners** (not owner-tagged).

- Decisions that apply: 3 (extended), 4 (current), 5 (current), 6 (current).
- Changed by: ADR 110 extends decision 3; ADR 130 extends decisions 12 and 14; ADR 131 extends decisions 2, 13 and 15; ADR 133 changes decision 10; ADR 133 extends; ADR 134 extends decisions 7, 10 and 13; ADR 164 extends decisions 7, 10, 14 and 15; ADR 300 changes decision 1. Chain to the end: ADR 110, ADR 130, ADR 131, ADR 133, ADR 134, ADR 164, ADR 300, ADR 210, ADR 218, ADR 222, ADR 225, ADR 286, ADR 305, ADR 308, ADR 343, ADR 139, ADR 204, ADR 350, ADR 199, ADR 330, ADR 166, ADR 247, ADR 248, ADR 335, ADR 351, ADR 354, ADR 299, ADR 360, ADR 282, ADR 310, ADR 353, ADR 328, ADR 359, ADR 304.
- Current rule: this ADR, with ADR 110, 130, 131, 133, 134, 164, 300.
- Quote: "Write-back runs in the server."
- Quote: "Write-back edits only the config of the unit."
- Quote: "Write-back does `apply` only."
- Quote: "The team's CI closes a write-back job."
- Effect: This change implements the write-back deliver phase in the server, the config-only edit rule, the apply-only rule, and the CI-close rule for the job, with the ADR 110 extension that an edit on a missing file creates it.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The spec leaves open which forge errors retry and which fail; the proposal's open question 1 is undecided, yet the spec requirement 'Forge failure class' is written as if the answer is known. The change cannot be built right without this answer. Resolution: answered: Decided in spec requirement Forge retry class: retryable is transport errors, timeouts, 429 and 5xx; refused is 4xx without retry. Open questions now empty.
- R1.2 (open question): The trailer format is open (proposal open question 2), yet the spec and tasks require 'the trailer of the job' with no defined content. The commit cannot be tested or built without it. Resolution: answered: Decided in spec requirement Commit trailer: one trailer with the job ID. Tasks test the exact trailer.
- R1.3 (scope): The proposal is not spec-ready: task 2.4 names docs/guides/unit-config-edits.md as an existing check path, but the file does not exist in the checkout. The spec-ready gate must refuse the change until it is named as new. Resolution: answered: Task check now reads add the new file docs/guides/unit-config-edits.md.
- R1.4 (missing scenario): The spec has no requirement or scenario for the stale-base refusal path and its user-facing error text. Error table names a 'retry command' that does not exist in the job record, so the user path for a failed forge call is unspecified. Resolution: answered: Stale base scenario and error text now name job failed and the rebase step; forge error entry names the job read route with no invented retry command.
- R1.5 (missing scenario): No scenario covers the HTTP layer refusal: a missing unit write grant must return 403 and write an audit entry, as the layers table requires. The spec omits it, though ADR-level rule says each automatic action writes a record. Resolution: answered: Added requirement Grant refusal with 403 and audit entry scenario.
- R1.6 (failure mode): The design risk 'a commit with a wrong base overwrites a team push' has no scenario or requirement that checks the base commit is still the head of the branch at commit time (race between the read and the write). Resolution: answered: Added requirement Base still head covering the read-to-write race.
- R1.7 (failure mode): The design risk 'a forge secret reaches the job log' has no requirement, scenario or layer test; the spec does not require masking of forge errors in the job log. Resolution: answered: Added requirement Secret masking in the job log with a hostile log scenario.
- R1.8 (failure mode): Restart or crash during the deliver phase is not covered: no scenario says what happens to a job whose commit was made on the forge but not recorded, so a retry may create a duplicate commit. Resolution: answered: Added requirement Single commit on retry covering crash between forge commit and job record.
- R1.9 (adr conflict): ADR 101 decision 3 says a key changed after the base fails the job; the spec 'Stale base fails' requirement does not say whether the job is refused or failed, and the error table says 'Commit refused', which is a different outcome. The wording conflicts with the decision line. Resolution: answered: Stale base text now says fail the job, matching ADR 101 decision 3; error text says job failed.
- R1.10 (open question): The design says the edit path is checked with job ValidateEdits and StepPlanner Editable, and ADR 110 extends decision 3 to create a missing file, yet the spec has no scenario for an edit on a file that does not exist at the base commit. Resolution: answered: Added requirement Missing file creates covering the ADR 110 extension.
- R1.11 (wrong claim): The ADR check says the 'Changed by' chain was followed to the end, but the quoted decisions of ADR 101 are current and the proposal does not state that decision 3 is extended by ADR 110 with the new rule; the claim that there is 'Conflict: none' is not backed by the ADR 110 link. Resolution: answered: ADR check now lists ADR 110 with its extension line and states it in the ADR 101 effect.
- R1.12 (missing scenario): The spec has no scenario for a request to edit a unit whose tool is not OpenTofu or Pulumi, or whose StepPlanner has no editable files at all; the user path has no error for an empty editable set. Resolution: answered: Added requirement No editable files covering a tool with an empty editable set.
- R2.1 (adr conflict): ADR 110 is quoted as a decision line, but its decisions list is empty and the quote 'an edit on a missing file creates it.' is only in the links of the index; the ADR check cites a decision that the input does not support, so the missing-file requirement rests on an unverified quote (design.md ADR check, proposal.md Open questions). Resolution: answered: Removed the ADR 110 entry. ADR 101 carries the check. The missing-file behavior is stated as the extension noted in the ADR 101 chain and covered by the Missing file creates requirement.
- R2.2 (adr conflict): ADR 101 decision 3 says a key changed after the base fails the job, while the spec 'Base still head' and 'Stale base fails' do not state the rule for a file that is created or for a key changed by a team push at the same time; the two refusal paths are not tied to one decision line (specs/write-back/spec.md Stale base fails). Resolution: answered: Stale base fails and Base still head now both tie to ADR 101 decision 3. The base-head rule covers created files and team pushes at commit time.
- R2.3 (failure mode): The 'Forge retry class' requirement says 'If the forge answers with 4xx, then fail without retry' but the scenarios say a retry keeps the job running with no retry limit, backoff or maximum attempt count; an unbounded retry on 5xx and 429 is not specified. Resolution: answered: Split retry into two requirements. The retryable requirement now states a bounded retry with backoff and a maximum attempt count before the job fails.
- R2.4 (missing scenario): No scenario covers a concurrent second edit of the same unit while a write-back job is running (two jobs on one base commit); the base-head check covers only the commit moment, not the second job's base. Resolution: answered: Added Concurrent second edit requirement. A second edit on the same unit while a write-back job runs gets a new base and fails at commit when the head moved.
- R2.5 (wrong claim): The proposal's error table names the settings route as the command that helps for a stale base, but the Open questions section still reads 'Not spec-ready', so the claimed resolution of open questions and the spec-ready gate contradict each other. Resolution: answered: Open questions reads none. Error table commands name only the settings read and the job read routes.
- R2.6 (missing scenario): No scenario for an edit whose key is removed or set to an invalid value, or for an edit of a secret-like key in a config file; the refusal path for a secret in a config value is not stated. Resolution: answered: Added Invalid edit value requirement. A removed key or an invalid value is refused before any forge call.
- R2.7 (failure mode): The 'Single commit on retry' requirement relies on the job record naming the commit, but no scenario covers the record write itself failing after the forge commit succeeded, or the server restart before any record exists. Resolution: answered: Single commit on retry now covers a record write failure after the forge commit and a restart before any record exists through idempotent retry on the job ID.
- R2.8 (scope): tasks.md 2.3 requires the e2e step to test retry and refusal, but the proposal's scope lists 'Pull request automation' as out of scope without stating how the job close (ADR 101 decision 6, the next final state version) is handled; the job end state is not specified. Resolution: answered: Added Job close requirement from ADR 101 decision 6. The next final state version closes the job and expiry applies after 14 days.
- R2.9 (open question): The trailer format decision (one trailer with the job ID) does not state the trailer key name, so the exact commit text the tasks test is undefined. Resolution: answered: Trailer key is now exact. The commit carries one trailer Dustpan-Job with the job ID and tasks test that text.
- R2.10 (missing scenario): The design layers table requires a hostile test for the port adapter's path containment, but no spec requirement or scenario states the containment refusal for a path with traversal or an absolute path. Resolution: answered: Added Path containment requirement with traversal and absolute path scenarios.
- M.7 (dependency): The commit goes through the forge Commit method, but git-host-webhook-intake-post holds that method until the RFC 89 Q4 signing spike. The change declares no dependencies. Resolution: open question: Added depends_on git-host-webhook-intake-post and add-job-service-routes-start-1 and an owner open question on the Commit author and signature. The gate stays closed until the spike decides.

## Open questions

Not spec-ready. An implementer read this change against main on 14 Nov 2026 and could not start without these answers:

1. Which route and body start a config edit? `POST` jobs refuses the executor write-back today (`internal/api/jobs/start_test.go`). Is the delivery a direct push or a review request (ADR 101 decision 3 allows both)? Decides: PM, with the ADR check.
2. An implementer check found that "base still head" and "concurrent second edit" fail on any move of the head, while `Commit` returns `ErrConflict` only when a changed file differs from the base (`pkg/forge/forge.go`, ADR 101 decision 3); that the retry numbers are not from main (main uses the forge sentinels with the durable backoff); and that `internal/jobtest`, `internal/jobsvc` and `E2E_RUN` do not exist. Decides: PM, by a re-check of this change against main.

Earlier answers, before the implementer check:

1. What author and signature does the forge Commit method use for write-back commits? Decides: owner.
   - Options: Server forge identity as commit author with the starting principal named in the commit trailer; Starting principal as commit author with the server as committer.
   - Recommended: Server forge identity as commit author with the starting principal named in the commit trailer Reason: The signing spike in git-host-webhook-intake-post decides the Commit shape, and write-back must follow it.

Not spec-ready: question 1: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001.

Questions considered and answered:

- Does an API config edit commit through write-back or run on a runner? ADR 101 decisions 3, 4 and 5 answer it. The API edit starts a write-back job in the server that commits unit config through the Forge port and runs apply only.
- What happens when an edit names a file that does not exist at the base commit? ADR 101 decision 3 answers it with the ADR 110 extension noted in the ADR 101 chain. The deliver phase reads each file at the base commit, creates a missing file, and fails the job when a key changed after the base.
- Which forge errors retry and which fail the write-back job without retry? This change decides it in the spec. Retryable is transport errors, timeouts, 429 and 5xx with a bounded retry; refused is 4xx without retry.
- What exact trailer does the write-back commit carry? This change decides it in the spec. The commit carries one trailer with the key Dustpan-Job and the job ID.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
