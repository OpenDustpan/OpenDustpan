# 110. The Catalog: a gateway of declarative requests on the job model

Status: accepted · 2026-10-17. Supersedes parts of RFC 14 and RFC 13 (see "Superseded proposals"). Route paths superseded in part by ADR 210. Decision 9 and item 5 of the changes to the job model superseded by ADR 225

Changed by:

- ADR 305 extends: a resource of the Kubernetes operator that applies a
  blueprint is a Catalog request.
- ADR 210 changes: the route paths and the server: action names.
- ADR 218 extends: the open question on blueprints of other namespaces.
- ADR 222 supersedes decision 9: and item 5 of the changes to the job model.
- ADR 225 changes decision 9: also item 5 of the changes to the job model.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 308 extends: adoption of existing units, no move of a request, and git drift.

## Context

[RFC 14](../rfc/0014-blueprints.md) proposes blueprints: a program that a
platform team offers for self-service, with inputs that a consumer sets.
[RFC 12](../rfc/0012-api-edits-and-write-back.md) proposes write-back and the
rule that each value has one home. [RFC 13](../rfc/0013-policy-and-approvals.md)
proposes edit policy and approvals. ADRs 100 to 103 decide the shared job
model, and say that the Catalog starts jobs through `job.Service.Start`.

The project owner decided on 17 October 2026:

- The self-service gateway is the **Catalog**, at the API path
  `/api/catalog`. A Catalog request is a declarative JSON document. It is
  never tool code. YAML is a concern of the client.
- A request lives in git or in OpenDustpan alone. In git, OpenDustpan writes
  it back by a direct push or a review request, the team's CI applies it,
  and the state write closes the job. In OpenDustpan alone, a runner job
  applies it.
- A blueprint adds to a Pulumi component or an OpenTofu module. It never
  replaces it. A blueprint names the module or the component, its inputs
  (with types, defaults and validation in Expr), its choices, its policy and
  quota rules, and its delivery.
- Choices come from unit outputs. A dependent choice uses equality filters.
  The server alone computes the options, and checks the answers at submit,
  at approval and at apply.
- OpenDustpan approvals add to the approvals of the forge. They never gate a
  team that runs only in its own CI.
- Policy and quotas are SDK ports with default adapters. Expr inside
  blueprints stays built in.
- Each value has one home (RFC 12).
- Change freezes and break-glass come from the job model.
- The console form shows the same request as `request.json`, with the API
  call and the git path. The form is only one client of the API.

This ADR gives the frame. ADR 111 decides the request document and its git
layout. ADR 112 decides the blueprint document. ADR 113 decides the choice
engine. ADR 114 decides the policy port. ADR 115 decides quotas and
reservations. ADR 116 decides the request states and how they map to job
states. ADR 117 decides the actions, the audit entries, the routes and the
limits.

## Decision

1. **A domain package `pkg/catalog`.** It holds the request document, the
   blueprint document, the choice engine, the request states and the ports
   that the Catalog consumes. It imports `pkg/job`, `pkg/policy` and
   `pkg/quota`. It imports no tool package, no store adapter, no forge
   adapter and no transport. depguard holds it to the rules of the other
   domain packages. Two new tool-neutral packages hold the ports of the
   owner's decision: `pkg/policy` (ADR 114) and `pkg/quota` (ADR 115).
2. **One request, one unit.** A Catalog request asks for one unit from one
   blueprint, in the namespace of the request. The unit is a normal stack
   or workspace of its tool (ADR 13). A change to many units is many
   requests.
3. **The request is the desired state.** A request document says what the
   requester wants: a blueprint version, the answers to its inputs, and
   whether the unit is present or absent. It never says how to get there.
   The server compares the document with the last applied revision and
   picks the job: a create, an update or a destroy.
4. **A request has revisions.** Each submit of a document makes a new
   revision of the request, with a number that grows by 1. A revision
   records the document, the blueprint version, the checks, the policy
   decision, the reservation and the job. A request has at most one
   revision in flight (ADR 116 decision 3).
5. **Two homes.** The home of a request is where its document lives
   (RFC 12, "each value has one home"). The first submit fixes the home:
   - **`git`**: the document is a file in a repository of the team (ADR 111
     decision 7). A submit through the API starts a write-back job that
     commits the file, as a review request or a direct push. The team's CI
     asks the server to render the file at a commit (decision 8), and
     applies the result with its own credentials. The final state version
     closes the job (ADR 100 decision 15). OpenDustpan keeps no copy of the
     document as a source. The revision keeps the document that it
     delivered as a record only.
   - **`opendustpan`**: the document lives in OpenDustpan only. A submit
     starts a runner job. The server renders the files of the tool once, at
     the start of the job, from the revision of the job, and stores them as
     a rendered snapshot (change 3 below). Each phase runs that snapshot.

   A request never has both homes. A move from one home to the other is an
   open question.
6. **The Catalog makes the unit before its first job.** A job needs a unit
   with a stable unit ID (ADR 100 decision 2). So a create revision first
   makes an empty unit through the `catalog.UnitProvisioner` port: a Pulumi
   stack or an OpenTofu workspace, with the name that the blueprint computes
   (ADR 112 decision 9). The internal wiring of the server implements the
   port with the tool package, so no tool package imports `catalog`. The
   make is idempotent by the request ID. A create through the API makes the
   unit in the start operation (ADR 116 decision 5). A create from git has
   no start operation: the render makes the unit in the store transaction
   that stores the revision, through the port `catalog.UnitTx`. A `UnitTx`
   is the provisioner on the open transaction of the store, as `quota.Tx`
   is the counter on it: the wiring gives the SQL store a constructor that
   takes the transaction of the store and returns a `UnitTx`, and
   `Store.Submit` calls `UnitTx.Provision` for the provision of the
   submission. So the revision, its reservation and the empty unit commit
   together or not at all (ADR 116 decision 9). The suite
   `catalogtest.RunUnitTx` states the rules: a rollback keeps no unit, a
   second call for one request returns the same unit, and a name that
   another request owns gives `catalog.ErrUnitName`. A create
   revision that ends `rejected`, `canceled`, `failed` or `expired` before
   any state version deletes the empty unit again through the same port. A
   create that a revision from git supersedes keeps its unit, because the
   new revision takes it over.
7. **The Catalog starts jobs through `job.Service` only.** A revision gives
   one `job.StartRequest`. The idempotency key is `<request ID>/<revision>`,
   so a retry of the start gives one job. The Catalog never writes to the job
   store. The job keeps every rule of ADRs 100 to 103: one change at a time
   on a unit, freezes, break-glass, gates, agent modes and the audit of the
   job.
8. **Render is an API call.** `catalog.Renderer` is a port with one adapter
   for each tool, in the internal wiring of the server. It turns a revision
   and its blueprint version into the files of the tool: an OpenTofu root
   module of one module call and its `.tfvars.json` file, or a Pulumi YAML
   program of one component resource and its stack settings. The render
   gives no secret value. Secrets come from the profile of the blueprint at
   run time (RFC 5). Two renders of one revision give the same bytes.
   - **Answers are data.** The render writes each answer and each platform
     value with a JSON encoder into the variables file or the stack
     settings, and never into the text of the root module or of the Pulumi
     YAML program. The program text names only the keys of the blueprint,
     which the publish checks (ADR 112 decision 5). So no answer can change
     the program that the tool runs.
   - A runner job gets the files as a rendered snapshot that the server
     makes at the start of the job (change 3 below), never from a render at
     the claim of a phase.
   - The team's CI calls the render route (ADR 117) with the repository,
     the commit and the path of the request file. The server reads
     `request.json` at that commit from the forge itself, through the port
     `catalog.RequestFiles`, and never takes a document from the caller. It
     checks the path (ADR 111 decision 7) and the document at the point
     `apply` (ADR 113 decision 8), and returns the files. A CLI command
     wraps the call. So the server alone checks the answers also when the
     team's CI applies, and a render renders only what is in git.
   - A render needs `catalog:render` and the action of its operation:
     `catalog:request` on the blueprint for a create, `unit:edit-inputs` on
     the unit for an update, and `unit:request-destroy` on the unit for a
     destroy (ADR 117 decision 2). So a token of the CI can never do more
     through a render than a submit of the same document can.

   A render never writes the files to git. The rendered files are output, not
   a second home.
9. **Approvals add to the forge.** The gates of a Catalog job come from the
   job decision (ADR 103 decision 1), which reads the Catalog origin of the
   job spec (ADR 116 decision 6). The requester never counts as an
   approver of its own request. A write-back job waits at the gate before
   its commit. After the commit, the review request still needs the approvals of
   the forge. A change that a team makes to a request file in git and
   applies in its own CI has no job. The render checks it, and no
   OpenDustpan approval gates it (ADR 116 decision 9).
10. **Ports with conformance first.** `catalog.Store` and
    `catalog.BlueprintStore` get the suite `pkg/catalog/catalogtest`,
    `policy.Engine` gets `pkg/policy/policytest`, and `quota.Counter` gets
    `pkg/quota/quotatest`, before any adapter. A reference adapter in
    memory, in the tests of each suite package, runs its suite.
11. **Terms.** The design uses the terms of `GLOSSARY.md`: Catalog, Catalog
    request and Blueprint. It adds these terms (see "Shared-file changes
    requested" in the pull request): **revision** of a Catalog request,
    **input**, **answer**, **choice**, **option**, **home** of a request,
    **render**, **quota**, **reservation** and **recheck point**.

## Changes requested in the job model

The job model (ADRs 100 to 103) is a design that no tag shipped (ADR 73).
The Catalog needs these five changes. Each one adds to the job model and
changes none of its rules:

1. **A Catalog origin on a job.** `job.StartRequest` and `job.Spec` get a
   field `Catalog *job.CatalogRef`: the request ID, the revision number and
   the Catalog action that the revision needed. `job.StartRequirements`
   then returns that Catalog action on its object instead of `unit:run-*`
   or `unit:write-back`. A consumer of a blueprint has `catalog:request` on
   the blueprint, not `unit:run-apply` on the unit (ADR 117 decision 2).
   Only the Catalog sets the field: the job REST API refuses it. The
   `job.Decider` adapter reads the field in `job.DecisionInput.Spec`, and
   so gets the Catalog decision of the revision with no other field.
2. **A precondition port.** `job.Service` calls a new port
   `job.Precondition` at two points: before it records an approval, and
   before the dispatch of a phase that changes. The Catalog implements it
   with its recheck (ADR 113 decision 8). The contract:
   - A failed check applies `rejected`, with the reason of the check.
   - A check that cannot run (a policy or a quota rule that fails to run,
     an engine that does not answer, a source that does not answer)
     returns an error. With the error, the job model records no approval
     and dispatches nothing. It leaves the job as it is, and the reconcile
     schedule tries again.
   - The timeout of the gate or of the queue still applies. So a job whose
     check keeps failing to run ends `expired` at that time, as any other
     job that waits.
3. **A rendered snapshot for a runner job.** A runner job of a request in
   OpenDustpan has no code location in git. ADR 135 lets a runner get code
   only as a source snapshot that the server made and that the launcher
   checks by its hash, and never from the forge. The Catalog keeps that
   rule with a snapshot of its own kind:
   - At the start of the job, the Catalog renders the files of the revision
     with `catalog.Renderer`, once, and stores them in the blob store as a
     **rendered snapshot**. The job spec records the SHA-256 of the
     snapshot. `job.Location` gets the kind `catalog`, which names the
     snapshot by that hash.
   - The launcher reads the rendered snapshot through its session, as it
     reads a source snapshot, and checks the SHA-256 and the paths of each
     file (ADR 135 decision 4) before it writes a file. So each phase runs
     the same bytes, and no claim renders again.
   - A module in a repository of a forge (`catalog.Module` with a
     connection, a repository and a revision) gets a `source.fetch`
     snapshot of its commit, started in the same transaction as the job.
     The rendered root module references the module by a local path into
     that snapshot, never by a forge address. So the runner never connects
     to the forge, also for the module.
   - A module from a registry and a Pulumi component keep their source
     address and exact version: the tool gets them from the registry, as
     for any other job.

   ADR 135 defines one snapshot kind, a git pack. A rendered snapshot is a
   second kind with no git pack (see "Open questions").
4. **An edit on a missing file creates it.** A `job.FileEdit` on a file
   that does not exist at the base commit makes the file. The Catalog
   planner gives `job.ValidateEdits` an editable predicate that allows
   exactly the request file of the revision, `GitPath(ref)`
   (`catalog.EditableRequestFile`), and no other file. The CI configuration
   rule of ADR 101 decision 4 still applies to the path.
5. **Several approval gates on one phase.** `job.Decision.Gates` becomes
   `map[PhaseKind][]Gate`, and `job.PolicyGates` appends each gate of the
   kind. The server's `job.Decider` adapter appends the gates of
   `catalog.GateFor` to the gates of the `job.start` decision, and never
   merges two gates. A `jobtest` rule checks that a phase with two policy
   gates dispatches only when both are open. Approvals across the gates of
   one phase must come from distinct people: one approver who is in two
   required groups passes one gate, not both. This change replaces the rule
   of one gate for each phase kind (ADR 103 decision 1). This pull request
   changes no code in `pkg/job`.

The Catalog needs no new field in `job.DecisionInput` (item 1 covers it)
and no batch ID in version 1 (ADR 112 "Open questions").

## Superseded proposals

| RFC | Proposal | Superseded by | What changes |
| --- | --- | --- | --- |
| 14 | "Input schema" in JSON Schema | ADR 112 decisions 4 to 6 | Inputs have a small set of types, defaults and validation in Expr. No JSON Schema. |
| 14 | "Validation", step 3: custom checks on a runner | ADR 112 decision 7, ADR 115 | Custom checks are Expr rules in the server. A runner step before the plan stays possible as a custom step of the blueprint. |
| 14 | "Rendering": no rendered values in git, workstation runs | Decision 8 | The render is an API call that a runner and the team's CI make. |
| 13 | One edit has one place for approval | Decision 9 | ADR 103 already records this. OpenDustpan approvals add to the forge. |
| 13 | "Policy engines" are to be decided | ADR 114 | Four default adapters of one port. |

## Consequences

- A platform team offers a module or a component that it already has. The
  module keeps its own variable validation, and the tool still runs it.
- A consumer needs one action on the blueprint to ask for a unit, and needs
  no action that runs a tool.
- A team that keeps its requests in git keeps git as the record, and its CI
  applies them. The server still checks each answer at the render.
- The CI of a team with requests in git must reach the server at the
  render. A CI with no network path to the server cannot apply a request.
- An empty unit exists from the first submit of a create. The inventory
  shows it with no state version until the first job applies.
- The job model gets four requested changes. Until they land, a Catalog
  adapter cannot start a job.

## Open questions

- ADR 135 defines a source snapshot as a git pack of one commit. A rendered
  snapshot (change 3 of "Changes requested in the job model") has no git
  pack, so it needs an RFC or an amendment of ADR 135: its key, its hash,
  the checks of the launcher on a set of files, its retention, and the
  local path of a module snapshot in the work directory.

- Can a request move from one home to the other? A move is a write of the
  file and a change of the record in one step, and the forge cannot join
  the transaction of the store.
- Can a blueprint serve requests of other namespaces? Version 1 limits a
  blueprint to its own namespace, as ADR 160 assumes.
- Can a Catalog request attach a unit that exists, which no blueprint made
  (RFC 14 "Gaps")? The one-home check of ADR 112 decision 8 then must read
  the config of the unit first.
