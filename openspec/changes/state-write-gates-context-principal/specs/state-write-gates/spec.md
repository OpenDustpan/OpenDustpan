## ADDED Requirements

### Requirement: Map stored agent kind to gate fact

When the caller holds kind identity.KindAgent, the server SHALL set context.principal.kind to agent at state.write.

#### Scenario: Agent write matches agent gate
- **WHEN** a principal of kind identity.KindAgent makes a state.write call covered by a gate on context.principal.kind == agent
- **THEN** the server denies or allows exactly as the agent gate says and the evaluation records kind agent

### Requirement: Fail closed on unknown kind

If the principal kind is unknown, then the server SHALL deny the state.write call with the NEW error code gate.unknown-principal-kind.

#### Scenario: Unknown kind denied
- **WHEN** principalSubjects.Subject sees a kind value outside person, service, workload and agent
- **THEN** the server denies the state.write call with the NEW error code gate.unknown-principal-kind and never treats the caller as person

### Requirement: Keep other kinds unchanged

When the caller holds kind person, service or workload, the server SHALL map it to the same gate fact as on main.

#### Scenario: Person service workload mapping
- **WHEN** a principal of kind person, service or workload makes a state.write call
- **THEN** person maps to person and service and workload map to service

### Requirement: Read kind at time of call

When a state.write call arrives, the server SHALL read the stored principal kind and its enabled state at the time of the call.

#### Scenario: Disabled agent denied
- **WHEN** a disabled, deleted or revoked agent principal makes a state.write call
- **THEN** the server denies the state.write call and writes an audit entry with the actor and the reason

#### Scenario: Token uses current kind
- **WHEN** an access token caller makes a state.write call after the stored kind changed
- **THEN** the server evaluates the current stored kind, not the kind at token issue

### Requirement: Baseline allows the agent value

Where the schema baseline checks the principal kind, the server SHALL allow the value agent with no numbered migration.

#### Scenario: Existing agent gate starts to match
- **WHEN** a gate stored before the upgrade tests context.principal.kind == agent
- **THEN** an agent caller matches context.principal.kind == agent and a person caller does not

### Requirement: Same kind at other decision points

When an agent principal reaches an access limit or the change.propose decision point, the server SHALL use kind agent.

#### Scenario: Access limit sees agent
- **WHEN** an agent principal reaches an access limit or the change.propose decision point
- **THEN** the access-limit evaluation and the change.propose decision see kind agent for the same caller

### Requirement: Denial shape and audit

When the server denies state.write with the NEW error code gate.unknown-principal-kind, the server SHALL write an audit entry and answer the error in the shape of the called API.

#### Scenario: Unknown kind denial is audited and shaped
- **WHEN** a state.write call is denied with the NEW error code gate.unknown-principal-kind
- **THEN** the server writes an audit entry with the actor and the reason, answers the Pulumi shape with code and message, and answers the TFE shape with a JSON:API error
