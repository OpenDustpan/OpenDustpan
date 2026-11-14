## ADDED Requirements

### Requirement: request-examples

Where an operation defines a JSON request body, the server SHALL include one request example for that body in the API description.

#### Scenario: read description
- **WHEN** client reads the API description
- **THEN** each operation with a JSON request body includes one request example

### Requirement: response-examples

Where an operation defines a JSON success answer with a body, the server SHALL include one response example for each such success status in the API description.

#### Scenario: read description
- **WHEN** client reads the API description
- **THEN** each JSON success status with a body includes one response example

### Requirement: no-example-without-body

Where an operation defines no JSON request body and no JSON success answer with a body, the server SHALL include no example for that operation.

#### Scenario: operations without a JSON body
- **WHEN** client reads the API description
- **THEN** operations with 204 no content or with no JSON body include no example

### Requirement: example-valid

While serving examples, the server SHALL serve only examples that validate against their schemas including required fields and enums.

#### Scenario: optional fields and branches
- **WHEN** client reads the API description
- **THEN** each example holds required fields plus at most one optional field and uses the first branch of each oneOf choice

### Requirement: invalid-example-fails-closed

If one example does not validate against its schema, then the server SHALL refuse to start.

#### Scenario: invalid fixture at start
- **WHEN** one example does not validate against its schema
- **THEN** the server refuses to start and writes a plain error that names the failing operation

### Requirement: no-secrets-in-examples

The server SHALL include only fake IDs and fake tokens from the example allowlist in API description examples.

#### Scenario: hostile fixture review
- **WHEN** one example holds a denied secret pattern in any nested field or array item
- **THEN** the validation rejects the fixture and the server refuses to start

### Requirement: unchanged-routes

While adding examples, the server SHALL leave paths, operations and authorization actions unchanged.

#### Scenario: read description after change
- **WHEN** client reads the API description
- **THEN** paths, operations and authorization actions match main

### Requirement: stable-bytes

While serving the description, the server SHALL serve byte-identical output across restarts for the same build.

#### Scenario: repeated reads
- **WHEN** client reads the API description twice across a restart
- **THEN** the two answers are byte-identical
