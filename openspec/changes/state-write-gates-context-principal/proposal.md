Issue: #2321.

## Why

**Persona: namespace administrator.** A namespace administrator tries to keep agents off sensitive stacks and workspaces with a state.write gate on context.principal.kind == agent, but the gate never matches because the server always sends person or service.

The user path this change must make work:

1. **Docs page.** Read docs/guides/state-write-gates.md and write a forbid gate with context.principal.kind == agent.
2. **API.** Store the gate with PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id} and list it back.
3. **CLI.** Run the Pulumi CLI against the server and watch the agent-gated state.write allow or deny by caller kind.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| state.write denied by an agent-only forbid gate when the caller is an agent | Gate denies with gate.denied and the text names the failing gate; run the list command to read the gate. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| gate with context.principal.kind == agent fails to parse | Gate text fails with gate.invalid and names the bad policy; run the set command to fix the gate text. | `PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id}` |
| state.write with a stored principal kind outside person, service, workload and agent | Call fails with the NEW error code gate.unknown-principal-kind and names no principal data; run the list command to confirm the gate that denied it. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |

Evidence for the claims above:

- gate.PrincipalAgent exists but only test code uses it outside its definition Proof: pkg/gate/facts.go:180:	PrincipalAgent   PrincipalKind = "agent" and rg -n -F gate.PrincipalAgent gives 1 match in pkg/gate/gatetest/run_engine.go:556
- The state guard starts at person and only maps service and workload Proof: pkg/dustpan/stateguard.go:340:	kind := gate.PrincipalPerson and pkg/dustpan/stateguard.go:341:	if p.Kind == identity.KindService || p.Kind == identity.KindWorkload {
- identity.Kind has person, service and workload and no agent kind in non-test code Proof: pkg/identity/identity.go:87 KindPerson, pkg/identity/identity.go:88 identity.KindService, pkg/identity/identity.go:89 identity.KindWorkload with 0 matches for principalSubjects.Subject in non-test code

## What Changes

- principalSubjects.Subject maps the stored kind identity.KindAgent to gate.PrincipalAgent and fails closed on unknown kinds
- GuardStateWrite carries kind agent into PrincipalFacts.Kind at state.write
- docs/guides/state-write-gates.md documents that the server sets agent

Out of scope:

- Definition of identity.KindAgent, creation, ceiling grants, delegation scope and revocation of agent principals, owned by person-delegates-dusty
- Change-order approvals and confirmation behavior
- MCP endpoint and stdio proxy work

Depends on:

- person-delegates-dusty
- `person-delegates-dusty` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The path still costs time because the administrator must learn that person and service share one gate value while agent has its own value.
- The guide surprises when an old gate that tested agent suddenly starts to match after the upgrade; the docs call this out.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- GuardStateWrite copies the subject kind into Change.Subject and PrincipalFacts.Kind (`pkg/gate/statewrite.go:65 (func Service.GuardStateWrite)`)
- PrincipalFacts carries the principal kind for gate evaluation (`pkg/gate/facts.go:322 (func record)`)
- The gate facts define person, service and agent kinds (`pkg/gate/facts.go:180 (func GroupsOf)`)
- principalSubjects.Subject builds the gate subject from the identity principal (`pkg/dustpan/stateguard.go:335 (func principalSubjects.Subject)`)
- identity.Kind defines person, service and workload (`pkg/identity/identity.go:82 (type identity.Kind)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The person who delegated may confirm (owner, 2026-11-07). Question 1. The person who delegated (Alice) may confirm a change that her own agent proposed, by default."
- Effect: The change keeps the delegation model where a person delegates to an agent; state.write facts distinguish the agent caller from the person so gates can tell them apart.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Should the fix remove gate.PrincipalAgent? No. The roadmap keeps the agent kind and the owner kept it; the fix maps the stored agent kind to gate.PrincipalAgent at state.write.
- Should this change create agent principals? No. Creation, ceiling grants, scope, revocation, creator, time and principal API exposure belong to person-delegates-dusty; this change only maps the stored kind at gate time.
- What error does an unknown principal kind give? Deny with the NEW code gate.unknown-principal-kind, never as person; an unknown kind is a data fault, not a gate denial.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
