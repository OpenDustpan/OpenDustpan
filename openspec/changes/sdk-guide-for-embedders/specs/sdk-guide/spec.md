## ADDED Requirements

### Requirement: Embed server from guide

When an embedder follows the guide, the server SHALL start from dustpan.NewServer with the NEW built-in constructors and serve GET /api/dustpan/v1/capabilities.

#### Scenario: Minimal program starts
- **WHEN** an embedder builds the minimal program from the guide
- **THEN** the server serves GET /api/dustpan/v1/capabilities with an access token and returns 200.

#### Scenario: Guide imports stay public
- **WHEN** an embedder lists the imports of the guide example
- **THEN** every import resolves to pkg/dustpan or a pkg port package.

### Requirement: Own adapter proves against suite

Where an embedder writes an own adapter, the server SHALL define the proof as a passing run of the named conformance suite of pkg for that port.

#### Scenario: Store conformance run passes
- **WHEN** an embedder runs the storetest suite against an own store adapter
- **THEN** the suite reports success for each port method.

#### Scenario: Blob conformance run passes
- **WHEN** an embedder runs the blobtest suite against an own blob adapter
- **THEN** the suite reports success for each port method.

#### Scenario: Secret conformance run passes
- **WHEN** an embedder runs the secrettest suite against an own secret adapter
- **THEN** the suite reports success for each port method.

#### Scenario: Conformance failure names method
- **WHEN** an own adapter misses a port method
- **THEN** the suite output names the unmet port method.
