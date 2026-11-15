Issue: #939.

## Why

**Persona: operator.** An Operator runs the hosted service and must offer Dusty without surprise bills, unclear data handling, or poor tool quality. The owner cannot pick the hosted default model until research compares the options with sources, so this change writes that note and changes no server behavior.

The user path this change must make work:

1. **Docs page.** Read the research note and compare the three options with sources.
2. **Docs page.** Check the review date and the decision rule. After 2027-05-14 the note is stale, so use a tenant-brings connection until the refresh.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Research note page not found | The note page is missing or its link broke. Check the docs links. | `make docs-lint` |
| Hosted default model undecided | The owner has not decided a hosted default. Dusty stays off until an administrator connects a model. Read the connection rules in the note. | `make docs-lint` |

Evidence for the claims above:

- Issue 939 asks for a research note with sources and a recommendation on the hosted default model. Proof: #939
- The milestone scope for Dusty design is ports and conformance suites in pkg with no adapter yet. Proof: Milestone 10 scope: The design of ADR 120 to 127

## What Changes

- Add the new file docs/research/dusty-hosted-default-model.md. It compares a hosted provider account, a tenant-brings connection, and no default on data handling and region, per-tenant cost control, and quality on the Dusty tool set, with review date 2027-05-14 and quality marked untested.

Out of scope:

- Any server code, route, setting, or connection document change
- Any choice of vendor, model name, price, or region beyond sourced research
- Legal advice on provider terms
- Milestone 10 delegation, run, tool, MCP, and audit work

## Usability

Where the path still costs the person time or surprises them:

- The note costs owner time where vendor pages conflict, so each cell names its source and access date.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests that plain connections default to off. (`pkg/egress/plainconn/plainconn_test.go:57 (TestAllowPlainConnectionsDefaultsOff)`)
- Main requires the backup target to come only from the option. (`pkg/dustpan/dustpan_test.go:1268 (TestBackupTargetComesOnlyFromTheOption)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decisions that apply: 3 (changed), 4 (current).
- Changed by: ADR 128 changes decision 22; ADR 129 changes decisions 3, 7 and 20; ADR 129 extends; ADR 210 changes; ADR 297 changes decisions 13 and 14; ADR 311 extends; ADR 314 extends; ADR 354 changes decisions 13, 14, 15 and 16. Chain to the end: ADR 128, ADR 129, ADR 210, ADR 297, ADR 311, ADR 314, ADR 354, ADR 190, ADR 222, ADR 227, ADR 286, ADR 247, ADR 301, ADR 333, ADR 357, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354.
- Quote: "The hosted service never turns it on silently. Onboarding offers Dusty to an account administrator. The administrator turns it on, or leaves it off. A later change of the hosted default needs a new ADR."
- Effect: The note keeps hosted onboarding opt-in and leaves a hosted default to a later ADR.
- Conflict: none.

**ADR 129: The operator sets every field of the model connection** (not owner-tagged).

- Decisions that apply: 1 (current), 34 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "No default model, no default provider, no preset. The server fills no field of a connection with a value for a provider."
- Quote: "A managed connection can come later, with no break. OpenDustpan Cloud may later offer a connection that the service runs."
- Effect: The note proposes no server default and treats a hosted offer as a later managed connection document.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which default model option does the hosted service adopt for Dusty?
   - Options: Hosted provider account as opt-in managed connection; Tenant-brings connection only; No default and no hosted offer.
   - Provisional decision (owner review 14 Nov): Tenant-brings connection only until sourced quality and cost runs exist.
   - Reason: No sourced quality runs exist because no adapter exists, so the safe position is tenant-brings. Benchmark: AWS Bedrock uses opt-in model access, per-account quotas and region-pinned endpoints.
   - Persona impact: No persona loses work: Dusty stays off until an administrator turns it on. Escape hatch: A tenant connects its own model and keeps Dusty off until then.

Questions considered and answered:

- Can the server ship a built-in default model? ADR 129 decision 1 answers it: no default model, provider or preset.
- Can the hosted service turn Dusty on silently? ADR 120 decision 4 answers it: the administrator turns it on, and a later default needs a new ADR.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
