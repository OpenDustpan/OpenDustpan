## ADDED Requirements

### Requirement: A static LDAP provider is set at start

The server SHALL build one LDAP provider from flags, `DUSTPAN_LDAP_*` variables and
file keys `ldap.*`, validated with an allowlist, and SHALL fail the start on an
invalid setting. The provider list SHALL show each effective setting and its
source, and SHALL show `bindPasswordSet` and never the password.

#### Scenario: Valid provider
- **WHEN** the settings are valid and the bind password is set
- **THEN** `GET /api/server/sign-in-providers` shows the provider with `source: static`, `mode: ldap`, each effective setting and its source

#### Scenario: Setting without a URL
- **WHEN** an `--ldap-*` setting is set and `--ldap-url` is not
- **THEN** the start fails and names `--ldap-url`

### Requirement: The bind password has one way in

The server MUST read the bind password only from `DUSTPAN_LDAP_BIND_PASSWORD` or from
the file that `DUSTPAN_LDAP_BIND_PASSWORD_FILE` names. No flag and no file key SHALL
take it. The server MUST NOT show the bind password or a typed password in a log, an
error, an audit entry or an API answer.

#### Scenario: Password file that others can read
- **WHEN** the password file has permissions that let others read it
- **THEN** the start fails

#### Scenario: Failed sign-in
- **WHEN** a sign-in fails
- **THEN** no log line, error text or audit entry holds a password

### Requirement: A person signs in through the route

The server SHALL accept `POST /api/dustpan/v1/sign-in/providers/{provider}/password`
with a JSON body `{"userName", "password"}`, and SHALL answer `204` with the session
cookie when the directory accepts the user name and the password.

#### Scenario: Right password over TLS
- **WHEN** a console sends a right user name and password for the static provider
- **THEN** the answer is `204` and the session cookie is set

### Requirement: One answer for each refusal

The server SHALL answer one identical `401 sign-in.failed` for a wrong password, an
unknown user, a locked name or person, a disabled person, an unknown provider, an OIDC
provider ID and a request of another site. Each SHALL spend one unit of the password
budget of the source address. A body that is not valid SHALL answer `400
request.body-invalid`. A full budget SHALL answer `429` before the body is read. A
directory that does not answer SHALL give `503 sign-in.unavailable` and spend nothing.

#### Scenario: Wrong password
- **WHEN** the directory refuses the password
- **THEN** the answer is `401 sign-in.failed` and the audit entry holds the real reason

#### Scenario: Full budget
- **WHEN** the budget of the source address is full
- **THEN** the answer is `429` and no connection to the directory opens

### Requirement: A request of another site is refused

The route MUST call `SignInFromBody` before the budget and before any store or
directory call. The media type SHALL be `application/json`. An `Origin` header, when
present, SHALL be an allowed console origin. A `Sec-Fetch-Site: cross-site` header SHALL
refuse the request.

#### Scenario: Text/plain from another site with the right password
- **WHEN** a page of another site sends the body as `text/plain`
- **THEN** the answer is `401 sign-in.failed`, no cookie is set, no budget is spent and no connection to the directory opens

### Requirement: Plain connections are refused unless an operator opts in

The server MUST refuse an LDAP connection with no TLS to a host that is not a loopback
IP address, unless `egress.allowPlainConnections` is on. The server MUST read the
setting at each sign-in.

#### Scenario: Opt-in off
- **WHEN** the provider URL is `ldap://` to a non-loopback host and the setting is off
- **THEN** the sign-in is refused and the error names the address and the setting

#### Scenario: Opt-in turned on, then off
- **WHEN** the setting is turned on and then off between two sign-ins
- **THEN** the first sign-in succeeds and the second is refused

### Requirement: The subject lockout limit is a setting

The server SHALL read `ldap.subjectFailureLimit` as static configuration, 5 by default,
1 to 100, and SHALL show its effective value and its source. A value outside the range
MUST stop the start.

#### Scenario: Lock after the limit
- **WHEN** a person fails the limit number of sign-ins in a row
- **THEN** the next sign-in is refused with the same `401 sign-in.failed` and no password reaches the directory

### Requirement: Groups are read and not applied

The server SHALL read the groups of a person and SHALL NOT apply a membership from them.

#### Scenario: Groups on
- **WHEN** `--ldap-groups` is `search` and the person has groups
- **THEN** the sign-in succeeds and no membership changes
