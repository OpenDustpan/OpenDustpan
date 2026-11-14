## ADDED Requirements

### Requirement: per-section-data-schema

When a caller reads a context section, the server SHALL return the data member in the named schema of the requested section.

#### Scenario: Identity section
- **WHEN** a caller reads the identity section of a unit context
- **THEN** the data member validates against the identity data schema

#### Scenario: Context section
- **WHEN** a caller reads the context section of a unit context
- **THEN** the data member validates against the context data schema

#### Scenario: Readme section
- **WHEN** a caller reads the readme section of a unit context
- **THEN** the data member validates against the readme data schema

#### Scenario: Outputs section
- **WHEN** a caller reads the outputs section of a unit context
- **THEN** the data member validates against the outputs data schema

#### Scenario: Dependencies section
- **WHEN** a caller reads the dependencies section of a unit context
- **THEN** the data member validates against the dependencies data schema

#### Scenario: Changes section
- **WHEN** a caller reads the changes section of a unit context
- **THEN** the data member validates against the changes data schema

#### Scenario: Health section
- **WHEN** a caller reads the health section of a unit context
- **THEN** the data member validates against the health data schema

#### Scenario: Audit section
- **WHEN** a caller reads the audit section of a unit context
- **THEN** the data member validates against the audit data schema

#### Scenario: Several sections
- **WHEN** a caller reads several sections in one unit context read
- **THEN** each entry validates against the named schema of its own section name

#### Scenario: Default sections
- **WHEN** a caller reads a unit context with an empty section list
- **THEN** each returned entry validates against the named schema of its own section name

### Requirement: real-answer-array-shape

When the server answers a context read route, the server SHALL encode each empty list member as an empty array.

#### Scenario: Unit read validates
- **WHEN** a caller reads a unit context with an access token
- **THEN** each section data member validates against the named schema of that section

#### Scenario: Namespace read validates
- **WHEN** a caller reads a namespace context with an access token
- **THEN** each section data member validates against the named schema of that section

#### Scenario: Empty list section
- **WHEN** a section holds no entries
- **THEN** the answer holds an empty array and validates against the description

#### Scenario: Unset pointer member
- **WHEN** an optional pointer member holds no value
- **THEN** the answer omits the member and validates against the description

### Requirement: unknown-section-refusal

When a caller requests an unknown context section, the server SHALL answer the error code `context.unknown-section`.

#### Scenario: Unknown section
- **WHEN** a caller requests a section name outside the listed sections
- **THEN** the server answers context.unknown-section

### Requirement: denied-context-read

When a caller without the read grant reads a context object, the server SHALL refuse the request.

#### Scenario: Denied caller
- **WHEN** a caller without the read grant reads a unit context
- **THEN** the server refuses the read
