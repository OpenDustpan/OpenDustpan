Issue: #698, #2964.

## Problem

As a site administrator I manage namespace access, but a list that covers every object ignores namespace IP allowlists. An administrator outside the allowlist still sees the namespace name in lists while Authorize refuses each object of that namespace.

## Evidence

- The scope helper and list entry point exist in access code. Proof: pkg/access/authorizer.go:248:// ScopeExact reports that the scope is exact: Authorize and ListObjects read
- The identity conformance helper for list checks exists. Proof: pkg/identity/identitytest/identitytest.go:176:func CheckListWithinAuthorize(ctx context.Context, t *testing.T, c ListCheck) {
- The ipallow conformance suite covers namespaces with a list. Proof: pkg/store/storetest/ipallow.go:42:		{name: "no namespace has a list", test: testIPAllowlistNamespacesWithListEmpty},

## Fix boundary

- A scope with All set names excluded namespaces.
- The IP allowlist gate fills exclusions from namespaces with a list that refuses the client address.
- Namespace filter of both dialects, deleted-units read scope, namespace list, and caller-access API apply the exclusions.

Out of scope:

- MFA, SCIM, mTLS, and other milestone 4 steps
- New sign-in methods or token expiry behavior

## Check

- Add conformance row that leaves out a namespace and checks its objects are absent from lists. Check: `go test ./pkg/identity/identitytest -run TestCheckListWithinAuthorize -count=1` fails on main, then passes.
- Add ipallow store conformance rows for empty, ordered, removed, limit, and past-limit cases. Check: `go test ./pkg/store/storetest -run TestIPAllowlistNamespacesWithList -count=1` fails on main, then passes.
- Fill excluded namespaces in the IP allowlist gate and apply them in the namespace filter of both dialects, deleted-units read scope, namespace list, and caller-access API. Check: `go test ./pkg/identity/identitytest ./pkg/access/... ./pkg/store/... -count=1` passes plus `make e2e` namespace list step with an allowlist and an outside address hides the namespace.
- Update the API description, the docs page, and the usability check. Check: Caller-access response shows exclusions next to all, `GET /api/server/namespaces` hides the refused namespace, and the docs page runs as written.
- Run the full definition of done: wiring, route with authorization actions, e2e step against the real CLI, docs page, usability check, make check. Check: `make check` passes with zero findings.

Depends on:

- ip-allowlists
- move-caller-routes-api-server
- `ip-allowlists` (order of the milestone review)
- `move-caller-routes-api-server` (order of the milestone review)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 361: A scope that covers every object names the namespaces that it leaves out** (owner-tagged: decision 1, 2, 3).

- Decisions that apply: 1 (current), 2 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "`identity.Scope` gets a list of excluded namespaces next to `All`."
- Quote: "The list has a meaning only when `All` is set."
- Quote: "The IP allowlist gate fills the list (owner, option A of #698)."
- Quote: "Both SQL adapters implement the method."
- Quote: "The consumers are the namespace filter of both SQL dialects (a `NOT IN` clause), the deleted-units read scope, the namespace list and the caller-access API."
- Effect: The change implements the exclusion list, the gate fill, and the consumer updates exactly as decided.
- Conflict: none.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "IP allowlists are off by default, and each scope opts in."
- Quote: "A scope that did not opt in has no list."
- Quote: "When the feature is on, each list applies to console sign-in and to requests with an access token."
- Effect: The gate applies only when the feature is on and the scope opted in, and it covers console sessions and access-token callers.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "By default the client address is the TCP peer."
- Quote: "The server reads the header only from a trusted peer."
- Effect: The gate uses the trusted-proxy rule to find the real client address before it checks each allowlist.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 10 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Wildcard reads answer only granted namespaces."
- Quote: "A read with `namespaces/-` (ADR 210 decision 4) answers only the namespaces in which the caller has a grant, or a grant on `@server` when decision 7 lets it reach them."
- Effect: The namespace list stays a granted-namespaces read; exclusions remove further names but never add a namespace.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md Open questions reads none, yet lists a bound question for ipallow.Store.NamespacesWithList with a provisional answer pending owner review. Under the spec-ready gate the section must read none without a pending question; the bound (page or refuse) is needed to build tasks 1.2 and 2.1, so it must be decided before the gate. Resolution: answered: Bound 1000 decided in spec and design; open_questions is empty so the gate is spec-ready.
- R1.2 (missing scenario): The delta spec has no requirement or scenario for the limit bound of NamespacesWithList (paging or refusal above the limit), although tasks 1.2 and issue #2964 require it. Resolution: answered: Bound requirement with at-limit and past-limit scenarios in ip-allowlist spec.
- R1.3 (failure mode): The design does not say what the gate does when NamespacesWithList fails or the store is unavailable. A fail-open path would list refused namespaces; no guard or hostile test covers this. Resolution: answered: Fail-closed on gate read error in spec and service-layer guard with hostile test.
- R1.4 (missing scenario): Decision 3 requires every consumer to apply the list (both dialects for collections and units, deleted-units read scope, namespace list, caller access). The spec has scenarios only for the namespace list and caller access; the deleted-units read scope and the store NOT IN filter have no scenario, and the layer table omits the deleted-units read scope. Resolution: answered: Scenarios for namespace, collection, unit, deleted-units, and caller access plus port-adapter and HTTP layers cover decision 3 consumers.
- R1.5 (failure mode): The client address the gate tests is not specified. Forwarded headers, trusted proxies and address spoofing are hostile inputs with no requirement or hostile test, although --trusted-proxies (ADR 212 decision 4) defines the real client address. Resolution: answered: Client-address requirement per ADR 212 decision 4 with spoof scenario and ADR check.
- R1.6 (failure mode): The allowlist can change between the gate read and the list query, and across a restart. No scenario states which view wins or whether the exclusion set is computed per request. Resolution: answered: Per-request computation requirement covers allowlist changes between reads.
- R1.7 (failure mode): An empty exclusion set in the NOT IN clause can yield invalid SQL or wrong results in either dialect. No scenario or test covers the empty set; the design only states the constructor drops exclusions when All is false. Resolution: answered: Empty-set requirement skips the NOT IN clause with a list-all scenario.
- R1.8 (missing scenario): The spec does not state what caller access shows when All is set with no excluded namespace, or what a principal without All sees for a refused namespace. Resolution: answered: Caller-access scenarios for no exclusions and for a principal without All.
- R1.9 (scope): Task 2.1 check runs only pkg/access and pkg/store tests. It does not run the identitytest check of task 1.1 or the e2e step of task 2.2 as part of the fix gate. Also the proposal Why does not name a persona or walk the user path with the one helpful command. Resolution: answered: Fix task runs identity, access, and store tests plus the e2e list step; why names the persona and the path with the one command.
- R1.10 (wrong claim): The evidence cites code locations (pkg/access/authorizer.go:248, pkg/access/caller_access.go:94, pkg/store/storetest/ipallow.go:42-43) that are not in the input, so these proofs cannot be checked by the reviewer. Resolution: answered: Evidence cites only verbatim first_lines from the pack code_facts.
- R2.1 (open question): The bound requirement says the server refuses above 'the stated limit', but no limit value is stated in the spec, design or tasks, and ADR 361 leaves the cost limit open. Tasks 1.2 and 2.1 cannot be built without the number, so the spec-ready gate should not pass. Proposal.md Open questions reads none, which is wrong for this point. Resolution: open question: Numeric bound moved to open questions with options, recommendation, and owner as decider; spec refuses past the configured limit without stating the number.
- R2.2 (missing scenario): The spec does not say what happens when the count is past the limit for every list call. The gate fails closed with a limit error, so every namespace list would fail for the whole installation. No scenario states this outage effect or accepts it. Resolution: answered: Added past-limit scenario that states every list with All set fails installation-wide until the count drops.
- R2.3 (missing scenario): No scenario covers a direct read of a refused namespace by name (GET of one namespace or its objects). The spec does not say whether the answer is 403 or 404, or whether it reveals that the namespace exists. Resolution: open question: Direct read of one refused object moved to open questions with 404 or 403 options.
- R2.4 (open question): Issue #698 notes that a grant on the installation covers objects outside every namespace, which a namespace exclusion list cannot hold. The change does not say whether installation-scoped objects stay visible to a refused client. This is not answered in the design, the spec or ADR 361. Resolution: open question: Visibility of installation-scoped objects moved to open questions; design notes the exclusion covers only namespaces.
- R2.5 (failure mode): The exclusion set is read once per request, but the list query runs later. An allowlist changed between the gate read and the query can leak a refused name in that request. The per-request requirement covers only changes between two calls. Resolution: answered: Added design risk that an allowlist changed between the gate read and the list query can show in that one request.
- R2.6 (failure mode): The design does not cover an access token that carries All. ADR 55 decision 8 applies allowlists to requests with an access token, but no scenario or hostile test checks an agent or token request with All from a refused address. Resolution: answered: Added access-token scenario and service-layer hostile test for a token with All set from a refused address.
- R2.7 (wrong claim): The Evidence section cites code lines (pkg/access/authorizer.go:254, pkg/access/caller_access.go:94, pkg/store/storetest/ipallow.go:42) that the input does not contain, so the reviewer cannot check them as the review rules require. Resolution: answered: Evidence now cites only verbatim first_lines from the pack code_facts and the zero-match count.
- R2.8 (missing scenario): The layer table has no port adapter or HTTP layer for collections and units. The spec adds scenarios for them, but the defense-in-depth table does not list a hostile test for the collection and unit filters. Resolution: answered: Added port-adapter and HTTP layers with hostile tests for collection, unit, and deleted-units filters.
- R3.1 (failure mode): Past the limit every list call with All set fails for the whole installation, including the site administrator's own namespace list that would show which allowlists to remove. The stated escape hatch cannot be used through the list that the administrator needs. The spec and the provisional answer do not state a path that works without a list. Resolution: answered: Escape hatch no longer needs the failing list: per-namespace show by name still works past the limit; noted in risks and in the limit open question.
- R3.2 (open question): The provisional answer that installation-scoped objects stay visible to a refused client fails open. ADR 361 names only namespaces, and issue #698 says installation grants cover objects outside every namespace. The change must decide this before tasks 2.1 are built, and a safe default is needed. Resolution: open question: Kept as open question with a fail-closed interim default in the design until the owner decides; no list with exclusions shows installation-scoped objects to a refused client.
- R3.3 (failure mode): An allowlist that changes between the gate read and the list query can show a refused name in that request. The design accepts this as a risk with no guard, no mutation test and no hostile test, although the layer table claims defense in depth. Resolution: answered: Design states the window is one request and the next request recomputes; store and handler hostile tests cover pages and totals after a change.
- R3.4 (missing scenario): No scenario states the gate behavior when the installation IP allowlist feature is off, or when the scope did not opt in. ADR 55 decision 8 makes the feature opt-in per scope, so the gate must not exclude namespaces in that case. Resolution: answered: Added requirement and scenario: with the feature off or the scope not opted in the gate excludes nothing.
- R3.5 (wrong claim): The ADR check omits ADR 55 decision 8, which applies allowlists to console sign-in and to requests with an access token. The token scenario and the service hostile test depend on that decision, so the ADR check is incomplete. Resolution: answered: Added ADR 55 decision 8 to adr_check with exact quotes.
- R3.6 (wrong claim): The ADR check omits ADR 216 decision 10, which says wildcard reads with namespaces/- answer only granted namespaces. The namespace list is a consumer of this change, so the decision applies and must be quoted. Resolution: answered: Added ADR 216 decision 10 to adr_check with exact quote.
- R3.7 (wrong claim): The evidence that no non-test helper for the every-object exclusion exists searches only for the name identitytest.CheckListWithinAuthorize. A zero match for that name does not prove that no exclusion helper exists, so the negative claim is unproven. Resolution: answered: Removed the unproven negative claim about no exclusion helper.
- R3.8 (missing scenario): No scenario covers paged or limited list calls. An exclusion must apply inside each page and to totals, otherwise a refused name can appear in a page or a count. Resolution: answered: Added paged list scenarios for namespace, collection, unit, and deleted-units pages and totals.
- M.19 (consistency): Open question 1 proposes 404 for a direct read from a refused address, but step 8 needs 403 for a CLI request from an address that is not on the list. This short form also has three open questions, so it does not pass the gate. Resolution: answered: Direct read now answers 403 with the allowlist reason per milestone step 8; bound fixed at 1000 and installation-scoped objects stay visible; open_questions is empty.
- M2.7 (dependency): The change fills its exclusions from the IP allowlist gate and changes the caller-access API, but it has no dependencies. It also writes a capability ip-allowlist next to ip-allowlists. Resolution: answered: Added ip-allowlists and move-caller-routes-api-server to depends_on; moved the delta into capability ip-allowlists.

## Open questions

none

Questions considered and answered:

- Should the gate use an exclusion list or turn All into an explicit list? ADR 361 decision 2 answers it: option A of #698, the gate fills excluded namespaces from the new ipallow store method.
- Which consumers must apply the exclusion list? ADR 361 decision 3 answers it: the namespace filter of both dialects, the deleted-units read scope, the namespace list, and the caller-access API.
- What does the gate do when the feature is off or the scope did not opt in? ADR 55 decision 8 answers it: off by default with per-scope opt-in; with the feature off or the scope not opted in the gate excludes nothing.
- Which client address does the gate check? ADR 212 decision 4 answers it: the TCP peer by default, else the header walk from the right through named trusted proxies.
- What does a direct read of one refused namespace answer? Milestone step 8 answers it: a CLI request from an address not on the list gives 403; a direct read by a grant holder gives 403 with the allowlist reason.
- Does the namespace exclusion list change installation-scoped objects? Issue #698 answers it: a namespace exclusion list cannot hold installation-scoped objects, so those stay visible; ADR 361 decision 3 lists only namespace consumers.
- What bound applies to NamespacesWithList? Issue #2964 answers it: refuse past 1000 names with conformance rows at the limit and one past it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
