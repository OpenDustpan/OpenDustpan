## ADDED Requirements

### Requirement: first-boot-recovery-settings

When an installation starts for the first time, the server SHALL store the recovery mode, a new installation ID and, in signed-request recovery, the first key of the trust bundle, in one transaction.

#### Scenario: First boot in signed-request recovery
- **WHEN** `serve` starts for the first time with `--recovery-mode signed-request` and a key file
- **THEN** the store holds the mode `signed-request`, one installation ID and one key, and the log names the installation ID

#### Scenario: No key file
- **WHEN** `serve` starts for the first time with `--recovery-mode signed-request` and no key file
- **THEN** the server does not start and the error names `--recovery-public-key-file`

#### Scenario: The stored mode wins
- **WHEN** the stored mode is `signed-request` and a later start sets `--recovery-mode bootstrap`
- **THEN** the effective mode is `signed-request` and the log has one warning

### Requirement: signed-request-makes-one-temporary-administrator

When the stored mode is `signed-request` and a request is canonical, in its time window, for this installation, signed by a key of the trust bundle, with a new nonce, inside the rate limits, and no temporary administrator of a recovery is live, the server SHALL make one temporary administrator and answer with the token encrypted to the key of the request. The `person` member SHALL be 1 to 64 characters of `[a-z0-9._-]`; the server refuses any other value and does not change it.

#### Scenario: A valid request
- **WHEN** the operator submits a valid signed request
- **THEN** the store holds one person, one site-administrator grant, one record and one token, with the four audit entries, the nonce and one `break-glass.used` notification
- **AND** the answer decrypts with `request.key` only, and holds no token in plain text

#### Scenario: No console session
- **WHEN** a valid request succeeds
- **THEN** the answer sets no cookie

#### Scenario: The person member
- **WHEN** a request has a `person` member outside the allowlist
- **THEN** the server refuses it with the one answer

### Requirement: one-answer-for-every-refusal

When the server refuses a recovery request for any reason, it SHALL answer `403` with the code `recovery.refused` and the same body, and write one audit entry with the reason.

#### Scenario: The twelve reasons look the same
- **WHEN** requests fail for the reasons `shape`, `canonical`, `time`, `mode`, `installation`, `unknown-key`, `signature`, `nonce-used`, `rate-limit`, `recovery-active`, `maintenance` and `store`
- **THEN** each answer has the same status, the same body and the same length
- **AND** each audit entry `recovery.refused` holds its reason

### Requirement: maintenance-mode-refuses

When the installation is in maintenance mode, the server SHALL refuse a signed recovery request with the one answer, and SHALL write the audit entry with the reason `maintenance`. The refusal fails closed.

#### Scenario: A request in maintenance mode
- **WHEN** a valid signed request arrives while the installation is in maintenance mode
- **THEN** the server answers `403` with the code `recovery.refused` and makes no temporary administrator
- **AND** the audit entry `recovery.refused` holds the reason `maintenance`

### Requirement: nonce-once

When a nonce was used, the server SHALL refuse a second request with that nonce until the `notAfter` time of the first request plus 1 hour.

#### Scenario: Replay
- **WHEN** a used request arrives again
- **THEN** the server refuses it with the one answer and makes no temporary administrator

#### Scenario: Two at once
- **WHEN** two submits carry one new nonce at the same time on two instances
- **THEN** one temporary administrator exists

#### Scenario: Crash before the commit
- **WHEN** the server stops before the commit
- **THEN** no temporary administrator and no nonce exist, and the same request succeeds once

### Requirement: request-window

When a request has `notAfter` more than 10 minutes after `notBefore`, or the time of the server is before `notBefore` minus 2 minutes or after `notAfter` plus 2 minutes, the server SHALL refuse it.

#### Scenario: Late request
- **WHEN** a request arrives 3 minutes after its `notAfter`
- **THEN** the server refuses it with the one answer

### Requirement: shared-rate-limit

When one source address, or its /64 group for IPv6, sent 5 recovery requests in 15 minutes, or the installation received 30, the server SHALL refuse more requests in that window on every instance. The server SHALL count the attempt and refuse it before it checks the signature of the request.

#### Scenario: Sixth request on another instance
- **WHEN** five requests from one /64 group reach instance A and a sixth reaches instance B
- **THEN** instance B refuses the sixth with the one answer

#### Scenario: The limit bounds the signature check
- **WHEN** one source sends requests with a bad signature after the limit
- **THEN** the server refuses them with the one answer, and runs no signature check for a refused request

### Requirement: source-address

The server SHALL take the source address of a request from the resolved client address of the trusted-proxy setting, or from the remote address of the connection when no proxy is trusted. The server SHALL parse the address and SHALL refuse a value that is not a valid IP address.

#### Scenario: A hostile forwarded header
- **WHEN** a request from a peer that is not a trusted proxy carries a forwarding header
- **THEN** the source is the remote address of the connection, and the header has no effect

#### Scenario: A malformed address
- **WHEN** the source address is malformed
- **THEN** the server refuses the request with the one answer

### Requirement: one-live-recovery

When a temporary administrator from a recovery, by the host command or by the route, is live, the server SHALL refuse a signed recovery request.

#### Scenario: Second recovery
- **WHEN** a valid request arrives while a temporary administrator of a recovery is live
- **THEN** the server refuses it with the one answer, and the audit reason is `recovery-active`

### Requirement: review-whatever-the-opt-in

When a signed-request recovery succeeds, the server SHALL open a break-glass review of the kind `temp-admin` with the mode `signed-request` and the source, and send one notice to each site administrator, whatever `breakGlassReview.enabled` says.

#### Scenario: Opt-in off
- **WHEN** `breakGlassReview.enabled` is off and a recovery succeeds
- **THEN** a review opens with no deadline and no notice steps, and each site administrator gets one notice with the time, the source and the principal

#### Scenario: Opt-in on
- **WHEN** `breakGlassReview.enabled` is on and a recovery succeeds
- **THEN** a review opens with its deadline and its notice steps

#### Scenario: No notification, no recovery
- **WHEN** the write of `break-glass.used` fails
- **THEN** no temporary administrator exists

### Requirement: review-list-filters-on-the-mode

The list of break-glass reviews SHALL filter on the recovery mode.

#### Scenario: Filter by mode
- **WHEN** a site administrator lists the break-glass reviews with the mode `signed-request`
- **THEN** the list holds the reviews of signed-request recoveries and no review of another mode

### Requirement: two-recovery-steps

When the caller is a temporary administrator of a recovery, the server SHALL let it reset the factors of a person and issue an enrollment code with no fresh authentication, and SHALL refuse every other step that needs a fresh authentication.

#### Scenario: Reset and enrollment code
- **WHEN** a temporary administrator of a recovery resets the factors of a person and issues an enrollment code, with a reason
- **THEN** both succeed, and each writes an audit entry that names the temporary administrator

#### Scenario: Other fresh steps
- **WHEN** a temporary administrator of a recovery creates an access token, adds a recovery key or changes a verified email address
- **THEN** the server answers `403` with `mfa.fresh-step-required`

### Requirement: trust-bundle-rotation

When a site administrator with a fresh authentication adds a key, the server SHALL accept requests signed by each key of the trust bundle, and SHALL refuse the removal of the last key.

#### Scenario: Overlap and end
- **WHEN** a second key is added and the first key is removed
- **THEN** a request signed by the second key succeeds and a request signed by the first key is refused

#### Scenario: Last key
- **WHEN** a site administrator removes the last key
- **THEN** the server answers `409` with `recovery.last-key`

### Requirement: signer-needs-an-encrypted-key

When `opendustpan recovery sign` gets a key file that no passphrase encrypts, the command SHALL refuse it and write no signature.

#### Scenario: Plain key file
- **WHEN** the operator signs with a key file that has no passphrase
- **THEN** the command exits 1, names `ssh-keygen -p`, and writes no file

### Requirement: key-files-are-contained

When the server or the CLI reads a key file, it SHALL open the file through a root or a clean-path containment check, SHALL limit the read to 16 KiB, and SHALL refuse a path that leaves the root or a file larger than 16 KiB. The CLI SHALL refuse a private key file that group or others can read.

#### Scenario: A path that leaves the root
- **WHEN** a key file path leaves the root
- **THEN** the reader refuses it and writes no signature

#### Scenario: A file larger than 16 KiB
- **WHEN** a key file is larger than 16 KiB
- **THEN** the reader refuses it

#### Scenario: A private key file that group or others can read
- **WHEN** the private key file has mode 0644
- **THEN** the command refuses it and writes no signature
