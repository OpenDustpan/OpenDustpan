## ADDED Requirements

### Requirement: publisher-identity

The server SHALL keep the subject string on each release with the publisher principal ID beside it through PublishRelease, with an empty ID for callers with no principal record.

#### Scenario: Publish by named principal records ID
- **WHEN** a principal with the publish grant publishes a version
- **THEN** the release answers the publisher principal ID and the subject, and PublishRelease stores both

#### Scenario: Publish by bootstrap token
- **WHEN** a bootstrap token with no principal record publishes a version
- **THEN** the release answers an empty principal ID with the bootstrap subject

#### Scenario: Publish refused without grant
- **WHEN** a caller without the publish grant calls PublishRelease
- **THEN** the server answers 404 registry.not-found and writes the audit entry

### Requirement: key-adder-identity

The server SHALL keep the subject string on each key record with the adder principal ID beside it through AddKey, with an empty ID for callers with no principal record.

#### Scenario: Key add records adder
- **WHEN** a principal with the key grant adds a signing key
- **THEN** the key record answers the adder principal ID and the subject, and AddKey stores both

#### Scenario: Removed or recreated principal stays readable
- **WHEN** the publishing or adding principal is later renamed, removed, or recreated
- **THEN** the release and key record answer the stored subject, with an empty resolved name when the ID is empty or points to a removed principal and without adopting rows of a recreated principal with the same subject

### Requirement: mirror-host-without-header

Where the registry mirror serves, the server SHALL build mirror URLs only from the configured public URL base with the host folded to ASCII lowercase without reading the request Host header.

#### Scenario: Mirror answers from configured base
- **WHEN** OpenTofu requests the mirror path
- **THEN** the server answers the index with URLs built only from the configured public URL base with the host folded to ASCII lowercase

#### Scenario: Mirror disabled without public URL
- **WHEN** the public URL is unset
- **THEN** the server answers 404 registry.not-found

#### Scenario: Alias host does not change URLs
- **WHEN** a request arrives with a proxy, port, mixed-case, trailing-dot, or alias Host value
- **THEN** the server answers URLs built from the configured base and writes no audit entry for the host mismatch

#### Scenario: Startup refuses bad public URL host
- **WHEN** the server starts with a public URL whose host is empty or has a trailing dot
- **THEN** the server refuses to start and names the public URL setting
