# 285. Local users sign in with a password and with a passkey

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026, with the product manager decisions marked below. Answers question O1 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md) with "no" (option B). Amends ADR 55 (decision 5), ADR 46 (decision 1) and ADR 237 (decision 3)

## Context

[ADR 55](0055-sign-in-methods-in-milestone-4.md) decision 5 says that a
local user signs in with a passkey alone. It names no password. The
proposal of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md) P7 asked the
owner, as question O1, whether a passkey is the only sign-in of a local user.
It recommended "yes" (option A).

On 31 October 2026 the project owner decided:

> Sign in for local users accepted as is EXCEPT I also want username +
> password support. We absolutely need it for SaaS and currently the demo
> mode is useless because the only conceivable way to sign in is with an
> admin token which blocks most of the features.

So a local user signs in with a user name and a password, with a passkey,
or with both. The rest of the local-user design of RFC 21 P7 stays as the
owner accepted it. The owner also approved the modules
`github.com/go-webauthn/webauthn`, `github.com/go-ldap/ldap/v3`,
`github.com/russellhaering/gosaml2`, `github.com/russellhaering/goxmldsig`
and `github.com/beevik/etree`. OpenDustpan has no users and no data yet, so
this decision needs no migration of data and no compatibility path.

Facts on `main` at a50ab96:

- No code stores a password. `pkg/session` has the providers `oidc` and
  `oauth` only.
- `golang.org/x/crypto` is in `go.mod` as an indirect requirement. Its
  package `argon2` gives Argon2id. `golang.org/x/text` is a direct
  requirement. Its package `unicode/norm` gives the NFC form.
- `internal/authgate` counts the failures of each source address, in one
  count that every instance on one database shares. It has no count by
  user name.
- `make demo` (`scripts/demo.sh`) runs `opendustpan serve` with a
  bootstrap admin token. So its data is of the installation kind
  `production`, and the only sign-in is the bootstrap admin token. The
  bootstrap admin token is not a person, so the features that need a person
  (personal tokens, approvals, preferences, MFA) do not work there.
- `opendustpan dev` (ADR 237) makes data of the kind `dev`. A server of the
  kind `production` refuses each principal whose subject starts with `dev:`
  (ADR 253 decision 13).

Each decision of the product manager has the label "(product manager)" and a
"Benchmark:" line. The marks "(fetched)" and "(not fetched)" have the meaning
of RFC 21: "(fetched)" means that the author fetched the source page on
2026-10-31. "(not fetched)" means that the fact comes from RFC 21 or the
research note `docs/research/m4-identity-defaults.md`, and a reviewer must
check it before a guide quotes it. This ADR never says that OpenDustpan
complies with a standard. It says "designed against".

## Decision

### One local user, two kinds of credential

1. **A local user has a password, passkeys, or both (owner).** A local user
   is one person principal with one local user record. The record holds the
   user name and the password hash. The passkeys of the person are records
   of the person principal (package `mfa`). So a person who signs in with
   the password and the same person who signs in with a passkey are one
   local user: both credentials name the same principal ID. The server never
   links two records by a user name or an email (ADR 70 decision 22).
2. **The provider ID `local` (product manager).** Each session of a local
   user sign-in records the provider `local`. No configured identity
   provider can take that ID. The subject of a local user is
   `local:<record ID>`, and the record ID never changes. The API refuses
   each other subject that starts with `local:`. So a change of the
   user name changes no subject, no grant and no audit reference.
   Benchmark: ADR 70 decision 22 (no link by an attribute that can change).
3. **The user name (product manager).** A user name has 1 to 64 characters
   that match `^[a-z0-9][a-z0-9._-]{0,63}$`. The API refuses each other
   value at create time. It does not change a value to make it valid. A
   sign-in compares the name after an ASCII lowercase fold. A change of the
   user name of a member of a protected group is a protected action (ADR 70
   decision 22).
   Benchmark: GitLab and Grafana have a user name next to the email (not
   fetched). The pattern is the subject rule of `principal.ValidateSubject`,
   with the letters in lowercase.
4. **The last credential stays.** The server refuses a change that leaves an
   enabled local user with no password and no passkey
   (`localuser.ErrLastCredential`). An administrator who wants to stop the
   sign-in of a person disables the principal.
5. **The first password comes from a set-up code (product manager).** An
   administrator who makes a local user receives a one-use set-up code, not a
   password field. The person opens the set-up page with the code and
   chooses the password, or adds a passkey. So no administrator ever knows
   the password of a person.
   Benchmark: OWASP Authentication Cheat Sheet (fetched): the application
   verifies the current password, or the person is "authenticated with an
   active session", before a password change. A password that an
   administrator chooses is a shared secret.

### Password storage

6. **Argon2id (product manager).** The server hashes each password with
   Argon2id (RFC 9106) from `golang.org/x/crypto/argon2`. The module is in
   `go.mod` already. The change makes it a direct requirement. The server
   uses no other password hash function.
   Benchmark: the OWASP Password Storage Cheat Sheet (fetched): "Use
   Argon2id with a minimum configuration of 19 MiB of memory, an iteration
   count of 2, and 1 degree of parallelism." Keycloak uses Argon2 by default
   outside FIPS mode (fetched, from a summary of the server admin guide).
   GitLab uses bcrypt with a cost of 10, and PBKDF2-SHA512 in FIPS mode
   (fetched). bcrypt reads at most 72 bytes of a password, so it does not
   meet decision 10.
7. **The parameters (product manager).** The defaults are 19456 KiB of
   memory, 2 iterations, 1 lane, a salt of 16 bytes from `crypto/rand` and a
   key of 32 bytes. These are the second minimum configuration of OWASP, with
   the lowest memory that has 2 iterations.
   Benchmark: OWASP (fetched) lists five configurations of equal strength,
   from 46 MiB and 1 iteration to 7 MiB and 5 iterations. NIST SP 800-63B-4
   asks for a salt of at least 32 bits (fetched, from a summary).
8. **The stored form records its parameters (product manager).** The store
   keeps each hash as a PHC string, for example
   `$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>`. So a later change of the
   defaults applies to each new hash. At each good sign-in, the server
   compares the parameters of the stored hash with the current ones. When
   they differ, it stores a new hash of the same password, with a
   compare-and-swap on the old hash and an audit entry. The server reads a
   stored hash as untrusted input: it refuses a hash whose parameters are
   outside fixed bounds (7 MiB to 256 MiB, 1 to 16 iterations, 1 to 4
   lanes), so that a changed row cannot make the server spend unbounded
   memory or time.
   Benchmark: OWASP (fetched): "wait until the user next authenticates,
   then re-hash their password with the new work factor."
9. **A limit on hash work (product manager).** Each instance runs at most 4
   password hashes at the same time. A request over that limit waits at
   most 2 seconds and then receives `429`. With the defaults, 4 hashes use about
   76 MiB. The setting `password.maxConcurrentHashes` holds the limit.
   Benchmark: OWASP (fetched) warns that a memory-hard hash with a high cost
   lets an attacker use the sign-in to exhaust the memory of the server.
   The value is a product manager decision with no outside number.

### Password rules

10. **Length, not composition (product manager).** A password has at least
    15 characters by default, and at most 256. The server counts Unicode
    code points after the NFC form, and hashes the NFC form. It refuses a
    password that is not valid UTF-8, that holds a control character, or
    that is longer than 1024 bytes. It never truncates a password. The
    setting `password.minLength` takes 12 to 64. A lower value is a weaker
    setting (ADR 212 decision 3). The server sets no rule on upper case,
    digits or symbols, and never asks for a periodic change.
    Benchmark: NIST SP 800-63B-4 section 3.1.1.2 (fetched): a minimum of 15
    characters for a password that is the only factor, and of 8 for a
    password that is only part of MFA. "SHOULD permit a maximum password
    length of at least 64 characters". "SHALL NOT impose other composition
    rules". "SHALL NOT require subscribers to change passwords
    periodically". The verifier applies NFC before it hashes, and verifies
    "the entire submitted password". OWASP Authentication (fetched): weak
    below 15 characters without MFA. PCI DSS v4.0.1 requirement 8.3.6 asks
    for at least 12 characters (not fetched), so the floor of the setting is
    12. GitLab: 8 by default, 8 to 128 (fetched). Grafana: 4 by default, and
    12 with uppercase, lowercase, digits and symbols when its password policy
    is on (fetched). MFA is off by default (ADR 46 decision 5), so a password
    is the only factor by default, and 15 applies.
11. **A blocklist in the binary (product manager).** The server refuses a
    password that is on a list of common and breached passwords. The list is
    a file in the binary. A check never calls a service outside the
    installation (ADR 74). The server compares after NFC and a lowercase
    fold. It also refuses a password that holds the user name, the name
    of the person, the local part of the email or the product name, and a password
    that is one character again and again or a plain sequence.
    Benchmark: NIST SP 800-63B-4 (fetched): "verifiers SHALL compare the
    prospective secret against a blocklist that contains known commonly
    used, expected, or compromised passwords", including the passwords
    of earlier breach corpuses. GitLab (fetched): "Must not match a list
    of 4,500+ known, breached passwords" and "Must not contain a predictable
    word (for example, gitlab or devops)". OWASP (fetched) names an online
    breach service. OpenDustpan sends no data out, so the list ships with
    the binary.
12. **One error text for each rule.** The API answers a refused password
    with `400` and one code for each rule (`password.too-short`,
    `password.too-long`, `password.form`, `password.blocked`). The text
    names the rule and the number, for example "Use 15 characters or more."
    It never shows the password.

### Sign-in, rate limits and lockout

13. **One answer for each failure (product manager).** A password sign-in
    answers `401` with one code and one text, "The user name or the password
    is wrong.", for an unknown user name, a wrong password, a locked
    name, a disabled principal and a user with no password. For an unknown
    name and a user with no password, the server checks the password against
    a fixed hash of the current parameters, so that the time of the answer
    is the same. The audit entry of the failure records the real reason.
    The response of the account set-up and of a reset never tells that a
    user name exists.
    Benchmark: OWASP Authentication (fetched): "respond with a generic error
    message regardless of whether" the user ID or the password is wrong,
    the account does not exist, or the account is locked or disabled.
14. **A lockout by name, shared by each instance (product manager).** 10
    wrong passwords in a row lock a user name for 30 minutes, or until an
    administrator unlocks it. The count lives in the database, behind the
    new port `authgate.NameLockCounter`, so every instance counts in one
    count. The key is an HMAC-SHA-256 of the folded user name under a key of
    the installation. So the table holds no user name, and an unknown name
    locks the same as a known name. The server adds the failure before it
    checks the password, and clears the count after a good password. So of
    attempts at the same time, no more than 10 pass. When the count cannot
    be written, the sign-in fails closed.
    Benchmark: PCI DSS v4.0.1 requirement 8.3.4: a lockout after not more
    than 10 attempts, for at least 30 minutes or until a person checks the
    identity (not fetched, quoted by RFC 21 P1). NIST SP 800-63B-4 (fetched,
    from a summary): no more than 100 failures in a row. RFC 21 P1 gives the
    same numbers to LDAP, so one rule covers both password methods.
15. **A limit by source as well (product manager).** The password sign-in
    has its own scope in the source budget of `authgate` (`password`), apart
    from the token lookups. A lockout by name alone lets one source try one
    password against many names. The source budget stops that.
    Benchmark: RFC 21 P1 (the passkey sign-in counts by client address).
    OWASP Authentication (fetched) ties the counter to the account, not to
    the source address. OpenDustpan keeps both.
16. **A lock is visible and ends with a record.** An administrator sees the
    lock state of a local user in the API, and can unlock it with a reason.
    The lock, the unlock and the end of a lock each write an audit entry.
    A lock by name also writes an alert to the administrators at most once
    for each name in each hour, so that a lockout attack against a person is
    visible.

### Reset and change

17. **A reset code that an administrator makes (product manager).** An
    administrator starts a reset for a local user. The server makes a code
    of 32 random bytes, shows it once to the administrator, and stores only
    its SHA-256 digest. The administrator gives the code to the person on
    another channel. The code works once. It ends after 1 hour by default.
    The setting `password.resetLifetime` takes 10 minutes to 24 hours. A new
    code replaces the earlier code of the same person. The use of the code
    ends every session of the person. Factors, recovery codes and access
    tokens stay. The reset needs `installation:manage-sign-in`, a fresh
    authentication, a reason, and a reach that covers the reach of the
    person (ADR 70 decision 22). The server refuses a reset of the own
    account.
    Benchmark: NIST SP 800-63B-4 (fetched, from a summary): a recovery
    secret has at least 64 bits and the store keeps it hashed. The code has
    256 bits. RFC 21 P7 gives the same rights and checks to an MFA reset.
18. **Email only with a mail sender (product manager).** When the
    installation has a mail sender (the SMTP settings of ADR 53 and ADR
    212), the administrator can send the reset link to the email of the
    person. A self-service reset ("forgot password") exists only with a mail
    sender. Its answer is the same for each user name, and it counts in the
    source budget of decision 15. Without a mail sender, only the
    administrator path exists.
19. **A change needs the current password or a fresh authentication.** A
    person changes the own password with the current password, or in a
    session with a fresh authentication (ADR 227). A wrong current password
    counts in the lockout by name. A change ends every other session of the
    person and keeps the session of the change.
    Benchmark: OWASP Authentication (fetched): "Current password
    verification."

### MFA

20. **A passkey or TOTP as the second factor.** When built-in MFA is on
    (ADR 46), a password sign-in makes a pending sign-in, not a session
    (RFC 21 P7). The person then gives a passkey (user verification
    `preferred`), a TOTP code, or a recovery code. A passkey sign-in with a
    discoverable credential and user verification `required` is a sign-in
    of its own and needs no password. The server records it as a sign-in
    with two factors.
    Benchmark: NIST SP 800-63B-4 counts a multi-factor cryptographic
    authenticator as two factors (not fetched). RFC 21 P7 gives the same
    user verification values.
21. **"Require MFA" applies to the password (owner, through RFC 21 P7).**
    The installation setting `mfa.requireForAll` makes each password
    sign-in ask for a second factor. A person with no factor receives a session
    that can only enroll a factor and sign out, after the grace of
    `mfa.enrollmentGraceDays`. A passkey sign-in meets the setting alone.
    The bootstrap admin token stays outside the setting (RFC 21 O12).
22. **A shorter minimum with MFA stays a choice.** NIST allows 8 characters
    for a password that is only part of MFA. The server does not lower the
    minimum on its own when `mfa.requireForAll` is on, because the setting
    can change later and the stored passwords stay. An administrator can set
    `password.minLength` to 12 in that case.

### Audit

23. **Each step writes an audit entry, in the transaction of the change.**
    The entries are `local-user.created`, `local-user.deleted`,
    `local-user.renamed`, `password.set`, `password.changed`,
    `password.removed`, `password.rehashed`, `password.reset-created`,
    `password.reset-used`, `password.reset-sent`, `sign-in.password-failed`
    (with the real reason), `sign-in.name-locked`, `sign-in.name-unlocked`,
    `passkey.added`, `passkey.removed`, `passkey.suspended` and
    `sign-in.succeeded` (with the provider `local`, the factors and the
    times). No entry holds a password, a hash, a reset code or a key.

### Demo sign-in

24. **A demo person, only on a dev installation (owner, product manager).**
    `opendustpan dev` takes a new flag, `--demo`. This amends ADR 237
    decision 3. With `--demo`, `dev` makes the person `dev:demo` at the
    first start, with the local user name `demo`, a password of 4 random
    words from `crypto/rand`, and a stored grant of `site-administrator`
    with the creator `system` and the reason "demo user of opendustpan dev".
    The sign-in page of the console shows the user name and the password,
    from the public route `GET /api/dustpan/v1/sign-in/demo`. The banner of
    `dev` prints them too. A visitor signs in as a person, so each feature
    that needs a person works. `make demo` changes to `opendustpan dev
    --demo`.
    Benchmark: Grafana `admin`/`admin` and the demo of Vault `-dev` show
    the first credential to the person who started the server (not fetched).
    ADR 237 decision 12 shows a one-time code for the dev admin.
25. **The demo person cannot change its credentials.** The server refuses a
    change of the password, the user name, the passkeys and the factors of
    the demo person (`localuser.ErrDemoFixed`). The lockout by name does not
    apply to it, so one visitor cannot lock out the others. The source
    budget of decision 15 still applies. This is a rule of the code. The
    catalog of code rules lists it.
26. **Hard checks against a demo on a normal installation.** Each check
    stops a demo sign-in alone:
    - Only the wiring of `dev` in `cmd/opendustpan` passes the demo option.
      `serve` has no flag, no variable and no configuration key for it, and
      `pkg/dustpan` has no option for it, as for the one-time code of ADR
      237 decision 12.
    - The server refuses to start with the demo option when the installation
      kind of the database is not `dev`.
    - The demo route and the demo seed check the installation kind again at
      each call, and refuse with `localuser.ErrDemoOnly` on another kind.
    - The demo person has a `dev:` subject. A server of the kind
      `production` refuses to authenticate it (ADR 253 decision 13), and a
      restore into production removes it (ADR 253 decision 6).
    - The local user service refuses a password sign-in for a record with
      `Demo` set, or for a principal with a `dev:` subject, on an
      installation that is not of the kind `dev`.
    - The route test lists the demo route as public only with the option of
      `dev`, and a test fails when `serve` registers it.

### Defense in depth for each input

27. **The layers.** Each input that comes from outside the process has its
    layers (ADR 250). Each layer has its own test that sends hostile input
    to that layer alone.

    | Input | 1. Boundary | 2. Encode or contain | 3. Limit | 4. Authorization at use | 5. Fail closed |
    | --- | --- | --- | --- | --- | --- |
    | User name | pattern, 64 characters, strict JSON decoder | SQL placeholders, HMAC key for the lock table, CR and LF removed in logs | lockout by name, source budget | the record belongs to the person of the session | unknown name answers as a wrong password |
    | Password | valid UTF-8, no control character, 1024 bytes, 256 code points | NFC, never logged, never in an audit entry | hash limit of decision 9, lockout | the hash belongs to the record of the user name | hasher error refuses the sign-in |
    | Stored hash (a database row) | PHC parse, algorithm allowlist | constant-time compare of the key | parameter bounds of decision 8 | the hash row of the record that the name found | parse error refuses, with an audit entry |
    | Reset code | 43 base64url characters | stored digest only | one use, 1 hour, source budget | the code names one record, and the reset does not change another | unknown or expired code refuses |
    | Passkey ceremony | as RFC 21 P7 | origin and RP ID from the server URL | 300 seconds, one use, 20 passkeys | the passkey belongs to the person of the ceremony | counter regression suspends |
    | Demo option | a flag of `dev` only | no setting, no variable | `dev:` subject | kind check at start and at each call | another kind refuses to start |
    | Blocklist file | fixed in the binary | compared after NFC and fold | none needed | none needed | a list that cannot load refuses each new password |

### Ports

28. **The ports of this ADR.** `pkg/localuser` holds `Store`, `ResetStore`,
    `PasswordHasher` and `Blocklist`, with their errors and defaults.
    `pkg/mfa` holds `PasskeyStore`, `RecoveryCodeStore`, `PendingStore` and
    the MFA policy. `internal/authgate` holds `NameLockCounter`. The TOTP
    factor stays in `internal/stepup` (`TOTPStore`), shared with the CLI
    step-up (RFC 21 O13). Each port has a conformance suite whose cases skip
    until the first adapter lands. The services go to `internal/localusersvc`
    and `internal/mfasvc`.
29. **Actions.** The administrator routes (create, reset, unlock, removal,
    the password settings) use `installation:manage-sign-in` (ADR 70
    decision 11, with the name of ADR 210). A person changes the own
    password and factors with `sign-in:manage-own`. The constants land with
    the routes, so that each action has a route in the route test.

## Not decided here

- A pepper. OWASP (fetched) says "Consider using a pepper". A pepper from
  the key-encryption key makes a rotation of that key reset each password.
  The product manager keeps it out of milestone 4 and asks the owner.
- PCI DSS v4.0.1 requirement 8.3.6 also asks for letters and digits (not
  fetched). Decision 10 follows NIST and sets no composition rule. Whether
  an opt-in setting for that rule exists is open.
- The source and the license of the blocklist file. A list of 100,000
  passwords from a breach corpus under a license that `make licenses` and
  `make trivy` accept is the proposal. The work item names the file before
  it lands.
- Whether an administrator reset also revokes the access tokens of the
  person. Decision 17 keeps them, as ADR 46 decision 8 keeps tokens as
  bearer tokens.
- The role of the demo person. `site-administrator` lets a visitor change
  the settings of the demo installation. A public demo may need a smaller
  role.

## Consequences

- ADR 55 decision 5 now reads: a local user signs in with a passkey alone,
  with a user name and a password, or with both. ADR 46 decision 1 covers
  local users with a password.
- ADR 237 decision 3 adds `--demo` to the flags of `dev`.
- The question O1 of RFC 21 closes with "no". The work item for local
  passwords joins the work items of RFC 21: the store migrations of both
  dialects, the Argon2id hasher, the blocklist, the routes, the console
  pages, the e2e step and the user documentation page.
- `golang.org/x/crypto` becomes a direct requirement.
- The RFC 21 work items for WebAuthn (W3), recovery codes (W6) and built-in
  MFA (W7) implement the ports of `pkg/mfa`.
