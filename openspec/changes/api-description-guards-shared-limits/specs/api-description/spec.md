## ADDED Requirements

### Requirement: handler-guard

The server SHALL list each route-handler field in the API description.

#### Scenario: wrapper handler without row
- **WHEN** a named handler type that implements the handler interface is added without an API description row
- **THEN** the guard test fails naming the missing field

#### Scenario: pointer receiver handler without row
- **WHEN** a handler type that implements the handler interface only through a pointer receiver is added without a row
- **THEN** the guard test fails naming the missing field

#### Scenario: non-route field excluded
- **WHEN** an exclusion-listed handler-typed field serves no route
- **THEN** the guard test passes without an API description row for that field

### Requirement: profiles-single-declaration

While starting with a nil profiles handler, the server SHALL fail startup before serving traffic.

#### Scenario: single profiles field
- **WHEN** the profiles handler is declared once in neutralHandlers
- **THEN** the guard passes with one profiles handler and wiring reads the kept field

#### Scenario: nil profiles handler
- **WHEN** the kept profiles handler is nil at startup
- **THEN** server startup fails before serving traffic
