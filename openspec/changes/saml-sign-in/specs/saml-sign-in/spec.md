## ADDED Requirements

### Requirement: SAML provider settings

The server SHALL accept a sign-in provider in the mode `saml` only with an `https:` metadata URL or an uploaded metadata document, a subject rule, and no SHA-1 algorithm, and SHALL show the effective value of each SAML setting in the provider answer.

#### Scenario: Effective defaults
- **WHEN** an administrator creates a SAML provider with only a metadata URL
- **THEN** the provider answer shows `allowIdpStarted` false, `encryptedAssertions` false, the persistent `NameID` subject, a maximum assertion age of 300 seconds and a clock skew of 120 seconds.

#### Scenario: SHA-1 refused
- **WHEN** an administrator sends a SAML setting that names a SHA-1 signature or digest algorithm
- **THEN** the server gives 400 and stores no change.

### Requirement: Response checks as if the library failed

The server SHALL refuse a SAML response unless the whole document has exactly one assertion, the signature has exactly one reference whose URI is the ID of the signed element, the algorithms are SHA-256 or stronger, and the destination, recipient, audience, issuer, request ID and times match, also when the signature library accepts the response.

#### Scenario: Two assertions
- **WHEN** a response holds one signed and one unsigned assertion
- **THEN** the server gives 400 with the text "sign-in failed: start the sign-in again" and opens no session.

#### Scenario: Signature wrapping
- **WHEN** a response moves a copy of the signed element to another place in the document and changes the original
- **THEN** the server gives 400 and opens no session.

#### Scenario: Library that accepts everything
- **WHEN** a test replaces the signature verifier with one that accepts every document and sends a response with a wrong audience
- **THEN** the adapter refuses the response.

### Requirement: XML limits

The server SHALL refuse a SAML response body above 256 KiB, a compressed value, a DTD, an entity, a depth above 64 and more than 20,000 XML tokens, before the signature check.

#### Scenario: Billion laughs
- **WHEN** a response holds a DTD with nested entities
- **THEN** the server gives 400, does not expand the entities, and opens no session.

#### Scenario: Depth 65
- **WHEN** a response has elements nested 65 deep
- **THEN** the server gives 400 and opens no session.

### Requirement: Assertion replay

The server SHALL accept each assertion ID once for each provider, SHALL keep it until its `NotOnOrAfter`, and SHALL refuse an assertion with no `NotOnOrAfter` or with a `NotOnOrAfter` more than 1 hour after receipt.

#### Scenario: Replayed response
- **WHEN** a browser posts a response whose assertion ID a sign-in used before
- **THEN** the server gives 400, opens no session, and writes one audit entry with the reason "sign-in value was used before".

### Requirement: Same-origin callback

The server SHALL answer the identity provider's POST to the ACS route with a page that posts the same fields to the callback route of the server, and SHALL refuse a callback POST whose `Origin` is not the origin of the server.

#### Scenario: Cross-origin callback
- **WHEN** a POST to the callback route has an `Origin` of another site
- **THEN** the server gives 403 with the text "a request with the session cookie comes from another site" and reads no SAML field.

#### Scenario: No JavaScript
- **WHEN** the browser runs no script on the ACS page
- **THEN** the page shows a "Continue" button that posts the fields.

### Requirement: IdP-started SAML off by default

The server SHALL refuse an unsolicited SAML response unless `allowIdpStarted` is on for the provider, and when it is on SHALL require a one-time assertion ID, an assertion age of at most 300 seconds, and the audience and recipient checks, and SHALL never mark that sign-in as fresh.

#### Scenario: Unsolicited response with the setting off
- **WHEN** a response with no `InResponseTo` arrives for a provider with `allowIdpStarted` false
- **THEN** the server gives 400 and writes one audit entry with the reason `sign-in.unsolicited` (NEW).

#### Scenario: Old unsolicited response
- **WHEN** an unsolicited assertion is 301 seconds old and the setting is on
- **THEN** the server gives 400 and opens no session.

### Requirement: Encrypted assertions off by default

The server SHALL refuse an `EncryptedAssertion` unless `encryptedAssertions` is on for the provider, and SHALL accept only RSA-OAEP key transport and AES-GCM content encryption.

#### Scenario: Encrypted assertion with the setting off
- **WHEN** a response holds an `EncryptedAssertion` and the setting is off
- **THEN** the server gives 400, decrypts nothing, and writes one audit entry with the reason `sign-in.encrypted-off` (NEW).

#### Scenario: Short cipher value
- **WHEN** the setting is on and the `CipherValue` is 1 byte long
- **THEN** the server gives 400 and does not panic.

### Requirement: Pending signing certificates

The server SHALL store a certificate that is new in fetched metadata as pending, SHALL raise an alert, and SHALL not verify a response with it until a site administrator accepts it with a fresh authentication and a reason.

#### Scenario: Response signed by a pending certificate
- **WHEN** a response is signed only by a pending certificate
- **THEN** the server gives 400 and writes one audit entry with the reason `sign-in.certificate-pending` (NEW).

### Requirement: SAML subject

The server SHALL take the subject from a persistent `NameID` or from the attribute that the provider names, and SHALL refuse a transient, email or unspecified `NameID` as a subject.

#### Scenario: Transient NameID
- **WHEN** the signed assertion has a transient `NameID` and the provider uses the `NameID`
- **THEN** the server gives 400 and opens no session.
