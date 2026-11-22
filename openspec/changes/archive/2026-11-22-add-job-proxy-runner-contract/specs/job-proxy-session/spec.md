## ADDED Requirements

### Requirement: Proxy handoff token

When a runner asks for the proxy handoff token of a claim that it owns and that is live, the server SHALL issue one token of kind proxy that is good for one exchange and for 10 minutes.

#### Scenario: Runner gets a proxy handoff token
- **WHEN** a runner key with `runner:claim` on the pool asks for the proxy handoff token of its live claim
- **THEN** the server returns a token that starts with `dph_`, stores only its SHA-256, and a second request for a claim with no proxy session replaces the first token

#### Scenario: Runner asks for the claim of another runner
- **WHEN** a runner asks for the proxy handoff token of a claim that another runner owns
- **THEN** the server refuses with `claim-lost` and issues no token

### Requirement: Proxy session exchange

When the job proxy sends a valid proxy handoff token, a public key and the proof of the new key, the server SHALL open one proxy session of the claim and return the proxy assignment.

#### Scenario: Open a proxy session
- **WHEN** the job proxy exchanges an unused proxy handoff token of a live claim
- **THEN** the server marks the token used, stores a session of kind proxy, writes an audit entry with the job, the phase, the unit ID and the rules digest, and returns the rendered rules, the digest, the server host, the allowed ranges of the pool and the upstream URL

#### Scenario: Token of the other kind
- **WHEN** a launcher handoff token is sent to the proxy session path, or a proxy handoff token is sent to the launcher session path
- **THEN** the server answers as for an unknown token (401 `bad-proof` on the proxy path, 404 `not-found` on the launcher path), so that the answer does not name the kind of the token, opens no session, leaves the token unused and writes an audit entry

#### Scenario: Used or expired token
- **WHEN** the job proxy sends a proxy handoff token that is used or expired
- **THEN** the server refuses the exchange and opens no session

#### Scenario: Rules that cannot render
- **WHEN** a rule of the joined set uses a template that has no value before job start
- **THEN** the server refuses the open with the reason `rules-not-renderable` and the proxy gets no partial rules

### Requirement: Session ends with the claim

A proxy session SHALL authenticate nothing after its claim is no longer live, after a takeover, or after the runner is retired.

#### Scenario: Takeover of the claim
- **WHEN** a takeover replaces the claim of a proxy session and the old proxy sends a request
- **THEN** the server refuses with `claim-lost`

#### Scenario: Assignment of another claim
- **WHEN** a proxy session sends a request that names the operation of another claim
- **THEN** the server uses the claim of the session only and returns nothing of the other claim

### Requirement: Upstream credential only for a proxy session

The server SHALL give the credential of the upstream proxy of a pool only to a live proxy session of a claim in that pool, and SHALL write one audit entry of the kind reveal for each answer.

#### Scenario: Proxy session reads the credential
- **WHEN** a live proxy session of a pool with an upstream credential asks for it
- **THEN** the server returns the user name and the password and writes an audit entry that holds no credential

#### Scenario: Launcher session asks for the credential
- **WHEN** a launcher session sends a request to the upstream credential path
- **THEN** the server refuses with 403 and the reason `wrong-session-kind` and returns no credential

#### Scenario: Pool with no upstream proxy
- **WHEN** a proxy session of a pool with no credential asks for it
- **THEN** the server answers 404 with the reason `no-upstream`

### Requirement: Fixed error reasons

An answer or an audit entry of a proxy path SHALL NOT hold the upstream URL, the upstream credential or a secret.

#### Scenario: Unseal fails
- **WHEN** the server cannot unseal the credential
- **THEN** the answer holds a fixed reason and none of the URL, the credential or the sealed value

### Requirement: Launcher paths refuse a proxy session

Each launcher path SHALL refuse a session of kind proxy, and each proxy path SHALL refuse a session of kind launcher and a runner key.

#### Scenario: Proxy session on a launcher path
- **WHEN** a proxy session sends a request to the step credentials path
- **THEN** the server refuses with 403 and the reason `wrong-session-kind`, and no job token is made
