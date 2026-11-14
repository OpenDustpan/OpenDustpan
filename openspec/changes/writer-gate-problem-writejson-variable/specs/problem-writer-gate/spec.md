## ADDED Requirements

### Requirement: narrow-writer

The server SHALL write each error body with a status of 400 or more only through the NEW WriteProblem method.

#### Scenario: NEW WriteProblem raw WriteJSON with 500 and map body
- **WHEN** a gated adapter calls the generic writer with status 500 and a map body
- **THEN** the gate fails and names the call site and the required NEW WriteProblem method

#### Scenario: unexported writer compile fail
- **WHEN** a gated adapter calls the unexported generic writer directly
- **THEN** the gated adapter fails to compile

### Requirement: problem-500-log

When the NEW WriteProblem method writes a 500, the server SHALL write one fault-log line.

#### Scenario: NEW WriteProblem writes 500
- **WHEN** the NEW WriteProblem method writes a 500 error body
- **THEN** the response carries a code field and one fault-log line is written

#### Scenario: migrated call sites use NEW WriteProblem or NEW WriteSuccess
- **WHEN** the migration of internal callers such as internal/api/objectcontext/objectcontext.go:241 completes
- **THEN** each of the 13 non-test WriteJSON call sites uses the NEW WriteProblem method or the NEW WriteSuccess method

### Requirement: success-guard

When the NEW WriteSuccess method receives a status of 400 or more or a body with a code field, the server SHALL fail closed through the NEW WriteProblem method.

#### Scenario: NEW WriteSuccess with error status
- **WHEN** the NEW WriteSuccess method receives a status of 400 or more at run time
- **THEN** the server writes a 500 problem body with code internal and one fault-log line

#### Scenario: NEW WriteSuccess with code field
- **WHEN** the NEW WriteSuccess method receives a body that holds a code field
- **THEN** the server fails closed through the NEW WriteProblem method with code internal

### Requirement: variable-status

While a file is in a gated adapter directory, the gate SHALL flag each WriteHeader call whose status argument is not a constant status literal.

#### Scenario: variable WriteHeader
- **WHEN** a gated adapter calls WriteHeader with a variable status
- **THEN** the gate fails and names the call site

#### Scenario: converted constant WriteHeader
- **WHEN** a gated adapter calls WriteHeader with int of a status constant outside the allowlist
- **THEN** the gate fails and names the call site

### Requirement: map-body

While a file is in a gated adapter directory, the gate SHALL flag each response encode of a map type or an anonymous struct type.

#### Scenario: map encode
- **WHEN** a gated adapter encodes a map or a named map type to the response
- **THEN** the gate fails and names the call site

#### Scenario: anonymous struct encode
- **WHEN** a gated adapter encodes an anonymous struct to the response
- **THEN** the gate fails and names the call site

### Requirement: body-type-detection

When BodyTypes reads an adapter directory, the gate SHALL report each named map type and each anonymous struct encode as a body type.

#### Scenario: BodyTypes sees a named map type
- **WHEN** BodyTypes reads a directory that holds a named map type used as a body
- **THEN** the returned set names that type

#### Scenario: BodyTypes sees an anonymous struct
- **WHEN** BodyTypes reads a directory that encodes an anonymous struct
- **THEN** the returned set flags that encode

### Requirement: pinned-exceptions

The gate SHALL compare the fault-log exception list by exact sorted-key set match.

#### Scenario: remove-plus-add swap
- **WHEN** the exception list swaps one entry for another with the same row count
- **THEN** the gate fails and names the added and removed keys
