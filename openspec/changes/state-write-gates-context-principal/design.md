## Context

state.write gates evaluate context.principal.kind from the subject that principalSubjects.Subject builds. The subject maps every principal to person or service, so kind agent never reaches the gate engine.

## Decisions

1. **Stored agent kind with closed mapping.** person-delegates-dusty defines the stored kind identity.KindAgent with its creator, time, listing and principal API exposure. This change maps the stored kind only: principalSubjects.Subject maps KindPerson to PrincipalPerson, KindService and KindWorkload to PrincipalService, and KindAgent to PrincipalAgent through the NEW function newSubjectKind, which takes identity.Kind and returns one gate kind for each known input and an error for any other input. The principal kind is immutable after creation; a change of kind is refused by person-delegates-dusty.
2. **Schema baseline allows the agent value.** Allow the value agent in the check text of the schema baseline of each dialect in place, with no numbered migration. Before the first tag the baseline edit needs dev reset on existing dev databases with no data upgrade. Existing stored gate rows that test context.principal.kind == agent start to match for agent callers after the upgrade; the guide calls this out.

## Structure

The NEW unexported function newSubjectKind with its allowlist makes an unknown kind unrepresentable in PrincipalFacts: only its return value sets PrincipalFacts.Kind. For each known identity kind it returns one gate kind value. For any other value it returns an unknown-kind error.

## Risks

- A stored agent kind without a creator and time would be a hidden rule and break the grant audit trail.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Boundary check: decode and allowlist identity.Kind at the principal read, and fail closed on an unknown kind with the NEW error code gate.unknown-principal-kind. | Send a principal record with kind evil and show state.write denies with gate.unknown-principal-kind instead of falling back to person. |
| service | Service check: principalSubjects.Subject calls newSubjectKind, which returns one gate kind for each of the four known input kinds and returns an unknown-kind error for anything else. | Call newSubjectKind with an unknown kind and show it errors even when the adapter check is removed. |
| HTTP handler | Handler check: the tool API adapter propagates the unknown-kind error without copying an unknown string into PrincipalFacts.Kind and renders the tool shape for that error code. | Evaluate a gate with context.principal.kind == agent against an unknown-kind subject and show it matches neither person nor agent. |
