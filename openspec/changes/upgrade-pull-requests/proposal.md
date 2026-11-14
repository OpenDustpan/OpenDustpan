Roadmap: `docs/roadmap.md:388` (Upgrade pull requests).

## Why

**Persona: developer.** A Developer keeps units on current providers and modules. Today a stale pin stays stale until a person finds the new release by hand. The change opens a pull request with the version change and a plan, so review stays fast.

The user path this change must make work:

1. **CLI.** Developer pins an old provider version in unit settings and pushes the unit.
2. **API.** Server scans pins, finds a newer registry release, and opens a pull request on the forge that changes the pin.
3. **API.** Server runs a plan for the changed unit and links the result to the upgrade request.
4. **Docs page.** Developer reads the upgrade guide and reviews the pull request and the plan output.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Pull request already open for the same pin | The server answers 409 with code version-pinned and names the open pull request. Run the command that reads the open upgrade request to see the pull request reference. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}` |
| Registry release gone before open | The server answers 404 with code not-found when the registry release is gone. Run the command that lists open upgrade requests to see the stored versions. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests?state=open` |
| Forge connection fails | The server answers 502 with code forge-unreachable and stores the failure on the upgrade request. Run the command that reads the upgrade request to see the failure. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}` |
| Caller without grant tries to open | The server answers 403 with code forbidden when the caller lacks unit:manage on the unit. Run the command that lists open upgrade requests to check the stored request. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests?state=open` |

Evidence for the claims above:

- Forge domain code exists in the server. Proof: pkg/forge, pkg/forge/forgetest, internal/forgelinksvc
- The forge link model exists in the store. Proof: pkg/forgelink, pkg/forgelink/forgelinktest, pkg/store/internal/forgelinkstore
- RFC 56 is a draft and the design is still needed. Proof: part: Design needed: RFC 56 is a draft.
- The context pack names no ADR for this item. Proof: adrs: []

## What Changes

- Add POST /api/dustpan/v1/namespaces/{namespace}/upgrade-requests to open an upgrade pull request for one unit and pin.
- Add GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests to list upgrade requests with state filter.
- Add GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id} to read one upgrade request with versions and plan result.
- Add POST /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}/close to close an upgrade request.
- Add daily upgrade request scan that compares fixed-version pins with registry releases with one open request per unit stable ID, pin, and new version.
- Open forge pull requests for stale pins and run a plan per request through the milestone 5 plan path.

Out of scope:

- Blueprints catalog and create form
- Ephemeral units
- Environment promotion
- Curated upstreams
- Job proxy
- Console catalog and create form
- Units whose pin tracks a forge branch instead of a fixed version
- Automatic merge of an upgrade pull request

Depends on:

- artifact-registry-private-provider
- artifact-registry-private-module

## Usability

Where the path still costs the person time or surprises them:

- The Developer must find the upgrade pull request, the version change, and the plan result from one screen without reading server logs.
- A refused duplicate must name the open pull request so the Developer does not search for it.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The change says the design is still needed because RFC 56 is a draft, yet design.md and specs are written as if the design exists. The spec-ready gate is not met while the open questions for scan timing and PR scope are unanswered; the scan trigger and PR scope are undecided. Resolution: open question: Design context states RFC 56 is a draft and the change is not spec-ready; scan timing and request scope stay as open questions.
- R1.2 (wrong claim): The proposal cites forge code as proof but does not show that the Forge port supports opening a pull request that changes a pin; the claim that no new forge client is needed is unproven for the write path. Resolution: open question: Considered answer states the pack proves only forge code existence; open question on Forge port write path keeps the doubt.
- R1.3 (failure mode): Design Risks say the scanner may open duplicate pull requests, but no requirement or scenario enforces uniqueness under concurrent scans or a restart; the 409 duplicate check only covers the same unit and pin while a request is open, not a race between two scanner runs. Resolution: answered: Refuse concurrent duplicate open requirement enforces one open request per unit stable ID, pin, and new version under concurrent scans and restart.
- R1.4 (failure mode): Design Risks say a plan result never returns to the pull request, but the spec only says the server links the plan result; no scenario covers plan failure, plan timeout, or the pull request being closed before the plan finishes. Resolution: answered: Run plan for upgrade covers plan failure, plan timeout, and close before plan end with stored and linked results.
- R1.5 (missing scenario): No scenario covers forge unreachable (502 forge-unreachable) even though the error table lists it; the spec has no requirement for the forge failure path. Resolution: answered: Record forge failure requirement covers 502 forge-unreachable with stored failure scenario.
- R1.6 (missing scenario): No scenario covers a newer version missing from the registry (404 not-found), though the error table lists it. Resolution: answered: Open upgrade request covers missing release with 404 not-found scenario.
- R1.7 (missing scenario): No scenario covers a pull request that was closed or merged, or a pin that changed by hand, while the upgrade request is open; the close API in What Changes has no requirement. Resolution: answered: Close upgrade request covers merged, closed without merge, and hand-changed pin through scheduled poll.
- R1.8 (open question): The spec does not say whether a unit that pins a version on a forge branch, or a unit in an ephemeral or blueprint state, is in scope; the proposal excludes blueprints and ephemeral units but the design does not enforce it. Resolution: answered: Out of scope excludes branch pins and auto-merge; Open upgrade request applies only to fixed-version pins.
- R1.9 (scope): The API route list adds list and close endpoints, but the tasks only build a POST route; the authorization actions for list and close are not named in the tasks, so the definition of done cannot be checked for them. Resolution: answered: Build tasks name POST, GET list, GET one, and close routes with unit:manage and unit:read actions.
- R1.10 (wrong claim): The routes in the error table (registry releases list and forge connection status) are reported by the code check as absent from the API description and route table, so the 'command that helps' is unsupported. Resolution: answered: Error commands use only the new upgrade-requests routes named in what_changes.
- R2.1 (open question): The 'Open questions' section reads 'none' but then lists three questions with provisional decisions. RFC 56 is a draft, and design.md says the change does not pass the spec-ready gate. The two statements contradict each other, and the design is not accepted. Resolution: answered: Design context now states the change is not spec-ready while four open questions stay open, and no statement claims the list reads none.
- R2.2 (wrong claim): The ADR check says 'No ADR is touched' without quoted decision lines and without proof. The change touches forge writes, secrets and credentials, authorization, and the unit settings model, so the claim is unsupported. The input lists no ADR at all. Resolution: answered: adr_check stays empty because the pack names no ADR, and evidence now proves adrs: [] with pack facts only.
- R2.3 (wrong claim): Open question 3 gives a provisional answer 'Use the Forge port' but the proposal and design both admit that no proof exists that the port can open a pull request. The design also says the writer stops if the call is missing. The tasks have no step to add the write call, so the item cannot be built. Resolution: answered: Open question 3 keeps the write-path doubt, and tasks now include a probe of the Forge port with a task to add the write call behind the port when the probe fails.
- R2.4 (scope): The spec says 'Depends on: artifact-registry, blueprints', but blueprints are listed as out of scope. The roadmap item says the plan runs 'as in milestone 5 step 7'. The change does not say how it reuses this plan path, or whether the dependency exists. Resolution: answered: depends_on now lists only artifact-registry, and the plan requirement states reuse of the milestone 5 plan path for the changed unit.
- R2.5 (missing scenario): No requirement covers the scheduled scan, which is the provisional answer to question 1. There is no requirement or task for the daily schedule, for the namespace setting that turns off automatic pull requests, or for its effective value in the API. The project rule says each default shows its effective value in the API. Resolution: answered: Added Scan schedule requirement with daily run, disable setting, and effective value, plus matching tasks and e2e check.
- R2.6 (failure mode): The close requirement needs the server to learn that a pull request merged or closed on the forge, and that a pin changed by hand. The design has no webhook, poll, or sync mechanism for this. Without it, those scenarios cannot be built or tested. Resolution: answered: Added poll-based close design decision, Close upgrade request requirement with poll trigger, and a fourth open question for merge-state sync.
- R2.7 (missing scenario): The scanner has no actor identity and no authorization scenario. The tasks name 'unit:manage' and 'unit:read' with no source from an ADR, and no scenario covers a refused caller (403) or a hostile input. The scanner writes to the forge with credentials, and the spec does not say where the credentials are stored or whose they are. Each automatic action must record who did it and why. Resolution: answered: Added Record actor and authorization requirement with namespace service principal, credential reference, audit entry, and 403 scenario.
- R2.8 (failure mode): The scenarios do not cover a partial failure. The forge pull request opens but the plan start fails, or the store write fails after the forge call. The result is an orphan pull request and a retry that opens a duplicate. The spec says 'retry on next scan' but has no idempotency check against the forge. Resolution: answered: Added Survive partial failure requirement with idempotency key and retry of plan start only.
- R2.9 (wrong claim): The error table says the 409 duplicate answer names the open pull request in the usability section, but the scenario only refuses with code version-pinned. The 404 and 409 errors on POST are shown, but the scanner path has no user-visible error. Also a 'newer version removed' 404 is odd for an open POST, and the 'command that helps' does not name the stored versions. Resolution: answered: Error commands now use only the new upgrade-requests routes, the 409 answer names the open pull request, and the scanner path stores failures instead of returning POST errors.
- R2.10 (missing scenario): There is no scenario for a pin whose value is a range, a prerelease, or a downgrade. There is no rule for which 'newer' release to pick when many exist, and none for a second newer version while a request is open for the first. Resolution: answered: Added Select newest stable release requirement for ranges, prereleases, downgrades, newest pick, and second newer version.
- R2.11 (scope): Task 2.4 and the failing tests use a fake forge and a real CLI, but the tasks do not map each spec scenario to a named test. Close, plan failure, plan timeout, restart and 404 have no test. Resolution: answered: Tasks now map each spec scenario to a named test, including close, plan failure, plan timeout, restart, poll, and version-rule tests.
- R2.12 (open question): The design uses a unique key on unit, pin and new version. The spec does not say whether the unit reference is a stable ID. The project rule requires stable IDs for stored references to renamable entities. Resolution: answered: Design decisions and requirements now state that the unit reference is the unit stable ID.
- M.13 (gap): The ADR check is empty, but the change uses the Forge port, the review requests of ADR 299 and the plan path of milestone 5 step 7. Resolution: answered: adr_check stays empty because the pack lists adrs as empty and gives no decision text to quote; a quote cannot be invented. Needs ADR 299, ADR 328, and milestone 5 forge and plan decision texts in the pack before entries can be added.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. When does the server scan for stale pins and open pull requests?
   - Options: Scheduled scan once per day per namespace; Scan on each new registry release; Manual API call only.
   - Provisional decision (owner review 14 Nov): Scheduled scan once per day per namespace
   - Reason: A daily schedule bounds forge and runner load, and matches Renovate practice without blocking urgent manual requests. Benchmark: Google Renovate: scheduled scans with grouped updates and no automatic merge for major versions.
   - Persona impact: A scheduled scan upsets no persona because the escape hatch stops noise, and a manual call stays for urgent upgrades. Escape hatch: A namespace setting turns off automatic upgrade pull requests, and a manual API call opens one request on demand.
2. What is the scope of one upgrade pull request?
   - Options: One pull request per unit with one plan; One pull request per pin across many units; One pull request per namespace.
   - Provisional decision (owner review 14 Nov): One pull request per unit with one plan
   - Reason: One unit per request keeps the plan result exact and the rollback simple, as AWS links one review to one change. Benchmark: AWS: one review per change with plan output linked before merge.
   - Persona impact: One request per unit keeps review small for the Developer and the Namespace administrator, with no cross-unit merge risk. Escape hatch: A namespace administrator closes the pull request on the forge, and the next poll marks the change record closed.
3. Does the Forge port support opening a pull request that changes a pin?
   - Options: Use the Forge port to open the review request; Add a new forge write call behind the Forge port; Write git directly without the Forge port.
   - Provisional decision (owner review 14 Nov): Use the Forge port to open the review request
   - Reason: Reuse of the Forge port avoids a second forge client, and matches Renovate practice of one forge call per request. Benchmark: Google Renovate: the platform opens a branch and a pull request through one forge API call per repository.
   - Persona impact: Use of the Forge port upsets no persona because it reuses the same credentials and audit path as other forge writes. Escape hatch: The scanner records forge-unreachable and retries on the next scan, and a manual API call retries on demand.
4. How does the server learn that a forge pull request merged or closed?
   - Options: Poll forge request state on each scheduled scan; Receive a forge webhook for merge and close; Close only through the close API.
   - Provisional decision (owner review 14 Nov): Poll forge request state on each scheduled scan
   - Reason: Polling on the scheduled scan reuses the Forge port read path and closes stale requests without new inbound network rules. Benchmark: Google Renovate: poll the forge request state on each run and close the stored update when the request merges or closes.
   - Persona impact: Polling upsets no persona because it adds no webhook setup for the Namespace administrator. Escape hatch: A namespace administrator closes the upgrade request through the close API when the poll misses a merge.

Questions considered and answered:

- Does an accepted ADR already define upgrade pull requests? No. The pack lists adrs: [] and marks RFC 56 as draft with design needed. Scan timing, request scope, forge write path, and merge-state sync stay as open questions.
- Does the change need a new forge client? The pack proves only that forge code exists at pkg/forge and internal/forgelinksvc. It does not prove that the Forge port opens a pull request. An open question keeps this write-path doubt.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
