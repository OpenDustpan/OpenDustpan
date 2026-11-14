## ADDED Requirements

### Requirement: verify-ecdsa-p256

When a step-up signature uses ecdsa-sha2-nistp256, the server SHALL run Verify with ECDSA P-256 over SHA-256.

#### Scenario: valid ECDSA signature
- **WHEN** a step-up signature uses ecdsa-sha2-nistp256 with a valid P-256 signature over SHA-256
- **THEN** the server accepts the step-up.

#### Scenario: bad ECDSA range
- **WHEN** r or s is zero or not below the P-256 order, or mpint padding is wrong
- **THEN** the server returns stepup.ErrInvalid.

#### Scenario: bad ECDSA point
- **WHEN** the P-256 point is not on the curve or is the point at infinity
- **THEN** the server returns stepup.ErrInvalid.

#### Scenario: ECDSA wire mismatch
- **WHEN** the key type and signature format mismatch, trailing bytes follow the signature or the key blob, or the curve name is not nistp256
- **THEN** the server returns stepup.ErrInvalid.

### Requirement: verify-sk-ecdsa

When a step-up signature uses sk-ecdsa-sha2-nistp256, the server SHALL verify the authenticator message sha256(app) plus flags plus counter plus sha256(data) hashed with SHA-256.

#### Scenario: valid sk-ECDSA signature
- **WHEN** a step-up signature uses sk-ecdsa-sha2-nistp256 with user presence set and only UP and UV flags
- **THEN** the server accepts the step-up.

#### Scenario: missing user presence
- **WHEN** the sk-ECDSA flags omit user presence or carry a flag other than UP and UV
- **THEN** the server returns stepup.ErrInvalid.

#### Scenario: wrong sk application
- **WHEN** the sk-ECDSA application string does not match the expected step-up value
- **THEN** the server returns stepup.ErrInvalid.

#### Scenario: bad sk-ECDSA wire
- **WHEN** the sk-ECDSA signature blob has a bad authenticator-data length, bad flag byte, or trailing bytes
- **THEN** the server returns stepup.ErrInvalid.

### Requirement: verify-rsa-size

When a step-up signature uses an ssh-rsa key, the server SHALL accept only moduli of 3072 to 8192 bits with signature type rsa-sha2-256 or rsa-sha2-512.

#### Scenario: valid RSA signature
- **WHEN** an ssh-rsa key of 3072 to 8192 bits signs with rsa-sha2-256 or rsa-sha2-512
- **THEN** the server accepts the step-up.

#### Scenario: short or SHA-1 RSA
- **WHEN** the RSA modulus is below 3072 bits or the signature type is ssh-rsa with SHA-1
- **THEN** the server returns stepup.ErrInvalid.

#### Scenario: oversize RSA
- **WHEN** the RSA modulus exceeds 8192 bits or the exponent is even or outside 3 to 4294967295
- **THEN** the server returns stepup.ErrInvalid.

#### Scenario: bad RSA signature value
- **WHEN** the RSA signature length differs from the modulus length or its value is not below the modulus
- **THEN** the server returns stepup.ErrInvalid.

#### Scenario: RSA label mismatch
- **WHEN** an ssh-rsa key carries a non-RSA signature, or a rsa-sha2-256 label carries a SHA-512 digest
- **THEN** the server returns stepup.ErrInvalid.

### Requirement: refuse-other-types

When the SSH key type is outside the allowlist, the server SHALL return stepup.ErrInvalid.

#### Scenario: unknown type
- **WHEN** the SSH key type is outside the five-type allowlist
- **THEN** the server returns stepup.ErrInvalid.
