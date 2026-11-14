Issue: #1299, #1300, #1301.

## Why

**Persona: operator.** Operator installs the installation-level GitHub App and its webhook so pushes reach OpenDustpan as hints. The operator needs a stable hook URL, a secret rotation path with overlap, and a 204 intake that never leaks why it dropped a delivery, plus scoped tokens so runners never hold a long-lived credential.

The user path this change must make work:

1. **Server start.** Register the installation GitHub App, set its event secret and record the stable hook hostname from the federation guide.
2. **API.** Create the Git host connection in the namespace and run Check.
3. **Docs page.** Point the GitHub App webhook at https://<stable-host>/webhooks/<hook-id> and send a test delivery.
4. **Console.** Watch the hint arrive; a bad signature still answers 204 and counts a reject.
5. **E2e.** Run a job that reads code through the scoped token path.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Bad signature or unknown hook ID on POST /webhooks/{hook ID} | 204 with reject counted; no detail in body. Run `opendustpan doctor` and read the intake reject log class. | `opendustpan doctor` |
| Event for a namespace whose primary is another member | 204 and drop; the primary picks up the change by poll. Run `opendustpan status` to see which member is primary. | `opendustpan status` |
| Files, Commits or Reviews call refused or unsupported | Git host refused the credential or the call is unsupported. Run `opendustpan check` to test the connection and its capability list. | `opendustpan check` |

Evidence for the claims above:

- Slice 1 adds the connection store, installation-level GitHub App registration and map, connection routes and Check. Proof: Issue #1299 body: Slice 1 (draft PR from branch claude/886-git-host-slice1) adds the connection store, the installation-level GitHub App registration and map, the connection routes and Check.
- This slice covers intake with two active secrets, delivery dedupe for 24 hours, 204 for every request, the reject metric, map lookup and poll fallback. Proof: Issue #1299 body: ParseEvent for GitHub (X-Hub-Signature-256, two active event secrets, delivery ID dedupe for 24 hours), 204 for every request, git_host_event_rejected_total, installation-to-connection lookup through the map, poll fallback.
- This slice covers the Files, Commits and Reviews methods and the open signing question. Proof: Issue #1300 body: Head, ReadFile, Compare, Commit, FindCommit, OpenReview, ReadReview, CloseReview, with the forgetest cases that already exist, and the capabilities that they list. The signing question (RFC 89 Q4) needs its spike first.
- This slice covers a scoped installation token of at most 1 hour and refusal of unscoped tokens on the runner path. Proof: Issue #1301 body: RepoToken for the GitHub App (one repository, contents read, at most 1 hour), the repo-token and git-access capabilities, the refusal of an unscoped token on the runner path.

## What Changes

- POST /webhooks/{hook ID} intake with GitHub signature check, two stored secrets, effect-state duplicate rule and 204 for every request
- Hint flow with installation-to-connection lookup and replica drop with poll catch-up
- GitHub Files and Reviews reads behind capabilities with Commit held pending the signing spike
- RepoToken scoped to one repository for at most 1 hour and refusal of unscoped tokens on the runner path

Out of scope:

- Connection store, registration, map, routes and Check from slice 1
- Comment commands and plan comment behavior
- Bridge dial-out process
- Merge, status and reaction operations of later slices
- Federation hostname provisioning; this change only uses the stable hostname

## Usability

Where the path still costs the person time or surprises them:

- Hook setup needs DNS and secret rotation steps; the guide must give copyable commands.
- A 204 for rejects hides the reason; the operator runs `opendustpan doctor` to see the reason class.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 271: The Forge port, Git host connections and the installation-level GitHub App** (owner-tagged: decision 2, 3, 4, 8, 9, 10).

- Decisions that apply: 2 (current), 3 (current), 4 (changed), 8 (current), 9 (current), 10 (current).
- Changed by: ADR 286 changes; ADR 299 changes decision 4; ADR 299 extends decision 2; ADR 299 extends decision 7; ADR 300 changes decision 7; ADR 303 extends decision 11; ADR 336 extends. Chain to the end: ADR 286, ADR 299, ADR 300, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299, 300, 303, 336.
- Quote: "One interface with capabilities."
- Quote: "No Git host credential reaches a runner for the source of its job."
- Quote: "An event is a hint."
- Quote: "The person or IaC gives the event secret."
- Quote: "**A replica drops an event that it cannot serve, and the poll catches the change.**"
- Quote: "**One installation-level GitHub App is the default. A per-namespace GitHub App is an option.**"
- Effect: Follows the Forge port shape, the scoped token refusal, the hint route outside the versioned API, the write-only secret shape, the replica drop with poll catch-up, and the installation app default.
- Conflict: none.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged (all decisions)).

- Decisions that apply: 29 (current).
- Changed by: ADR 328 extends decision 1; ADR 359 extends decision 1; ADR 359 extends decision 15; ADR 359 extends decision 25. Chain to the end: ADR 328, ADR 359.
- Current rule: this ADR, with ADR 328, 359.
- Quote: "No polling (owner). Webhooks, a "Plan again" button and the command `dustpan plan`, one catch-up read at start, and manual redeliveries that the server decides from the state of the effect."
- Effect: Treats each delivery as a hint, keeps the federation poll path, and decides redelivery from the state of the effect.
- Conflict: none.

**ADR 300: PM answers on runs, recorded** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The review watcher of RFC 12 item WB-10 does not use the `job.review-watch` poll."
- Effect: Treats merge and close deliveries as hints that start a read before any state change.
- Conflict: none.

**ADR 303: Scope limits: no Ansible stack, no CDKTF, and not a Git host** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "OpenDustpan is not a Git host. It does not host Git repositories, issues or pull requests."
- Effect: Keeps OpenDustpan as a client of the Git host and adds no hosting behavior.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The overlap window of the two active event secrets during rotation is open and decided by the owner. ADR 271 decision 8 fixes no overlap rule. The design, the spec and the tasks all need it before the intake can be built. Resolution: open question: Kept as open question on secret overlap window; design accepts both secrets until the owner fixes the window.
- R1.2 (wrong claim): The error table and the usability notes name commands that the code check says do not exist: opendustpan federation status, opendustpan git-host-connections check and an intake log query. The user path cannot be completed as written, and the proposal is marked not spec-ready. Resolution: answered: Error commands use `opendustpan doctor`, `opendustpan status` and `opendustpan check`; user path names no missing command.
- R1.3 (adr conflict): The dedupe rule drops any repeated delivery ID for 24 hours and counts it as a reject. ADR 299 decision 29 (owner) says manual redeliveries are decided by the server from the state of the effect. A manual redelivery is therefore dropped and not decided, which contradicts the owner decision. Resolution: answered: Duplicate rule now derives from the state of the effect; dedupe store is a best-effort bound and manual redelivery is decided from effect state.
- R1.4 (failure mode): The dedupe store is not shared across federation members and is lost on restart. A replayed delivery sent to another member, or after a restart, is accepted again and starts a duplicate hint. The design does not address this. Resolution: answered: Design names the hint key and the fail-closed drop when effect state is unavailable, correct across members and restarts.
- R1.5 (missing scenario): The intake spec has no scenario for an unknown hook ID, a path traversal in the hook ID, a body above the size bound, or a secret shorter than 32 bytes. The design layer table and ADR 271 decision 8 require each of these, and the hostile tests depend on them. Resolution: answered: Added scenarios 'Unknown hook ID', 'Traversal in hook ID', 'Oversize body' and 'Short secret refused' in git-host-webhook-intake; 'Unknown hook ID' covers unknown IDs, 'Traversal in hook ID' covers path traversal, 'Oversize body' covers the size bound, 'Short secret refused' covers the 32-byte rule.
- R1.6 (missing scenario): The Files, Commits and Reviews spec has a scenario for ReadFile and OpenReview only. It has none for Compare, Commit, FindCommit, ReadReview, CloseReview, or for a call that returns ErrUnsupported when a host or auth kind cannot do it, which ADR 271 decision 2 requires. Resolution: answered: Added scenarios 'Compare and find commit', 'Unsupported call', 'Open and read review' and 'Close review' in git-host-files-commits-reviews; Commit stays held pending the signing spike.
- R1.7 (open question): The proposal says the signing question (RFC 89 Q4) needs a spike first, but the open questions section does not list it. The Commit method ships without that question answered or assigned. Resolution: open question: Added open question on commit author and signature pending the RFC 89 Q4 spike.
- R1.8 (missing scenario): The poll fallback that the issue scope and the proposal name has no requirement or scenario. The behavior of the replica drop when federation is off is also not specified. Resolution: answered: Added requirement 'Poll fallback' with scenario 'Single member without event' for missed deliveries on an installation without federation.
- R1.9 (missing scenario): The stable hook hostname that ADR 271 decision 9 requires, which survives a promotion, has no requirement or scenario, although the docs task relies on it. Resolution: answered: Added requirement 'Stable hook hostname' with scenario 'Promotion keeps URL' that keeps the stored URL across failover, plus scenario 'Reject redaction'.
- R1.10 (missing scenario): The design names the risk of a token or secret in a log or an error. No scenario checks that the reject log and the token path redact both, and no scenario covers a token used after its one-hour expiry. Resolution: answered: Added scenario 'Reject redaction' for secret and token redaction and scenario 'Expired token refused' for use after the one-hour expiry in git-host-repo-token.
- R2.1 (open question): The overlap window of the two active event secrets during rotation is undecided, yet design and tasks depend on it. The proposal's open question 1 is owner-decided with a recommendation, but the spec and intake cannot be built without the rule. File: design.md and proposal.md. Resolution: open question: Intake accepts both stored secrets until the owner fixes the overlap window; build does not wait on the answer.
- R2.2 (open question): The commit author and signature rule for the Commit method is not answered; Commit is scheduled in design task 2.2 without it. ADR 336 is cited as answering question 4 but the change does not quote or apply it. File: proposal.md open question 2 and tasks.md 2.2. Resolution: open question: Commit held until RFC 89 Q4 spike; tasks ship reads first and ADR 336 carries no numbered decision in the pack.
- R2.3 (adr conflict): ADR 271 decision 4 requires the route to be outside /api/dustpan/v1 with no word forge, and ADR 299 decision 29 says manual redeliveries are decided by the server from the state of the effect. The spec drops repeated delivery IDs for 24 hours without reject, which is a dedupe window that ADR 299 does not grant; the duplicate rule must derive only from effect state. File: specs/git-host-webhook-intake/spec.md, requirement Event hint parsing. Resolution: answered: Duplicate rule rewritten to derive from effect state per ADR 299 decision 29; 24-hour store kept as best-effort bound only.
- R2.4 (failure mode): The dedupe store is unshared and lost on restart. Design decision 4 relies on idempotent effects, but no requirement states how the hint key is computed or what a replayed delivery does when the effect state is unavailable. File: design.md decision 4. Resolution: answered: Requirement now names hint key of hook ID plus delivery ID plus effect key and the fail-closed drop when effect state is unavailable.
- R2.5 (missing scenario): No scenario covers a body with an invalid signature that also exceeds the size bound, nor a parse timeout, though design layer table requires bounded parse time. File: specs/git-host-webhook-intake/spec.md. Resolution: answered: Added oversize plus bad-signature and parse-time-bound scenarios.
- R2.6 (missing scenario): No scenario for a Check or RepoToken call when the host returns an auth failure or rate limit, which the error table promises as a refusal message naming the one command. File: specs/git-host-files-commits-reviews/spec.md. Resolution: answered: Added auth failure and rate-limit scenarios for Check and RepoToken paths.
- R2.7 (wrong claim): The ADR check says ADR 271 decision 9 gives the poll as the only path, but decision 9 is quoted as changed only by ADR 299 extension; the proposal's claim that ADR 271 decision 8 fixes the 32-byte minimum with no overlap is not checked against decision 8 text. File: proposal.md ADR check. Resolution: answered: ADR check quotes decision 8 32-byte line verbatim and no longer claims poll as only path.
- R2.8 (scope): Task 3.3 asks for federation guide documentation of the stable hook hostname, but the proposal lists the stable hostname as part of the install path; the item scope is intake only, so the docs task overlaps the federation item. File: tasks.md task 3.3. Resolution: answered: Docs task limited to intake section that uses the stable hostname; provisioning stays out of scope.
- R2.9 (failure mode): Concurrent deliveries for the same hook ID during a secret rotation are not specified; the verifier may accept a body against one secret and reject it against the other within the same request. File: design.md Layers table, signature and dedupe row. Resolution: answered: Verifier defined to check both secrets from one registration snapshot with per-secret constant-time compare.

## Open questions

1. How long does the old event secret stay valid during rotation of the app registration secret? Decides: owner.
   - Options: Keep both secrets valid until the operator confirms the new secret; Expire the old secret after 24 hours by default.
   - Recommended: Keep both secrets valid until the operator confirms the new secret, then revoke the old secret at once. Reason: ADR 271 decision 8 fixes the write-only secret shape and the 32-byte minimum but fixes no overlap window, and the pack gives two active secrets with no expiry rule.
2. What author and signature does the GitHub Commit method use before it ships? Decides: owner.
   - Options: Hold Commit until the RFC 89 Q4 spike fixes author and signature; Ship Commit unsigned and add a signature later.
   - Recommended: Hold Commit until the RFC 89 Q4 spike fixes author and signature. Reason: Issue #1300 says the signing question (RFC 89 Q4) needs its spike first, and the pack gives no author or signature rule for Commit.

Not spec-ready: question 1: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001; question 2: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001.

Questions considered and answered:

- Where does webhook parsing and routing live? ADR 271 decision 4 answers it: ParseEvent lives in the port and the route lives in the internal adapter, outside /api/dustpan/v1 with no word forge.
- May a long-lived Git host token reach a runner? ADR 271 decision 3 answers it: only a scoped token may leave the server on the runner git path, and the runner path refuses an unscoped token.
- What happens when a non-primary member receives an event? ADR 271 decision 9 answers it: a replica drops an event it cannot serve and the poll catches the change, with a stable hook hostname.
- Do the two open rotation and signature points block the intake build? Neither ADR 271 nor the pack gives the overlap window; both stored secrets stay valid until the operator revokes one, so intake is buildable while the window stays an open question. ADR 336 carries no numbered decision text in the pack, so it does not answer the Commit author rule and Commit stays held pending the RFC 89 Q4 spike.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
