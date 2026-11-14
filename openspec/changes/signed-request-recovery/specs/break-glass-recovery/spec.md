## ADDED Requirements

### Requirement: first-boot-recovery-settings

When an installation starts for the first time, the server SHALL store the recovery mode and, in signed-request recovery, the first key of the trust bundle, in one transaction. In signed-request recovery, the server SHALL refuse the start when `--public-url` is empty and SHALL store nothing. The installation name SHALL be the setting `--installation`.

#### Scenario: First boot in signed-request recovery
- **WHEN** `serve` starts for the first time with `--recovery-mode signed-request` and a key file
- **THEN** the store holds the mode `signed-request` and one key, and the installation name is the name that `--installation` gives

#### Scenario: No key file
- **WHEN** `serve` starts for the first time with `--recovery-mode signed-request` and no key file
- **THEN** the server does not start and the error names `--recovery-public-key-file`

#### Scenario: No public URL
- **WHEN** `serve` starts for the first time with `--recovery-mode signed-request`, a key file and no `--public-url`
- **THEN** the server does not start and the store holds no mode and no key

#### Scenario: The stored mode wins
- **WHEN** the stored mode is `signed-request` and a later start sets `--recovery-mode bootstrap`
- **THEN** the effective mode is `signed-request` and the log has one warning

### Requirement: first-boot-key-file-is-one-ed25519-key

When an installation starts for the first time in signed-request recovery, the server SHALL read the key file and SHALL refuse the start when its content is not exactly one `ssh-ed25519` public key: a key of another type, more than one key, text that does not parse and an empty file are each refused. On that refusal the server SHALL store nothing and SHALL write no recovery settings.

#### Scenario: Malformed key file
- **WHEN** `serve` starts for the first time with `--recovery-mode signed-request` and a key file whose content does not parse as exactly one `ssh-ed25519` public key
- **THEN** the server does not start and the store holds no mode and no key
- **AND** the log has one error that names the file and the reason, with no key material

#### Scenario: Wrong-type key file
- **WHEN** `serve` starts for the first time with `--recovery-mode signed-request` and a key file that holds an `ssh-rsa` key or two `ssh-ed25519` keys
- **THEN** the server does not start and the store holds no mode and no key
- **AND** the log has one error that names the file and the reason, with no key material

### Requirement: restore-keeps-control-tables

The four recovery tables SHALL be in `ControlTables`, so a backup leaves them out and a restore keeps the live rows. A restore of an older backup SHALL NOT bring back a removed key, SHALL NOT let the supplier of a backup add a trusted key, and SHALL NOT make a used nonce new again.

#### Scenario: Restore after a key removal
- **WHEN** a key is removed and a backup made before the removal is restored
- **THEN** the key stays removed and a used nonce stays used

### Requirement: signed-request-makes-one-temporary-administrator

When the stored mode is `signed-request` and a request is canonical, in its time window, names this installation and its audience, is signed by a key of the trust bundle, has a new nonce, is inside the rate limits, and no temporary administrator of a recovery is live, the server SHALL make one temporary administrator and answer with the token encrypted to the key of the request.

#### Scenario: A valid request
- **WHEN** the operator submits a valid signed request
- **THEN** the store holds one person, one site-administrator grant, one record and one token, with the four audit entries, the nonce and one `break-glass.used` notification
- **AND** the answer decrypts with `request.key` only, and holds no token in plain text

#### Scenario: No console session
- **WHEN** a valid request succeeds
- **THEN** the answer sets no cookie

#### Scenario: The person member
- **WHEN** a request carries a `person` member that is not 1 to 64 characters of `[a-z0-9._-]`
- **THEN** the server refuses it with the one answer and does not change the value

#### Scenario: A person that is reserved or taken
- **WHEN** a request carries a `person` that names a reserved subject or an existing principal
- **THEN** the server answers with the one refusal, and the audit entry `recovery.refused` holds the reason `person`
- **AND** the store holds no new person, grant, record, token or nonce

### Requirement: audience-matches-public-url

The audience of a request SHALL match the setting `--public-url`, the external URL of the server. The server SHALL compare the two byte for byte after one normalization: it removes a trailing slash and changes nothing else, no case and no port. A value that does not match SHALL refuse with the reason `audience`.

#### Scenario: A wrong audience
- **WHEN** a request carries an audience that does not match `--public-url` byte for byte
- **THEN** the server refuses it with the one answer
- **AND** the audit entry `recovery.refused` holds the reason `audience`

### Requirement: use-writes-one-audit-entry

When a signed-request recovery succeeds, the server SHALL write one audit entry that holds the mode, the time, the client address and the `keyId` of the signing key, and the action `recovery:use-signed-request`, in the same transaction as the use.

#### Scenario: The use audit entry
- **WHEN** a signed-request recovery succeeds
- **THEN** the store holds one audit entry with the action `recovery:use-signed-request`, the mode `signed-request`, the time, the client address and the `keyId` of the signing key
- **AND** the entry commits in the same transaction as the temporary administrator

### Requirement: one-answer-for-every-refusal

When the server refuses a recovery request for any reason, it SHALL answer `403` with the code `recovery.refused` and the same body, and write the refusal audit entry with the reason, within the bound of the refusal limiter. One exception applies: the aggregate entry of a full limiter table holds no reason.

#### Scenario: The sixteen reasons look the same
- **WHEN** requests fail for the reasons `shape`, `canonical`, `time`, `mode`, `installation`, `audience`, `unknown-key`, `signature`, `nonce-used`, `rate-limit`, `recovery-active`, `maintenance`, `answer`, `store`, `address` and `person`
- **THEN** each answer has the same status, the same body and the same length
- **AND** each audit entry `recovery.refused` holds its reason

#### Scenario: The refusal entries are bounded
- **WHEN** one source sends many refusals with one reason
- **THEN** the limiter keys each refusal by the source group in its principal (`recovery-source:` and the group), the action `recovery:use-signed-request`, the reason, and an empty scope, and one source group holds at most 16 keys
- **AND** the audit log holds a bounded number of `recovery.refused` entries and one entry that carries the count of suppressed entries

#### Scenario: The table is full
- **WHEN** refusals from many sources fill the limiter table to `MaxKeys`
- **THEN** each later refusal goes into one aggregate entry that names no source group and no reason and counts the denials
- **AND** each refusal still writes one server log line with its reason

### Requirement: answer-key-and-its-seal

The `answerKey` member SHALL be an X25519 public key of exactly 32 bytes after decoding. The server SHALL refuse any other value with the one answer and the reason `answer`. When the seal of the answer fails, the server SHALL fail closed: refuse the request, write the denial with the reason `answer` and create nothing.

#### Scenario: A malformed answer key
- **WHEN** the `answerKey` member is 31 bytes, is 33 bytes, or is not base64
- **THEN** the server refuses the request with the one answer
- **AND** the audit entry `recovery.refused` holds the reason `answer`

#### Scenario: A failed seal
- **WHEN** the seal of the answer fails
- **THEN** the server refuses the request with the one answer and makes no temporary administrator
- **AND** the audit entry `recovery.refused` holds the reason `answer`

### Requirement: maintenance-mode-refuses

When the installation is in maintenance mode, the server SHALL refuse a signed recovery request with the one answer, and SHALL write the audit entry with the reason `maintenance`. The refusal fails closed.

#### Scenario: A request in maintenance mode
- **WHEN** a valid signed request arrives while the installation is in maintenance mode
- **THEN** the server answers `403` with the code `recovery.refused` and makes no temporary administrator
- **AND** the audit entry `recovery.refused` holds the reason `maintenance`

### Requirement: nonce-once

During a signed-request recovery, the server SHALL insert the nonce with a unique key as the first write of the transaction, before the live-recovery check. The insert SHALL write the nonce, `notAfter`, the delete time and the SHA-256 hash of the exact request bytes. A second request with the same nonce SHALL fail the insert, and the transaction SHALL fail with it and SHALL create nothing. The server SHALL keep the nonce until the `notAfter` time of its request plus 1 hour.

#### Scenario: Two at once
- **WHEN** two submits carry one new nonce at the same time on two instances
- **THEN** one temporary administrator exists

### Requirement: sealed-answer-written-in-the-transaction

When the server inserts the nonce, the insert SHALL leave `sealed_answer` empty. After the token is made and the answer is sealed, the same transaction SHALL write `sealed_answer` into the nonce row, so the commit makes the nonce row and the sealed answer visible together. When the seal fails, the transaction SHALL fail and SHALL commit no nonce row, so a committed nonce row always holds a sealed answer.

#### Scenario: Crash before the commit
- **WHEN** the server stops before the commit
- **THEN** no temporary administrator and no nonce exist, and the same request succeeds once

### Requirement: replay-sends-the-stored-answer

After the failed transaction of a repeated request, the server SHALL read the nonce row in a separate read. When the stored request hash equals the hash of this request, the time is before `notAfter` and the stored `sealed_answer` is not empty, the server SHALL send the stored sealed answer again. In all other cases, including other bytes, a time at or after `notAfter` or an empty `sealed_answer`, the server SHALL refuse with the one answer and the reason `nonce-used`.

#### Scenario: Replay before the end
- **WHEN** the same request arrives again before `notAfter`
- **THEN** the server sends the stored sealed answer again, makes no new temporary administrator and creates no new nonce
- **AND** the audit entry `recovery.refused` does not hold the reason `recovery-active`

#### Scenario: Replay with other bytes
- **WHEN** a request carries a nonce of a stored request but other bytes
- **THEN** the server refuses it with the one answer
- **AND** the audit entry `recovery.refused` holds the reason `nonce-used`

#### Scenario: Replay after the end
- **WHEN** a request with a used nonce arrives after `notAfter`
- **THEN** the server refuses it with the one answer
- **AND** the audit entry `recovery.refused` holds the reason `nonce-used`

### Requirement: replay-writes-one-audit-entry

The repeat SHALL be counted like each attempt: by the per-source counter before the signature check and by the installation counter after it. The repeat SHALL write one audit entry with the action `recovery:use-signed-request`, the result `answer resent`, the nonce, the `keyId` of the signing key and the client address. It SHALL create no temporary administrator and SHALL send no new `break-glass.used` notice.

#### Scenario: The resend audit entry
- **WHEN** the same request arrives again before `notAfter`
- **THEN** the store holds one audit entry with the action `recovery:use-signed-request`, the result `answer resent`, the nonce, the `keyId` of the signing key and the client address
- **AND** no new temporary administrator and no new `break-glass.used` notice exist

### Requirement: request-window

When a request has `notAfter` more than 10 minutes after `notBefore`, or the time of the server is before `notBefore` minus 2 minutes or after `notAfter` plus 2 minutes, the server SHALL refuse it.

#### Scenario: Late request
- **WHEN** a request arrives 3 minutes after its `notAfter`
- **THEN** the server refuses it with the one answer

### Requirement: shared-rate-limit

When one source address, or its /64 group for IPv6, sent 5 recovery requests in 15 minutes, the server SHALL refuse more requests from that source in that window on every instance. The server SHALL count the attempt and refuse it before it checks the signature of the request.

#### Scenario: Sixth request on another instance
- **WHEN** five requests from one /64 group reach instance A and a sixth reaches instance B
- **THEN** instance B refuses the sixth with the one answer

#### Scenario: The limit bounds the signature check
- **WHEN** one source sends requests with a bad signature after the limit
- **THEN** the server refuses them with the one answer, and runs no signature check for a refused request

### Requirement: installation-rate-limit

When the installation received 30 recovery requests with a valid signature in 15 minutes, the server SHALL refuse more such requests in that window on every instance. The server SHALL count that attempt after the signature check, so it counts only requests with a valid signature from a key of the trust bundle.

#### Scenario: Bad signatures do not move the installation counter
- **WHEN** many sources send requests with a bad signature and then one source sends a valid request
- **THEN** the installation counter did not move, and the valid request succeeds

### Requirement: source-address

The server SHALL take the source address of a request from the client address, as the trusted-proxy setting of the server gives it. Without trusted proxies it is the address of the TCP peer. With `--trusted-proxies` it is the address that a trusted proxy forwarded. The server SHALL parse the address and SHALL refuse a value that is not a valid IP address.

#### Scenario: A hostile forwarded header
- **WHEN** a request from a peer that is not a trusted proxy carries a forwarding header
- **THEN** the source is the client address of the peer, and the header has no effect

#### Scenario: A malformed address
- **WHEN** the source address is malformed
- **THEN** the server refuses the request with the one answer

### Requirement: one-live-recovery

When a temporary administrator from a recovery, by the host command or by the route, is live, the server SHALL refuse a new signed recovery request. A repeat of the request that made the live temporary administrator SHALL NOT be refused with `recovery-active`: its nonce insert fails first, and the server sends the stored sealed answer again.

#### Scenario: Second recovery
- **WHEN** a valid request arrives while a temporary administrator of a recovery is live
- **THEN** the server refuses it with the one answer, and the audit reason is `recovery-active`

#### Scenario: A repeat of the live request
- **WHEN** the same request that made the live temporary administrator arrives again before `notAfter`
- **THEN** the server sends the stored sealed answer and the audit reason is not `recovery-active`

### Requirement: review-whatever-the-opt-in

When a signed-request recovery succeeds, the server SHALL open a break-glass review of the kind `temp-admin` with the mode `signed-request` and the source, and send one notice to each site administrator, whatever `breakGlassReview.enabled` says.

#### Scenario: Opt-in off
- **WHEN** `breakGlassReview.enabled` is off and a recovery succeeds
- **THEN** a review opens with no deadline and no notice steps, and each site administrator gets one notice with the time, the source, the principal and the `keyId` of the signing key

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

When the server or the CLI reads or writes a file of the flow, it SHALL open the file through a root or a clean-path containment check, SHALL limit the read to 16 KiB, and SHALL refuse a path that leaves the root or a file larger than 16 KiB. The rule applies to `request.json`, `request.json.sig`, `request.key`, the private key file of `sign`, the public key file of `verify` and the public key file of the first boot.

#### Scenario: A path that leaves the root
- **WHEN** a file path leaves the root
- **THEN** the reader refuses it and writes no signature

#### Scenario: A file larger than 16 KiB
- **WHEN** a file is larger than 16 KiB
- **THEN** the reader refuses it

### Requirement: private-key-file-modes

The CLI SHALL write `request.key` with mode 0600, and SHALL refuse a private key file that group or others can read.

#### Scenario: A private key file that group or others can read
- **WHEN** `request.key` or the private key file of `sign` has mode 0644
- **THEN** the command refuses it and writes no signature
