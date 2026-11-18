## ADDED Requirements

### Requirement: The bootstrap administrator asks for a one-time code

The server SHALL accept `POST /api/server/first-administrator` only from the static bootstrap administrator, while the marker "first administrator made" is not stored. The request SHALL carry the principal ID of one person and a reason. The server SHALL answer `201` with a code of 32 bytes from `crypto/rand`, the principal and the expiry 10 minutes after the request, with `Cache-Control: no-store`. One code SHALL be valid at a time: a new request replaces the old code.

#### Scenario: Offer for a live person

- **WHEN** the bootstrap administrator sends the principal ID of a live, enabled person and a valid reason
- **THEN** the answer is `201` with the code and its expiry, and an audit entry names the bootstrap administrator as creator and holds the reason and no code

#### Scenario: Offer after the marker

- **WHEN** the marker is stored and the bootstrap administrator sends a request
- **THEN** the answer is `409` with the NEW error code `first-administrator.already-made` and nothing changes

#### Scenario: Offer from another caller

- **WHEN** a personal token, a temporary administrator or a console session sends the request
- **THEN** the server refuses it and writes a failure entry

#### Scenario: Principal that cannot be the first administrator

- **WHEN** the principal is a break-glass person, a temporary administrator, a service, a disabled or deleted person or a `dev:` subject
- **THEN** the answer is `400` with the NEW error code `first-administrator.principal-invalid`

#### Scenario: Reason with a hidden character

- **WHEN** the reason is blank, longer than 500 characters, not UTF-8 or holds a control or format character
- **THEN** the answer is `400` with the NEW error code `first-administrator.reason-invalid`

### Requirement: The person redeems the code in a fresh console session

The server SHALL accept `POST /api/server/first-administrator/redeem` with the code in the body only, in a console session with its CSRF value and a fresh authentication. The check of the fresh authentication SHALL NOT depend on the setting `freshAuthEnabled`.

#### Scenario: Redeem with a fresh sign-in

- **WHEN** the named person sends the code in a console session whose sign-in is fresh
- **THEN** the answer is `200`, the grant is listed with the bootstrap administrator as creator, the code is spent and the marker is stored

#### Scenario: Redeem with an access token or a stale sign-in

- **WHEN** the request has an access token, or the sign-in of the console session is older than the window
- **THEN** the answer is `403` with the error code `mfa.fresh-step-required` and nothing changes, also when `freshAuthEnabled` is off

#### Scenario: Wrong, spent, expired or foreign code

- **WHEN** the code is wrong, already spent, older than 10 minutes, or made for another person
- **THEN** the answer is `400` with the NEW error code `first-administrator.code-invalid`, one text for all four cases, and nothing changes

#### Scenario: Failed audit write

- **WHEN** the audit entry cannot be written
- **THEN** the grant, the spend and the marker are not stored and the request is refused

#### Scenario: Person holds the role already

- **WHEN** the person holds the `site-administrator` role on the installation
- **THEN** the answer is `409` with the NEW error code `first-administrator.grant-exists`, the code is not spent and the marker is not stored

### Requirement: The redeem is one transaction

One transaction SHALL check that the marker is not set, that the code names the principal of the session, that the code is valid and not expired, that the principal is still a live, enabled person and that the sign-in is fresh. The same transaction SHALL make the `site-administrator` grant on the installation with the bootstrap administrator as creator, spend the code, store the marker and write one audit entry.

#### Scenario: All facts hold

- **WHEN** the named person redeems a valid code in a fresh console session
- **THEN** the grant, the spend, the marker and the audit entry are stored together

#### Scenario: One fact fails

- **WHEN** the marker is set, the principal of the session differs from the principal of the code, or the principal is no longer a live, enabled person
- **THEN** nothing is stored and the refusal writes an audit entry

### Requirement: The code is secret

The server SHALL store the digest of the code only and compare digests in constant time. The code SHALL NOT be in a URL, a log line, an audit entry, a metric, an error text or a stored row. A wrong code SHALL spend the sign-in failure budget.

#### Scenario: Code never leaves the answer

- **WHEN** an offer and a redeem run with the log, audit and metrics captured
- **THEN** the code and its digest are in none of them, and the stored row holds the digest only

### Requirement: The marker is a stored fact with a read

The server SHALL store the marker "first administrator made" in the same transaction as the grant, SHALL include it in a backup, and SHALL answer `GET /api/server/first-administrator` with the marker and the open offer, never with the code. A later loss of every administrator SHALL go through recovery, not through this route.

#### Scenario: Read of the marker

- **WHEN** the bootstrap administrator or an operator reads the route before and after the redeem
- **THEN** the answer shows `made: false` with the open offer, and then `made: true` with the person, the grant, the session ID and the time

#### Scenario: Backup and restore

- **WHEN** a backup is restored into another installation
- **THEN** the marker comes with the grants, and the open code does not

### Requirement: The guides give the true path

The guides of console sign-in, local users and workload identity SHALL describe the path of the first administrator in the same pull request, and a page SHALL give the commands and the errors.

#### Scenario: Operator with no shell

- **WHEN** an operator follows the page from a new installation
- **THEN** each command runs as written and ends with a site administrator who holds a stored grant
