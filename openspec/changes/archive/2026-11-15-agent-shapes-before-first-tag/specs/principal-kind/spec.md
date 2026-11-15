## ADDED Requirements

### Requirement: The kind agent is known or refused everywhere

Each reader of the kind of a principal SHALL know the kind `agent` or refuse it, and SHALL refuse an unknown kind.

#### Scenario: An unknown kind
- **WHEN** a request or a stored value names the kind `robot`
- **THEN** the parse answers `principal.invalid`, and no reader treats it as a person

#### Scenario: A create of an agent
- **WHEN** a site administrator posts a principal of the kind `agent` to `POST /api/server/principals`
- **THEN** the server answers 400 `principal.invalid` and stores no row

#### Scenario: An audit entry of an agent
- **WHEN** the server appends an entry whose principal kind is `agent`
- **THEN** the entry passes the validation of the principal kind

### Requirement: A state-write gate refuses an agent until it has a mapping

The gate subject source of a state write SHALL refuse a principal of the kind `agent` with a denial, and SHALL NOT give it the kind person, until a change maps the kind `agent` to the agent kind of the gate.

#### Scenario: An agent principal reaches a state-write gate
- **WHEN** a principal of the kind `agent` reaches the gate subject source of a state write
- **THEN** the source refuses it with a denial, and no gate sees it as a person
