## ADDED Requirements

### Requirement: SignFileURL shared URL

When one instance signs a registry file URL with SignFileURL, the server SHALL accept that URL with VerifyFileURL on every instance of the same installation inside 360 seconds from signing.

#### Scenario: Cross-instance verify
- **WHEN** one instance signs a provider file URL with SignFileURL and another instance receives the GET inside 360 seconds
- **THEN** the second instance returns the file bytes with HTTP 200

#### Scenario: Foreign namespace refused
- **WHEN** a caller presents a URL signed for another namespace
- **THEN** the server answers registry.signature-invalid

### Requirement: VerifyFileURL refuses bad URL

If a signed registry file URL checked by VerifyFileURL is altered, foreign, expired or of unknown version, then the server SHALL refuse it with registry.signature-invalid.

#### Scenario: Tamper refused
- **WHEN** a caller alters one byte of the signature
- **THEN** the server answers registry.signature-invalid and writes no file bytes

#### Scenario: Unknown version
- **WHEN** a caller presents v=2
- **THEN** the server answers registry.signature-invalid

#### Scenario: Expired URL
- **WHEN** a caller presents the URL after 360 seconds from signing
- **THEN** the server answers registry.signature-invalid

### Requirement: SignFileURL grant at sign

When a caller without the registry read grant asks SignFileURL for a signed file URL, the server SHALL refuse the request.

#### Scenario: No grant
- **WHEN** a caller without the grant asks SignFileURL for a URL
- **THEN** the server makes no URL

### Requirement: SignFileURL store outage

When the database is not available, the server SHALL answer a SignFileURL sign request in the 503 shape of database-outage-503-shape.

#### Scenario: Outage
- **WHEN** the database is down during a SignFileURL request
- **THEN** the server answers in the 503 shape of database-outage-503-shape and makes no URL
