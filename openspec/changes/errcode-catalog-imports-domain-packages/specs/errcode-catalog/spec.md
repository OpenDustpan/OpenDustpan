## ADDED Requirements

### Requirement: catalog-has-no-domain-imports

Where the catalog builds an answer, the catalog SHALL import no domain package.

#### Scenario: Direct banned import fails the guard
- **WHEN** internal/errcode imports pkg/gate, pkg/identity, pkg/ipallow, or internal/sessionauth
- **THEN** the import guard test refuses the package

### Requirement: forbidden-answers-unchanged

When a Forbidden cause reaches the adapter mapping, the server SHALL return the same answer bytes as on main.

#### Scenario: Gate refusal keeps bytes
- **WHEN** a gate refusal reaches the adapter mapping
- **THEN** the answer bytes equal the bytes on main

#### Scenario: Identity refusal keeps bytes
- **WHEN** an identity refusal reaches the adapter mapping
- **THEN** the answer bytes equal the bytes on main

#### Scenario: IP allow refusal keeps bytes
- **WHEN** an ipallow refusal reaches the adapter mapping
- **THEN** the answer bytes equal the bytes on main

#### Scenario: Session refusal keeps bytes
- **WHEN** a sessionauth refusal reaches the adapter mapping
- **THEN** the answer bytes equal the bytes on main

#### Scenario: Wrapped known error keeps bytes
- **WHEN** a wrapped gate, identity, ipallow, or sessionauth error reaches the adapter mapping
- **THEN** the answer bytes equal the bytes on main

### Requirement: match-order-wins

When an error matches two domain types, the server SHALL apply the first type in the order gate, identity, ipallow, sessionauth.

#### Scenario: Two matched types use first match
- **WHEN** an error matches two domain types with gate first in the order
- **THEN** the server returns the gate answer bytes from main

### Requirement: unknown-forbidden-fallback

When a domain error with no matched type reaches the adapter mapping, the server SHALL return the generic Forbidden answer.

#### Scenario: Unmatched error falls back
- **WHEN** a domain error with no matched type reaches the adapter mapping
- **THEN** the server returns the generic Forbidden answer with no input text

### Requirement: invalid-plain-fields-refused

When errcode.New receives an unknown code or missing values, the catalog SHALL refuse the answer.

#### Scenario: Unknown code refused
- **WHEN** errcode.New receives an unknown code
- **THEN** the catalog refuses the answer and returns an error

#### Scenario: Missing values refused
- **WHEN** errcode.New receives a code with missing values
- **THEN** the catalog refuses the answer and returns an error
