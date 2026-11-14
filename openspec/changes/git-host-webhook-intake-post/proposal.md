---
version: 2
---

Issue: #1299, #1300, #1301.

## Why

**Persona: operator.** Operator installs the installation-level GitHub App and its webhook so pushes reach OpenDustpan as hints. The operator needs a stable hook URL, a secret rotation path with a bounded overlap, and a 204 intake that never leaks why it dropped a delivery, plus scoped tokens so runners never hold a long-lived credential.

The user path this change must make work:

1. **Server start.** Register the installation GitHub App, set its event secret and record the stable hook hostname from the federation guide.
2. **API.** Create the Git host connection in the namespace with POST /api/dustpan/v1/namespaces/{namespace}/git-host-connections and run its check route.
3. **Docs page.** Point the GitHub App webhook at https://<stable-host>/webhooks/<hook-id> and send a test delivery.
4. **E2e.** Send a signed push delivery to the real binary over TLS and check the 204 answer and the hint record.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Bad signature or unknown hook ID on POST /webhooks/{hook ID} | 204 with an empty body. The server counts the reject and logs only the reason class. | `POST /api/dustpan/v1/namespaces/{namespace}/git-host-connections/{id}/check` |
| Event for a namespace whose primary is another member | 204. The member drops the event and the federation poll catches the change. | `GET /api/dustpan/v1/components` |
| Files, Commits or Reviews call refused or unsupported | The adapter returns a refusal that names GitHub, or ErrUnsupported. | `POST /api/dustpan/v1/namespaces/{namespace}/git-host-connections/{id}/check` |

Evidence for the claims above:

- Slice 1 adds the connection store, installation-level GitHub App registration and map, connection routes and Check. Proof: Issue #1299 body: Slice 1 adds the connection store, the installation-level GitHub App registration and map, the connection routes and Check.
- This slice covers intake with two active secrets, 24-hour delivery dedupe, 204 for every request, the reject metric, map lookup and poll fallback. Proof: Issue #1299 body: ParseEvent for GitHub (X-Hub-Signature-256, two active event secrets, delivery ID dedupe for 24 hours), 204 for every request, git_host_event_rejected_total.
- This slice covers the Files, Commits and Reviews methods. Proof: Issue #1300 body: Head, ReadFile, Compare, Commit, FindCommit, OpenReview, ReadReview, CloseReview.
- This slice covers a scoped installation token of at most 1 hour. Proof: Issue #1301 body: RepoToken for the GitHub App (one repository, contents read, at most 1 hour).
- The connection routes exist on main. Proof: Pack main list: route POST /api/dustpan/v1/namespaces/{}/git-host-connections/{}/check.

## What Changes

- POST /webhooks/{hook ID} intake with GitHub signature check, two stored secrets, effect-state duplicate rule and 204 for every request
- Hint flow with installation-to-connection lookup and replica drop
- GitHub Files, Commits and Reviews methods behind capabilities
- RepoToken scoped to one repository for at most 1 hour, and refusal of unscoped tokens on the runner path

Out of scope:

- Connection store, registration, map, routes and Check from slice 1
- Comment commands and plan comment behavior
- Bridge dial-out process
- Merge, status and reaction operations of later slices
- Federation hostname provisioning

## Usability

Where the path still costs the person time or surprises them:

- The 204 for a reject hides the reason; the operator reads the reject metric and the reason class in the log.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Forge port declares ParseEvent. (`pkg/forge/forge.go:436 (func RetryAfter)`)
- Forge port declares RepoToken. (`pkg/forge/forge.go:421 (func RetryAfter)`)
- GitHub adapter implements ReadFile. (`pkg/forge/github/files.go:127 (func gitHub.ReadFile)`)
- GitHub adapter implements OpenReview. (`pkg/forge/github/review.go:62 (func gitHub.OpenReview)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 271: The Forge port, Git host connections and the installation-level GitHub App** (owner-tagged: decision 2, 3, 4, 8, 9, 10, 11).

- Decisions that apply: 2 (current), 3 (current), 4 (changed), 8 (current), 9 (current), 10 (current), 11 (extended).
- Changed by: ADR 286 changes; ADR 299 changes decision 4; ADR 299 extends decision 2; ADR 299 extends decision 7; ADR 300 changes decision 7; ADR 303 extends decision 11; ADR 336 extends. Chain to the end: ADR 286, ADR 299, ADR 300, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299, 300, 303, 336.
- Quote: "One interface with capabilities."
- Quote: "No Git host credential reaches a runner for the source of its job."
- Quote: "An event is a hint."
- Quote: "The route is `POST /webhooks/{hook ID}`."
- Quote: "The person or IaC gives the event secret."
- Quote: "**A replica drops an event that it cannot serve, and the poll catches the change.**"
- Quote: "User-facing text says "GitHub" or "Git host", never "forge"."
- Effect: Follows each decision. ParseEvent lives in the port, the route is outside the versioned API, and only a scoped token reaches the runner git path.
- Conflict: none.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged (all decisions)).

- Decisions that apply: 29 (current), 47 (current).
- Changed by: ADR 328 extends decision 1; ADR 359 extends decision 1; ADR 359 extends decision 15; ADR 359 extends decision 25. Chain to the end: ADR 328, ADR 359.
- Current rule: this ADR, with ADR 328, 359.
- Quote: "No polling (owner)."
- Quote: "The Forge port gets the operations that GitHost needs (PM)."
- Effect: Each delivery is a hint. No poll except the federation poll.
- Conflict: none.

**ADR 300: PM answers on runs, recorded** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The review watcher of RFC 12 item WB-10 does not use the `job.review-watch` poll."
- Effect: Merge and close deliveries are hints.
- Conflict: none.

**ADR 303: Scope limits: no Ansible stack, no CDKTF, and not a Git host** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "OpenDustpan is not a Git host. It does not host Git repositories, issues or pull requests."
- Effect: The change is a client of the Git host through the Forge port.
- Conflict: none.

**ADR 336: The GitHub App is the author of a write-back commit** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The GitHub App is the author of a write-back commit"
- Effect: Commit ships with the GitHub App as author.
- Conflict: none.

## Open questions

1. How long does the old event secret stay valid during rotation of the registration secret? Decides: owner.
   - Options: Keep both valid until the operator confirms; End the old secret after at most 7 days, with an audit entry.
   - Recommended: End the old secret after at most 7 days or at operator confirmation, with an audit entry. Show the window in the API. Reason: An open-ended overlap can leave a leaked secret valid for ever.

Not spec-ready: a code check fails: task "Add an e2e step on route POST /webhooks/{hook ID} with a signed push d" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph; a code check fails: contract method HandleDelivery: no requirement or scenario names it.

Questions considered and answered:

- Where does webhook parsing live? ADR 271 decision 4: ParseEvent in the port, the route in the internal adapter.
- May a long-lived token reach a runner? ADR 271 decision 3: only a scoped token.
- Does a member poll for missed changes? ADR 299 decision 29: only the federation poll and one catch-up read at start.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
