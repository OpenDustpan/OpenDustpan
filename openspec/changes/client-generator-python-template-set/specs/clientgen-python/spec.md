## ADDED Requirements

### Requirement: python-urllib-transport

Where Python generation is requested, the generator SHALL emit a Python client that uses urllib.request by default.

#### Scenario: default-transport
- **WHEN** generation runs with option httpx false
- **THEN** the client uses urllib.request and json with no httpx import

### Requirement: python-httpx-extra

When generator option httpx is true, the generator SHALL emit a Python client that uses httpx.

#### Scenario: httpx-transport
- **WHEN** generation runs with option httpx true
- **THEN** the client uses httpx and pyproject.toml lists the httpx extra

### Requirement: python-names

Where Python generation runs, the generator SHALL refuse a generated name that fails the Python identifier allowlist or reserved words.

#### Scenario: reserved-word
- **WHEN** a name equals class, None or match
- **THEN** generation refuses it and reports the rejected name

#### Scenario: hostile-names
- **WHEN** a name is empty, starts with a digit, holds non-ASCII or collides after mapping
- **THEN** generation refuses it and reports the rejected name

### Requirement: python-golden

Where Python generation runs, the generator SHALL emit output that matches the checked-in golden files.

#### Scenario: golden-match
- **WHEN** generation runs twice on the pinned API description
- **THEN** both outputs equal the golden files byte for byte

### Requirement: python-suite-transports

When the NEW Python adapter starts, the harness SHALL run the full conformance suite on each InitializeParams.Transport value.

#### Scenario: both-transports
- **WHEN** the transport is urllib and then httpx
- **THEN** the harness runs the full suite once per transport

#### Scenario: missing-interpreter
- **WHEN** the interpreter or httpx is absent
- **THEN** the harness fails hard and names the missing part

### Requirement: python-protocol-docs

Where the NEW Python adapter lands, the NEW protocol document SHALL define framing, id echo, exit behavior and body re-encoding.

#### Scenario: docs-first
- **WHEN** the NEW Python adapter lands
- **THEN** the protocol document lists framing, id echo, stdout use, exit behavior, ErrorResult.code meaning and body re-encoding

### Requirement: python-optional-presence

When an optional list or map is absent, the generator SHALL omit the member in the emitted Python.

#### Scenario: absent-versus-empty
- **WHEN** a list is absent in one case and empty in another
- **THEN** the output omits the first and emits an empty list for the second

### Requirement: python-transport-errors

When a transport call fails, the NEW Python adapter SHALL map timeout, TLS failure, non-JSON body and oversize body to one client-side error kind on both transports.

#### Scenario: error-mapping
- **WHEN** a call times out, fails TLS, or returns non-JSON or an oversize body
- **THEN** both transports return the same client-side error kind
