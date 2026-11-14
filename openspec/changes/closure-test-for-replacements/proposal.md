Roadmap: `docs/roadmap.md` milestone 8, work item C3 (the claim model and the strict disjointness rules). Decision: ADR 368 decision 6.

## Why

**Persona: developer.** Two teams change one OpenTofu workspace. Team A replaces one instance (a new image, a new instance type). Team B updates resources that do not read that instance. Today every replacement is a conflict, so team B waits for the whole apply of team A. The developer needs the server to prove that the two plans are disjoint, so that both teams apply at the same time. A wrong "disjoint" loses state or applies a stale value, so the proof must fail closed.

The user path this change must make work:

1. **API.** Runner job A registers an apply claim with a plan that replaces `aws_instance.web`. Its plan also updates each instance that reads `aws_instance.web`.
2. **API.** Runner job B registers an apply claim with a plan that updates `aws_s3_bucket.logs`, which reads nothing in the closure of A. The server accepts both apply claims.
3. **API.** Runner job C registers an apply claim with a plan that reads `aws_instance.web.private_ip`. The server answers 409 with the code `claim.closure-unproven` or the overlap code, and names the colliding addresses.
4. **Console.** A review request with the plan of A and a review request with the plan of B show no conflict. A review request with the plan of C shows "Conflicts with another change" (ADR 299 decision 19).
5. **Docs page.** The developer reads `docs/explanation/plan-conflicts.md`: what the closure of a replacement is, why an unproven closure is a conflict, and the one command that shows the reason.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The closure of a replacement reaches the other plan. | 409 `claim.overlap`. The answer names the replaced address and the address of the other plan that reads it. Wait for the other apply claim to end, then plan again. | `GET /api/dustpan/v1/workspaces/{id}/claims` |
| The server cannot prove the closure (a reference through a data source, an unresolved module output, a provider that takes its settings from the replaced instance, a plan JSON that the server cannot read). | 409 `claim.closure-unproven`. The answer names the address and the reason, never a value of the plan. | `GET /api/dustpan/v1/workspaces/{id}/claims` |
| The plan JSON is above the size limit or not valid. | 422 `claim.plan-invalid`. Nothing is stored. Plan again with a supported OpenTofu version. | `tofu show -json plan.tfplan` |

Evidence for the claims above:

- No implementation of the `ConflictChecker` port exists on main. Proof: `rg -n 'PlanClosure' --type go . | grep -v pkg/reviewrequest/ports.go` gives no line.
- No parser of the OpenTofu plan JSON exists on main. Proof: `rg -ln '"prior_state"|tfjson|planjson' --type go pkg internal` gives no file.
- Every case of the conflict suite is pending. Proof: `pkg/reviewrequest/reviewrequesttest/reviewrequesttest.go:191` (`conflictCases` calls `pendingCases`).

## What Changes

- NEW type `reviewrequest.PlanGraph` (unexported fields, one constructor) that holds the operations, the read set and the dependency edges of one plan, and computes the downstream closure of each replacement itself.
- NEW strict decoder of the OpenTofu plan JSON (`plan-display` part, ADR 102 decision 2) into a `PlanGraph`.
- `reviewrequest.PlanClosure` gets the field `Graph PlanGraph`.
- NEW first adapter of the `ConflictChecker` port. It proves "disjoint" in two cases only: two plans of create and update operations on separate addresses with no shared read, and a replacement that passes the closure test of ADR 368 decision 6. Each other case is a conflict.
- The conformance case `AReplaceIsNeverDisjoint` is replaced by closure cases. The other never-disjoint cases become real tests of this adapter.
- The apply claim register route of two-disjoint-claims calls this checker for a claim with a replacement, in place of the fixed 409 `claim.closure-unproven`.
- A user docs page: `docs/explanation/plan-conflicts.md`.

Out of scope:

- The Pulumi CLI (ADR 302 decision 3: OpenTofu first).
- The merge of uploads (concurrent-applies-merge-core). The merge keeps its own refusal of a write-write and a stale read: a second layer that does not rely on this check.
- A relaxation of any other never-disjoint case.
- The approval floor of ADR 299 decision 10: a new replace still needs an approval.

Depends on:

- ADR 368 on main (pull request #3052)
- ADR 369 on main (pull request #3056)
- two-disjoint-claims (the register route and the apply claim record)

## Usability

Where the path still costs the person time or surprises them:

- A refusal names the address and the reason, so the developer sees why the server could not prove the closure.
- A module output that the plan does not resolve gives `claim.closure-unproven`, even when a person can see that it is safe. The docs page says so and says that the apply takes turns.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The port and its rule that a replacement is never proven disjoint. (`pkg/reviewrequest/ports.go:137 (ConflictChecker)`)
- The input of a check: operations, a flat closure and the count of unseen readers. (`pkg/reviewrequest/ports.go:120 (PlanClosure)`)
- The kinds of operation, with the two orders of a replace. (`pkg/reviewrequest/reviewrequest.go:137 (OperationKind)`)
- The approval floor of a replace. (`pkg/reviewrequest/reviewrequest.go:163 (OperationKind.Floor)`)
- The pending conflict cases, with `AReplaceIsNeverDisjoint`. (`pkg/reviewrequest/reviewrequesttest/reviewrequesttest.go:191 (conflictCases)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged: decisions 10, 19; PM details marked (PM)).

- Decisions that apply: 10 (current), 19 (changed), 41 (current).
- Changed by: ADR 328 extends decision 1; ADR 359 extends decisions 1, 15 and 25; ADR 368 changes decision 19. Chain to the end: ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 328, 359, 368.
- Quote: ""No conflict" holds only when the check proves the two closures disjoint (PM)."
- Quote: "At every level, a new delete, a new replace or any operation that the approved set does not hold stops the apply for a new approval with a fresh authentication."
- Quote: "A reader that the check cannot see counts as a conflict."
- Effect: The checker proves disjoint only what it can prove. The approval floor is not touched. An unseen reader stays a conflict.
- Conflict: none.

**ADR 368: Concurrent applies: apply claims, the server serial, fences and the closure test** (owner-tagged (all decisions)).

- Decisions that apply: 6 (current), 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The one `ConflictChecker` serves both the review requests of ADR 299 and the apply claims."
- Quote: "The closure is complete. When the check cannot prove an edge, the answer is a conflict."
- Quote: "The other cases stay never disjoint: provider changes, renames through `moved` blocks or aliases, data sources, deletes and changes of a parent resource."
- Effect: This change builds the four conditions of decision 6 in the one checker, and keeps each other case a conflict.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 3 (current).
- Changed by: ADR 368 extends decision 6. Chain to the end: ADR 368.
- Current rule: this ADR, with ADR 368.
- Quote: "Work that the server cannot prove disjoint takes turns."
- Quote: "OpenTofu is first."
- Effect: A replacement that the checker cannot prove disjoint takes turns. The decoder reads the OpenTofu plan JSON only.
- Conflict: none.

**ADR 102: Job plans and logs: parts, chunks, uploads and reads** (not owner-tagged).

- Decisions that apply: 2 (changed).
- Changed by: ADR 190 changes decisions 2, 7 and 8; ADR 194 extends decision 2; ADR 210 changes; ADR 220 changes decisions 3 and 4; ADR 299 changes decision 8; ADR 307 extends; ADR 353 extends decision 7. Chain to the end: ADR 190, ADR 194, ADR 210, ADR 220, ADR 299, ADR 307, ADR 353.
- Current rule: this ADR, with ADR 190, 194, 210, 220, 299, 307, 353.
- Quote: "`plan-display` is the readable JSON plan: `tofu show -json` or `pulumi preview --json`."
- Effect: The decoder reads the `plan-display` part. It never runs a binary and never reads the `plan-apply` part.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Each layer must stop the attack alone, without help from the others."
- Effect: The decoder, the checker and the register handler each refuse hostile input alone, each with its own hostile test.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a replacement count as an overlap of its address? Only when the closure test fails. ADR 368 decision 6 decides it.
- Which input gives the dependency edges? The `plan-display` part of the stored plan (ADR 102 decision 2): the references of the configuration, the `depends_on` of each resource, and the `dependencies` of each instance in the prior state. The runner already uploads it.
- Which other cases does the first checker prove disjoint? Only two plans of create and update on separate addresses with no shared read, and a replacement that passes the closure test. Each other case is a conflict. This is the fail-closed reading of ADR 299 decision 19 and ADR 368 decision 6.
- Is a reference through a module output an edge that the checker can prove? Only when the plan JSON resolves the output to resource addresses. Otherwise the closure is not complete and the answer is a conflict.
- Does a `create_before_destroy` replace differ from a delete-first replace? No, for the test. Both change the identity of the instance. The deposed object stays on the same address, so it is in the write set of the plan.
- Does the check depend on the order of the two plans? No. The check runs both ways, and one failure is a conflict. A test calls `Check(a, b)` and `Check(b, a)` for each case.
- Does this change the approval floor? No. ADR 299 decision 10 stays.
- Who answers the refusal? The register route of two-disjoint-claims, with the codes in the table above.
- Which credential does the e2e step use? The job token of each apply phase, which names the apply claim (ADR 369 decision 1).
- Does this need a new dependency? No. The decoder uses `encoding/json` with a size limit.

Drafted by the PM for milestone 8 from ADR 368 decision 6.
