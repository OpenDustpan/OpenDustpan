## ADDED Requirements

### Requirement: Cross-namespace token refusal

When a request for readable namespace B presents an access token of namespace A, the server SHALL refuse with error code token.outside-reach that names namespace A and the login command for namespace B.

#### Scenario: Pulumi token of A for B
- **WHEN** A Pulumi request for readable namespace B presents an access token of namespace A.
- **THEN** The server answers 403 with error code token.outside-reach, uses the word namespace, names namespace A, and names pulumi login for namespace B.

#### Scenario: OpenTofu token of A for B
- **WHEN** An OpenTofu request for readable namespace B presents an access token of namespace A.
- **THEN** The server answers 403 with error code token.outside-reach, uses the word namespace, names namespace A, and names tofu login for namespace B.

#### Scenario: Refusal says namespace
- **WHEN** The server refuses a cross-namespace token.
- **THEN** The 403 text uses the word namespace and never uses the word account.

### Requirement: Unknown namespace path

When a request names a namespace that does not exist or that the caller cannot read, the server SHALL answer 404 with no namespace name, no namespace ID, and no count.

#### Scenario: Unknown B with token of A
- **WHEN** A request names a namespace B that does not exist or that the caller cannot read.
- **THEN** The server answers 404 with no namespace name, no namespace ID, and no count.

### Requirement: Console path prefix

Where the hosted console labels a namespace, the server SHALL map the first path segment to the namespace name on one host.

#### Scenario: First segment routes
- **WHEN** A person opens a hosted account path on one host.
- **THEN** The console serves the namespace at https://app.opendustpan.com/<namespace name> and reserved first segments stay with the server.

### Requirement: No account object

Where the hosted console labels a namespace, the server SHALL keep no account object in the API.

#### Scenario: No account in description
- **WHEN** A test reads the API description.
- **THEN** The API description holds no account object and no account ID.

### Requirement: Invalid token on hosted host

When a request presents an invalid or expired token, the server SHALL refuse with error code token.invalid and no namespace name.

#### Scenario: Invalid token
- **WHEN** A request presents an invalid or expired token.
- **THEN** The server answers with error code token.invalid and names no namespace.

### Requirement: Account switching docs

When a person works in two namespaces on one host, the server docs SHALL list re-login and the per-namespace credential in the environment.

#### Scenario: Switch accounts
- **WHEN** A person works in two namespaces on one host.
- **THEN** The docs page lists re-login and the per-namespace credential in the environment for the Pulumi CLI and OpenTofu.
