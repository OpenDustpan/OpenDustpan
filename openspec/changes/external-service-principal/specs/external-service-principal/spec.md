## ADDED Requirements

### Requirement: External service principal keyed by external ID

The server SHALL key a principal from an external identity system by the pair of its external source and its stable external ID, and SHALL never key it by its name.

#### Scenario: Rename keeps the principal
- **WHEN** the account `system-deploy` is renamed to `system-release` in its source
- **THEN** the principal keeps its ID, its grants and its classification

#### Scenario: A new account with an old name
- **WHEN** an account is deleted and a new account gets the same name
- **THEN** the new account is a new principal with no grant of the old one

### Requirement: A classification rule only classifies

A classification rule SHALL make a matching principal of kind `service`, with no console session, no MFA and no break-glass, and SHALL never give a grant.

#### Scenario: Glob match
- **WHEN** a glob rule `system-*` exists and the account `system-deploy` signs in through an exchange
- **THEN** the principal has the kind `service`, the classification names the rule, and the principal has no new grant

#### Scenario: Service principal and the console
- **WHEN** a service principal tries to sign in to the console
- **THEN** the server answers 403 `session.service-principal` and writes an audit entry

### Requirement: Glob grammar and exact bytes

The server SHALL refuse a glob that holds only `*`, and SHALL compare a glob with the external username byte for byte, with no case folding and no Unicode normalization.

#### Scenario: Match everything
- **WHEN** an administrator creates a glob rule `*`
- **THEN** the server answers 400 `principal.classification-invalid` and stores nothing

#### Scenario: Case
- **WHEN** a glob rule `system-*` exists and the username is `System-deploy`
- **THEN** the rule does not match

#### Scenario: Lookalike character
- **WHEN** a glob rule `system-*` exists and the username starts with a Cyrillic letter that looks like `s`
- **THEN** the rule does not match

### Requirement: Fail toward restriction on a rename

A rename out of the pattern SHALL NOT change the kind of a service principal. A rename into the pattern SHALL classify the principal, with an audit entry. Only an administrator SHALL make a service principal a person again, with a reason and an audit entry.

#### Scenario: Rename out
- **WHEN** a service principal `system-deploy` is renamed to `deploy`
- **THEN** it stays a service principal

#### Scenario: Rename in
- **WHEN** a person `ops-bot` is renamed to `system-ops`
- **THEN** it becomes a service principal, loses its console session, and an audit entry names the rule

#### Scenario: Person matched by mistake
- **WHEN** a glob rule matches a person account by mistake
- **THEN** the person loses the console session, and only an administrator can make it a person again

### Requirement: Existing principals at a rule write

A create or an update of a classification rule SHALL carry `existingPrincipals` with `refuse` (the default), `skip`, `confirm` with `expectedDigest`, or `apply`. The server SHALL refuse an unknown value.

#### Scenario: No value and no match
- **WHEN** a rule matches no existing principal and the request has no value
- **THEN** the server writes the rule

#### Scenario: No value and matches
- **WHEN** a rule would classify 3 existing principals and the request has no value
- **THEN** the server answers 409 `principal.reclassify-refused` with the count 3 and the preview route, and changes nothing

#### Scenario: Skip
- **WHEN** the request has `skip`
- **THEN** the server writes the rule, classifies no existing principal, and lists the 3 matches

#### Scenario: Confirm with the previewed digest
- **WHEN** the request has `confirm` and the digest of the current set
- **THEN** the server classifies the set and writes one audit entry with the set and its count

#### Scenario: Stale digest
- **WHEN** a principal joined the set after the preview and the request has `confirm` with the old digest
- **THEN** the server answers 409 `principal.reclassify-stale` and changes nothing

#### Scenario: Digest of another set
- **WHEN** the request has `confirm` with the digest of the preview of another rule
- **THEN** the server answers 409 `principal.reclassify-stale` and changes nothing

#### Scenario: Apply
- **WHEN** the request has `apply`
- **THEN** the server classifies each principal that matches at the write, and the audit entry records the set and its count

#### Scenario: Unknown value
- **WHEN** the request has `existingPrincipals: force`
- **THEN** the server answers 400 `principal.classification-invalid` and changes nothing

### Requirement: Preview

The preview route SHALL answer the principals that a rule would classify, their count and the digest of the set, and SHALL change nothing.

#### Scenario: Preview changes nothing
- **WHEN** an administrator previews a rule
- **THEN** the answer has the IDs, the count and the digest, and no principal and no rule changes

### Requirement: Automated sources never act as a person

The token exchange SHALL never give a person as the actor of a token from an automated source: a schedule, an API trigger, a pipeline that another pipeline started, or a token with no user. An unknown event source SHALL count as automated.

#### Scenario: Schedule of a person
- **WHEN** a schedule that a person owns sends a token with the user ID of that person
- **THEN** the actor is a workload or a service principal, never the person

### Requirement: The account is active at each exchange

The exchange SHALL read the external account in its source by its external ID, and SHALL issue no token when the account is inactive or the read fails.

#### Scenario: Inactive account
- **WHEN** the account is blocked in its source
- **THEN** the exchange answers 401 `trustrule.account-inactive` and issues no token
