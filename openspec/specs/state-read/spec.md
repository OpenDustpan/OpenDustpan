# state-read Specification

## Purpose
Define how the server reads one state version of a stack or workspace with each secret value replaced by a marker. The read needs the unit:read grant on the unit, checks the version and the cursor at the boundary, and refuses a read that is too large, too deep, over its work budget or past its deadline.

## Requirements

### Requirement: ReadMasked

When a caller with unit:read reads one state version through ReadMasked, the server SHALL answer the marker paths of the version, with each secret value replaced by a marker.

#### Scenario: ReadMasked masked values
- **WHEN** caller with unit:read reads one pinned version through ReadMasked
- **THEN** server answers the marker paths and the markers, and no plain secret value

#### Scenario: ReadMasked OpenTofu sensitive marks
- **WHEN** caller with unit:read reads one OpenTofu version through ReadMasked
- **THEN** server masks each sensitive output, each sensitive_attributes path and each sensitive_values path

#### Scenario: OpenTofu version with no kept marks
- **WHEN** the version has no kept marks and an instance names a whole value in sensitive_attributes
- **THEN** server answers that value as one marker with no marker for a key below it, and each other attribute value as a marker of its own

### Requirement: value paths and pages

When the server answers a masked read, the server SHALL key each marker by its JSON Pointer value path for that version.

#### Scenario: paged markers
- **WHEN** state holds more than 200 markers
- **THEN** server answers 200 markers per page with an opaque cursor bound to unit, version and caller

#### Scenario: cursor reuse refused
- **WHEN** caller reuses a cursor on another unit, another version or after 10 minutes
- **THEN** server refuses the page with 400

#### Scenario: cursor too long
- **WHEN** caller sends a cursor above the length cap
- **THEN** server refuses the page with 400 and reads no state

### Requirement: label marker schema

The API description SHALL declare the marker of a masked value as its own named schema, and SHALL NOT change the schema of any other route.

#### Scenario: other routes keep their schema
- **WHEN** the API description is regenerated with the NEW route
- **THEN** the schema of each route that does not use the marker is the same as before

### Requirement: deny without grant

If a caller lacks the required action on the unit, then the server SHALL deny the read with 403.

#### Scenario: sibling grant denied
- **WHEN** caller holds unit:read on another unit with no grant on this unit
- **THEN** server answers 403 with the code `unit.forbidden` and no secret bytes

### Requirement: bounds and failures

If a version is above 5 MB, then the server SHALL refuse the read with 413 and no content, and the server SHALL refuse it before it holds the content of the version. If a read passes the timeout of 30 seconds, then the server SHALL answer 504 with no bytes.

#### Scenario: version above 5 MB
- **WHEN** a read names a version that is larger than 5 MB
- **THEN** server answers 413 before it loads the version content, and no content leaves

#### Scenario: read passes the deadline
- **WHEN** the read runs past its 30 second deadline
- **THEN** server answers 504 with the code `state.read-timeout` and no bytes

### Requirement: refusal limiter

If a caller causes repeated refusals on the read route, then the server SHALL count them with the denial limiter of the server configuration, as the other adapters do. The answer of each refusal keeps its own status. The reveal route of the change `39-reveal-state-value` reuses this limiter.

#### Scenario: repeated refusals
- **WHEN** one caller sends repeated reads that the server refuses
- **THEN** the limiter counts each refusal, writes one audit entry for each window with the count, and the answer of each refusal keeps its own status

#### Scenario: over size refused
- **WHEN** version is above 5 MB
- **THEN** server refuses with 413 and no content

### Requirement: bounded depth for one read

The server SHALL refuse a state that nests deeper than 100 levels with 413 and the code `stack.state-too-complex` (Pulumi) or `state-version.too-complex` (OpenTofu), before it builds the path of any marker. The refusal goes to the denial limiter.

#### Scenario: deep nesting under the size limit
- **WHEN** a version of less than 5 MB has a nesting depth above 100
- **THEN** server refuses with 413 and the code of the tool, and builds no path

### Requirement: bounded work budget for one read

The server SHALL charge each value of the state the length of its value path plus a fixed cost for its marker, against one budget of 128 MiB for the whole read, before it builds any path. A state over the budget SHALL be refused with 413 and the code of the tool. The server SHALL NOT add a second bound for the count of markers or the bytes of paths: a state inside the budget is read in pages.

#### Scenario: value paths over the work budget
- **WHEN** a version of less than 5 MB holds values whose paths need more than the work budget, for example a key of 1 MiB above 200 values
- **THEN** server refuses with 413 and the code of the tool, and builds no path

#### Scenario: many markers inside the budget
- **WHEN** a version of less than 5 MB holds more than 10000 markers with short paths
- **THEN** server answers 200 and holds the markers in pages of 200

### Requirement: deadline inside the masking

The server SHALL check the deadline of the request after the parse, inside the masking of each instance and after the walk, and SHALL answer 504 with the code `state.read-timeout` and no bytes when it has passed. The duplicate pass of OpenTofu works from an index of the masked secrets, so its work does not grow with the product of the values and the secrets.

#### Scenario: deadline inside the masking
- **WHEN** the deadline passes while the masking of an instance runs
- **THEN** server answers 504 with the code `state.read-timeout` and no bytes

#### Scenario: deadline after the walk
- **WHEN** the walk of the state ends after the 30 second deadline
- **THEN** server answers 504 with the code `state.read-timeout` and no bytes

#### Scenario: deadline after the parse
- **WHEN** the deadline passes after the parse of the state
- **THEN** server answers 504 with the code `state.read-timeout` and no bytes

### Requirement: a shape that the server cannot read fails closed

If the server cannot read the shape of a version, for example a client-encrypted state, a `resources` value that is not an array, or an entry with no type or name, then the server SHALL refuse the read with 500 and no content. The server SHALL NOT answer 200 with fewer markers.

#### Scenario: unknown shape
- **WHEN** a version holds a client-encrypted envelope or a `resources` value that is not an array
- **THEN** server answers 500 with no content, and a reader cannot mistake the answer for "no secrets"
