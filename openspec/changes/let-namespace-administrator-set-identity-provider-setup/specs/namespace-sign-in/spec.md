## ADDED Requirements

### Requirement: Namespace-only principals

When a person signs in through the OIDC or SAML provider of a namespace, the server SHALL find or make a principal of that namespace only via FindOrMakeSignInPrincipal.

#### Scenario: FindOrMakeSignInPrincipal same subject in A and B
- **WHEN** the same subject signs in through the provider of namespace A and then through the provider of namespace B
- **THEN** server holds two principals with the same subject, one with home A and one with home B

#### Scenario: FindOrMakeSignInPrincipal cross-namespace isolation
- **WHEN** a person signs in through the provider of namespace A
- **THEN** server finds or makes a principal whose home is namespace A only

### Requirement: Sign-in audit

When a sign-in through the provider of a namespace succeeds or fails, the server SHALL write an audit entry in the audit scope of that namespace.

#### Scenario: Sign-in success audited
- **WHEN** a person signs in through the provider of the namespace
- **THEN** namespace audit log holds the sign-in entry with principal home and provider

#### Scenario: Sign-in failure audited
- **WHEN** a sign-in through the provider of the namespace fails
- **THEN** namespace audit log holds the refused sign-in entry with the reason

### Requirement: Off provider refused

When a person signs in through a namespace provider that is off or unknown, the server SHALL refuse the sign-in.

#### Scenario: Provider off refused
- **WHEN** a person signs in through a namespace provider that is off or unknown
- **THEN** server refuses with 401 and writes a namespace audit entry

### Requirement: Disabled subject refused

When the directory reports the subject as disabled at namespace sign-in, the server SHALL refuse the sign-in.

#### Scenario: Disabled subject refused
- **WHEN** the directory reports the subject as disabled at namespace sign-in
- **THEN** server refuses the sign-in and writes a namespace audit entry

### Requirement: Fresh save

When a namespace administrator saves OIDC or SAML provider config, the server SHALL need a fresh authentication within the one window via SaveProviderConfig.

#### Scenario: SaveProviderConfig stale save refused with 401
- **WHEN** a namespace administrator saves provider config without a fresh authentication in the one window
- **THEN** server refuses with 401 and writes a refusal audit entry

#### Scenario: SaveProviderConfig IdP-started never fresh
- **WHEN** the fresh proof is an IdP-started SAML sign-in
- **THEN** server refuses the save

#### Scenario: SAML fresh uses SP-started ForceAuthn
- **WHEN** the fresh proof is an SP-started SAML sign-in with ForceAuthn in the one window
- **THEN** server accepts the save

### Requirement: Atomic audit

When a namespace administrator saves provider config, the server SHALL write the config and its audit entry in one transaction via SaveProviderConfig.

#### Scenario: SaveProviderConfig atomic write and audit
- **WHEN** a namespace administrator saves provider config with fresh authentication
- **THEN** namespace holds the new config and the audit entry together, and concurrent saves keep the last fenced write

### Requirement: Manage refused

When a caller without namespace manage-sign-in saves provider config, the server SHALL refuse the save.

#### Scenario: Without manage refused
- **WHEN** a caller without namespace manage-sign-in saves provider config
- **THEN** server refuses with 403 and writes a namespace audit entry

### Requirement: Invalid config refused

When a namespace administrator saves an OIDC or SAML config that breaks the allowlist or size limit, the server SHALL refuse the save.

#### Scenario: Bad issuer refused
- **WHEN** the OIDC issuer is not https or the SAML metadata exceeds 256 KiB or holds a DTD
- **THEN** server refuses with 400 and saves nothing

#### Scenario: SSRF metadata refused
- **WHEN** the metadata URL points outside the egress allowlist
- **THEN** server refuses the fetch before save

### Requirement: Effective values

When a namespace reader reads provider state, the server SHALL answer the effective values with no secret value.

#### Scenario: Effective provider shown without secrets
- **WHEN** a namespace reader reads provider state
- **THEN** server answers issuer, entity ID, and on state with no secret value

### Requirement: Secret sealed

When a namespace administrator saves the OIDC client secret, the server SHALL seal it as a stored secret.

#### Scenario: Client secret sealed and rotated
- **WHEN** a namespace administrator saves or rotates the OIDC client secret
- **THEN** server seals the new secret, keeps the old value until rotation ends, and never writes a secret to logs or audit

### Requirement: Suspended namespace

When the namespace is suspended or deleted, the server SHALL refuse provider saves and new sign-ins.

#### Scenario: Suspended namespace refused
- **WHEN** the namespace is suspended or deleted
- **THEN** server refuses provider saves and new sign-ins with namespace suspended

### Requirement: Removed provider sessions

When a namespace administrator turns a provider off, the server SHALL keep existing principals and refuse new sign-ins.

#### Scenario: Removed provider keeps principals
- **WHEN** a namespace administrator turns a provider off or removes it
- **THEN** existing principals stay and new sign-ins refuse while live sessions end at expiry

### Requirement: IdP-started off

When a namespace SAML provider has no explicit IdP-started setting, the server SHALL refuse unsolicited assertions.

#### Scenario: IdP-started default off
- **WHEN** a SAML provider has no explicit IdP-started setting
- **THEN** server refuses unsolicited assertions
