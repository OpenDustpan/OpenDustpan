# 285. Local users sign in with a password and with a passkey

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026, with the decisions of the PM under the owner's delegation marked below. Answers question O1 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md) with "no" (option B). Amends ADR 55 (decision 5), ADR 46 (decisions 1 and 2) and ADR 237 (decision 3). Decisions 5, 17, 19, 20 and 21 changed by ADR 297: "end every session" is a bump of the session generation. A good second factor sets cookies

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

The owner delegates the decisions of detail in this theme to the product
manager (PM). Each such decision has the label "(PM decision under the owner's
delegation)" and a "Benchmark:" line. The marks "(fetched)" and "(not fetched)" have the meaning
of RFC 21: "(fetched)" means that the author fetched the source page on
2026-10-31. "From a summary" means that the fact comes from a summary of a
fetched page, not from a word-for-word read, so this ADR does not quote it. "(not fetched)" means that the fact comes from RFC 21 or the
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

   The installation setting `signIn.localPassword.enabled` turns the
   password sign-in on or off. When an operator does not set it, its value
   comes from the configuration: on when no SSO identity provider is
   configured, and off when one is. The API shows the effective value and
   where it comes from (`default` or `set`). Each change writes an audit
   entry, and a change from off to on is a weaker setting (ADR 212 decision
   3).

   A change of the SSO configuration can change the effective value when
   the operator did not set it. Then the same transaction as the SSO change
   writes `sign-in.local-password-setting-changed`, with the old value, the
   new value and the cause (the SSO provider that the change adds or
   deletes). The answer of the SSO change names the new effective value. An
   SSO change that turns the effective value from off to on, for example
   the removal of the last SSO provider, goes through the check of a weaker
   setting: it needs a fresh authentication, as an explicit change does.
   One function computes the effective value from the stored setting and
   the SSO configuration. The setting route and each SSO change call that
   function, so the two paths cannot give different values.
   While it is off, the password sign-in, the set-up page and a reset
   answer `localuser.ErrPasswordSignInOff`. The passkey sign-in of a local
   user does not depend on it. While an SSO provider exists and the setting
   is on, each local password user needs a second factor, with the grace of
   `mfa.enrollmentGraceDays`, whatever the value of `mfa.requireForAll`
   (decision 21). This amends ADR 46
   decision 1, which covers installs with no SSO provider only, and ADR 46
   decision 2, which now covers the persons who sign in through the SSO
   provider only. So a local password account cannot skip the MFA of the
   installation because an SSO provider exists.
   Benchmark: the review of this design found that a local password next to
   SSO skips the MFA and the offboarding of the identity provider (ADR 70
   decision 19). GitLab and Grafana have a switch for the password form
   (`disable_login_form` in Grafana, fetched).
2. **The provider ID `local` (PM decision under the owner's delegation).** Each session of a local
   user sign-in records the provider `local`. No configured identity
   provider can take that ID. The subject of a local user is
   `local:<record ID>`, and the record ID never changes. The API refuses
   each other subject that starts with `local:`. So a change of the
   user name changes no subject, no grant and no audit reference.
   Benchmark: ADR 70 decision 22 (no link by an attribute that can change).
3. **The user name (PM decision under the owner's delegation).** A user name has 1 to 64 characters
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

   A break-glass person never has a password. The service refuses a
   password, a set-up code and a reset for a break-glass person
   (`localuser.ErrBreakGlass`), and the store refuses the same writes again
   in its own transaction, after it reads the break-glass flag of the
   principal. The reverse holds too: the principal store refuses to set the
   break-glass flag on a person who has a password
   (`principal.ErrBreakGlassPassword`), under the same row lock of the
   principal. Today only the create of a principal sets the flag, so this
   check guards a later write that sets it on an existing person. The
   administrator deletes the password first. So a break-glass account keeps the phishing-resistant sign-in
   of ADR 70 decision 23, and no lockout by name can apply to it (RFC 21
   P1). Each write that changes the credentials of a person locks the row of
   the person principal first, in the local user store and in the passkey
   store. So a removal of the password and a removal of the last passkey at
   the same time cannot both pass.
5. **The first password comes from a set-up code (PM decision under the owner's delegation).** An
   administrator who makes a local user receives a one-use set-up code, not a
   password field. The port that makes a local user takes no password hash.
   Only the redeem of a set-up code or a reset code, a change by the person
   and the demo seed write a hash. The person opens the set-up page with the code and
   chooses the password, or adds a passkey. So no administrator chooses or
   sees the password of a person. The administrator who holds a set-up code
   or a reset code can use it until it is used or ends, so the server tells
   the person about each set-up and reset: by email when a mail sender
   exists, and on the page of the sessions of the person.
   Benchmark: OWASP Authentication Cheat Sheet (fetched): the application
   verifies the current password, or the person is "authenticated with an
   active session", before a password change. A password that an
   administrator chooses is a shared secret.

### Password storage

6. **Argon2id (PM decision under the owner's delegation).** The server hashes each password with
   Argon2id (RFC 9106) from `golang.org/x/crypto/argon2`. The module is in
   `go.mod` already. The change makes it a direct requirement. The server
   uses no other password hash function.
   Benchmark: the OWASP Password Storage Cheat Sheet (fetched): "Use
   Argon2id with a minimum configuration of 19 MiB of memory, an iteration
   count of 2, and 1 degree of parallelism." Keycloak uses Argon2 by default
   outside FIPS mode (from a summary of the server admin guide, not a quote).
   GitLab uses bcrypt with a cost of 10, and PBKDF2-SHA512 in FIPS mode
   (fetched). bcrypt reads at most 72 bytes of a password, so it does not
   meet decision 10.

   The server adds no pepper to the hash (PM decision under the owner's delegation). A pepper from
   the key-encryption key makes each rotation of that key reset each
   password. Benchmark: OWASP Password Storage (fetched) says "Consider using a
   pepper", and that "alone, it provides no additional secure
   characteristics".
7. **The parameters (PM decision under the owner's delegation).** The defaults are 19456 KiB of
   memory, 2 iterations, 1 lane, a salt of 16 bytes from `crypto/rand` and a
   key of 32 bytes. These are the second minimum configuration of OWASP, with
   the lowest memory that has 2 iterations.
   Benchmark: OWASP (fetched) lists five configurations of equal strength,
   from 46 MiB and 1 iteration to 7 MiB and 5 iterations. NIST SP 800-63B-4
   asks for a salt of at least 32 bits (from a summary, not a quote).
8. **The stored form records its parameters (PM decision under the owner's delegation).** The store
   keeps each hash as a PHC string, for example
   `$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>`. So a later change of the
   defaults applies to each new hash. At each good sign-in, the server
   compares the parameters of the stored hash with the current ones. When
   they differ, it stores a new hash of the same password, with a
   compare-and-swap on the old hash and an audit entry. The server reads a
   stored hash as untrusted input: it refuses a hash whose parameters are
   outside fixed bounds (7 MiB to 64 MiB, 1 to 16 iterations, 1 to 4
   lanes, so a parallelism of 0 is refused before any work), so that a changed row cannot make the server spend unbounded
   memory or time.
   Benchmark: OWASP (fetched): "wait until the user next authenticates,
   then re-hash their password with the new work factor."
9. **A limit on hash work (PM decision under the owner's delegation).** The
   password hashes of one instance share a memory budget of 80 MiB by
   default. Each hash takes its own memory cost from the budget, so four
   default hashes fit, and one hash at the bound of 64 MiB takes most of
   it. A request that finds no room waits at most 2 seconds and then
   receives `429`. The setting `password.hashMemoryBudgetKiB` holds the
   budget, at least 64 MiB, so one valid hash always fits. The source budget
   of decision 15 runs before the hash, so one source cannot fill the
   budget. A sign-in that receives `429` from the budget counts no name
   failure and no factor failure: the server adds the failure only after
   the hash has its budget, and before the compare. So an attacker who fills
   the budget cannot add failures to the name of a real person. A distributed attacker can still fill it, and then each password
   sign-in answers `429` for a time. The passkey sign-in needs no hash, so
   it stays available. The guide states this limit.
   Benchmark: OWASP (fetched) warns that a memory-hard hash with a high cost
   lets an attacker use the sign-in to exhaust the memory of the server.
   The value is a PM decision with no outside number.

### Password rules

10. **Length, not composition (PM decision under the owner's delegation).** A password has at least
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

    An opt-in setting, `password.requireLettersAndDigits`, makes each new
    password hold at least one letter (Unicode category L) and one digit
    (Unicode category Nd). It is off by default, and the API shows its
    effective value. A change from on to off is a weaker setting. It applies
    to new passwords only. It is for a team that designs against PCI DSS
    v4.0.1 requirement 8.3.6, which asks for letters and digits (not
    fetched). The default stays the rule of NIST.
11. **A blocklist in the binary (PM decision under the owner's delegation).** The server refuses a
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

    The list is `Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt`
    of SecLists (`github.com/danielmiessler/SecLists`), under the MIT
    license (fetched). Its name gives 100,000 passwords. The work item
    counts the lines of the file that it adds, pins the commit of SecLists,
    and ships the MIT license text with the file, so that `make licenses`
    and `make trivy` see it. A new version of the list is a reviewed change,
    as for a module.
12. **One error text for each rule.** The API answers a refused password
    with `400` and one code for each rule (`password.too-short`,
    `password.too-long`, `password.form`, `password.blocked`). The text
    names the rule and the number, for example "Use 15 characters or more."
    With `password.requireLettersAndDigits` on, the code
    `password.letters-and-digits` names that rule.
    It never shows the password.

### Sign-in, rate limits and lockout

13. **One answer for each failure (PM decision under the owner's delegation).** A password sign-in
    answers `401` with one code and one text, "The user name or the password
    is wrong.", for an unknown user name, a wrong password, a locked
    name, a disabled principal and a user with no password. For an unknown
    name and a user with no password, the server checks the password against
    a fixed hash of the current parameters, so that the time of the answer
    is the same. The server checks the state of the principal (disabled,
    locked) only after a full check of the password, so that the time does
    not tell that state either. The audit entry of the failure records the
    real reason.
    The response of the account set-up and of a reset never tells that a
    user name exists.
    Benchmark: OWASP Authentication (fetched): "respond with a generic error
    message regardless of whether" the user ID or the password is wrong,
    the account does not exist, or the account is locked or disabled.
14. **A lockout by name, shared by each instance (PM decision under the owner's delegation).**
    The counts live in the database, behind the new port
    `authgate.NameLockCounter`, so every instance counts in one count. Three
    counts apply:
    - **Name and source.** 10 wrong passwords in a row for one user name
      from one source prefix (an IPv4 address, or the /64 of an IPv6
      address) lock that pair for 30 minutes (scope `password`). So a guess
      from one network cannot lock the person out from another network.
    - **Name from every source.** 100 wrong passwords in a row for one user
      name, from any source, lock the name for every source (scope
      `passwordname`). The first lock lasts 30 minutes. Each new global lock
      of the same name doubles the time, up to 24 hours. After 24 hours with
      no global lock, the next lock lasts 30 minutes again. This is the
      ceiling of NIST, and it stops a distributed guess.
    - **Second factor of the person.** 10 wrong second factors in a row for
      one person, across every pending sign-in, lock the factors of the
      person for 30 minutes (scope `factor`, keyed by the principal ID).
      So a known password does not give unlimited guesses of a TOTP code.
      A factor lock probably means that someone knows the password, so the
      server notifies the person (by email when a mail sender exists, and on
      the page of the sessions of the person) and offers a password change.

    Each count has a name and a sub-key. The name of a name count is an
    HMAC-SHA-256 of the folded user name under a key of the installation.
    So the table holds no user name, and an unknown name locks the same as a
    known name. The sub-key of `password` is the source prefix. So an
    administrator can list and clear every count of one name in one call. The server adds each
    failure before it compares the credential. Only the end of a full
    sign-in, after the second factor when one is needed, clears counts: the
    count of its own source prefix, the global count of the name, and the
    factor count of the person. It clears them after the commit of the
    session, and a failed clear leaves the counts, which is the safe
    direction. The counts of other prefixes end by their time or by an
    administrator unlock, which clears every count of the name. A good
    password alone clears nothing. So of
    attempts at the same time, no more than the limit pass. When a count
    cannot be written, the sign-in fails closed. The discoverable passkey
    sign-in reads no name lock and no factor lock, so a person with a
    passkey can always sign in while a guess locks the name or the factors.
    A passkey as a second factor after a password reads the factor lock,
    as the other factors do.

    The risk that stays: an attacker with 10 or more networks can keep a
    password-only user out of the password sign-in for up to 24 hours at a
    time, and lock the name again after each lock ends. The ways out are a
    passkey, which reads no name lock, and an administrator unlock. The
    immediate alert of decision 16 tells the administrators. The guide says
    so, and advises a passkey for each administrator.
    Benchmark: PCI DSS v4.0.1 requirement 8.3.4: a lockout after not more
    than 10 attempts, for at least 30 minutes or until a person checks the
    identity (not fetched, quoted by RFC 21 P1). NIST SP 800-63B-4 (from a
    summary, not a quote): no more than 100 failures in a row. RFC 21 P1
    counts a factor after a password "on the pending sign-in and on the
    person, with the same limit". RFC 21 P1 gives the same numbers to LDAP,
    so one rule covers both password methods.
15. **A limit by source as well (PM decision under the owner's delegation).** The password sign-in
    has its own scope in the source budget of `authgate` (`password`), apart
    from the token lookups. A lockout by name alone lets one source try one
    password against many names. The source budget stops that.
    Benchmark: RFC 21 P1 (the passkey sign-in counts by client address).
    OWASP Authentication (fetched) ties the counter to the account, not to
    the source address. OpenDustpan keeps both.
16. **A lock is visible and ends with a record.** An administrator sees the
    lock state of a local user in the API, and can unlock it with a reason.
    The lock, the unlock and the end of a lock each write an audit entry.
    A lock by name or by factor also writes an alert to the administrators
    at most once for each name in each hour, and the global lock of a name
    writes an alert at once, so that a lockout attack against a person is
    visible.

### Reset and change

17. **A reset code that an administrator makes (PM decision under the owner's delegation).** An
    administrator starts a reset for a local user. The server makes a code
    of 32 random bytes, shows it once to the administrator, and stores only
    its SHA-256 digest. The administrator gives the code to the person on
    another channel. The code works once. It ends after 1 hour by default.
    The setting `password.resetLifetime` takes 10 minutes to 24 hours. A new
    code replaces the earlier code of the same person. The start of the
    reset always ends every session of the person. Factors and recovery
    codes stay. Access tokens stay by default. The reset request has the
    option `revokeAccessTokens`, false by default, which also revokes each
    access token of the person in the same transaction. The audit entry of
    the reset records the choice. The API shows the count of live access
    tokens of the person next to the option. The use of a code is one
    transaction: the store checks the expiry, deletes the code, sets the
    password, ends the sessions and appends the audit entry
    (`localuser.ResetStore.RedeemReset`). The service checks the password
    rules before that call, so a refused password never uses up a code. The
    server refuses a reset and a set-up code for a break-glass person
    (decision 4). The reset needs `installation:manage-sign-in`, a fresh
    authentication, a reason, and a reach that covers the reach of the
    person (ADR 70 decision 22). The server refuses a reset of the own
    account.
    Benchmark: NIST SP 800-63B-4 (from a summary, not a quote): a recovery
    secret has at least 64 bits and the store keeps it hashed. The code has
    256 bits. RFC 21 P7 gives the same rights and checks to an MFA reset.
    GitHub keeps the personal access tokens at a password reset (not
    fetched).
18. **Email only with a mail sender (PM decision under the owner's delegation).** When the
    installation has a mail sender (the SMTP settings of ADR 53 and ADR
    212), the administrator can send the reset link to the email of the
    person. A self-service reset ("forgot password") exists only with a mail
    sender. Its answer is the same for each user name, and it counts in the
    source budget of decision 15. The server sends the email in a durable
    operation after the answer, so the time of the answer does not tell that
    a name exists. Without a mail sender, only the administrator path
    exists.

    A reset link holds the code in the URL fragment, never in the path or
    the query, as the one-time code of ADR 237 decision 12 does. The set-up
    page sends the code in the body of a POST. The page has
    `Referrer-Policy: no-referrer`, and the server never logs a fragment or
    a body of these routes.
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

    Each wrong factor counts on the pending sign-in (5 make it void) and on
    the person (decision 14). A good factor turns the pending sign-in into a
    session in one transaction: the store deletes the pending sign-in,
    stores the session and appends the audit entry
    (`mfa.PendingStore.CompletePending`). The adapter writes the session
    with the same code as `session.Store.CreateSession`, so one rule set
    checks each session. So a failed session write leaves
    the pending sign-in as it was, and one pending sign-in makes one
    session only.
    Benchmark: NIST SP 800-63B-4 counts a multi-factor cryptographic
    authenticator as two factors (not fetched). RFC 21 P7 gives the same
    user verification values.
21. **"Require MFA" applies to the password (owner, through RFC 21 P7).**
    The installation setting `mfa.requireForAll` makes each password
    sign-in ask for a second factor. While an SSO provider exists and
    `signIn.localPassword.enabled` is on, each password sign-in asks for a
    second factor too, whatever the value of `mfa.requireForAll`
    (decision 1). A person with no factor receives a session
    that can only enroll a factor and sign out, after the grace of
    `mfa.enrollmentGraceDays`. A passkey sign-in meets the setting alone.
    The bootstrap admin token stays outside the setting (RFC 21 O12).

    A fresh authentication (ADR 227) of a local user is a passkey, or a
    password and a second factor. A password alone is never a fresh
    authentication when `mfa.requireForAll` is on, while an SSO provider
    exists, or when the person has a factor. Only a person with no factor, in
    an installation with no SSO provider where MFA is not required, receives a fresh authentication from the password alone, because
    no other step exists for that person.
22. **A shorter minimum with MFA stays a choice.** NIST allows 8 characters
    for a password that is only part of MFA. The server does not lower the
    minimum on its own when `mfa.requireForAll` is on, because the setting
    can change later and the stored passwords stay. An administrator can set
    `password.minLength` to 12 in that case.

### Audit

23. **Each step writes an audit entry, in the transaction of the change.**
    The entries are `local-user.created`, `local-user.deleted`,
    `local-user.renamed`, `password.set`, `password.changed`,
    `local-user.change-required`, `password.removed`, `password.rehashed`,
    `password.demo-seeded`, `password.reset-created`,
    `password.reset-used`, `password.reset-sent`, `sign-in.password-failed`
    (with the real reason), `sign-in.name-locked`, `sign-in.name-unlocked`,
    `sign-in.factor-locked`, `sign-in.local-password-setting-changed`,
    `passkey.added`, `passkey.removed`, `passkey.suspended` and
    `sign-in.succeeded` (with the provider `local`, the factors and the
    times). No entry holds a password, a hash, a reset code or a key.

### Demo sign-in

24. **A demo person, only on a dev installation (owner, and PM decision under the owner's delegation).**
    `opendustpan dev` takes a new flag, `--demo`. This amends ADR 237
    decision 3. With `--demo`, `dev` makes the person `dev:demo` at the
    first start, with the local user name `demo`, and these stored grants, each with the
    creator `system` and the reason "demo user of opendustpan dev":
    - `admin` (the namespace administrator role) on each namespace that
      `dev` and the demo seed make, for example `dev`.
    - `viewer` on the installation, so that the person reads the
      installation and changes no installation setting.

    The demo person never holds `site-administrator` or
    `account-administrator`.
    At each start, the seed makes a new demo password from `crypto/rand`: 5
    groups of 4 lowercase letters and digits, joined by hyphens (24
    characters). The seed checks it against the rules of decisions 10 and
    11, and makes a new one when a rule refuses it. It stores only the hash,
    through `localuser.PasswordStore.SeedDemoPassword`, which reads the
    installation kind from its own database in the same transaction and
    refuses another kind. The password stays in the memory of the process
    only. No file and no database row holds it. The sign-in page of the
    console shows the user name and the password, from the public route
    `GET /api/dustpan/v1/sign-in/demo`, which reads the memory of the
    process. The banner of `dev` writes them to the terminal writer, never
    to `slog` or a log sink. A test checks that the demo password is in no
    log record and no audit entry. A visitor signs in as a person, so each feature
    that needs a person works. `make demo` changes to `opendustpan dev
    --demo`.
    Benchmark: Grafana `admin`/`admin` and the demo of Vault `-dev` show
    the first credential to the person who started the server (not fetched).
    ADR 237 decision 12 shows a one-time code for the dev admin. The public
    demo of Grafana gives its visitors a limited role (not fetched).
25. **The demo person cannot change its credentials.** The server refuses a
    change of the password, the user name, the passkeys and the factors of
    the demo person (`localuser.ErrDemoFixed`). Only the seed sets its
    password, at each start of `dev`. The lockouts by name do not
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
      restore into production deletes it with its local user record (ADR
      253 decision 6). The coverage test of ADR 253 lists the local user
      table.
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
    | User name | pattern, 64 characters, strict JSON decoder | SQL placeholders, HMAC key for the lock table, no CR and no LF in logs | name and source lock, global name lock, source budget | the record belongs to the person of the session | unknown name answers as a wrong password |
    | Password | valid UTF-8, no control character, 1024 bytes, 256 code points | NFC, never logged, never in an audit entry | hash memory budget of decision 9, lockouts of decision 14 | the hash belongs to the record of the user name | hasher error refuses the sign-in |
    | Stored hash (a database row) | PHC parse, algorithm allowlist | constant-time compare of the key | parameter bounds of decision 8 | the hash row of the record that the name found | parse error refuses, with an audit entry |
    | Reset code | 43 base64url characters, in the fragment and the body only | stored digest only, `no-referrer` | one use, 1 hour, source budget | the code names one record, and not a break-glass person | unknown or expired code refuses, and the redeem is one transaction |
    | Second factor after a password | 6 digits, 16 base32 characters, ceremony JSON | stored digests, sealed secret | 5 on the pending sign-in, 10 on the person | the factor belongs to the person of the pending sign-in | store error refuses |
    | Passkey ceremony | as RFC 21 P7 | origin and RP ID from the server URL | 300 seconds, one use, 20 passkeys | the passkey belongs to the person of the ceremony | counter regression suspends |
    | Demo option | a flag of `dev` only | no setting, no variable, password in memory only and never in a log | `dev:` subject, no installation right but `viewer` | kind check at start, at each call and in the seed transaction | another kind refuses to start |
    | Blocklist file | fixed in the binary | compared after NFC and fold | none needed | none needed | a list that cannot load refuses each new password |

### Ports

28. **The ports of this ADR.** `pkg/localuser` holds `Store`,
    `PasswordStore`, `ResetStore`, `PasswordHasher` and `Blocklist`, with
    their errors and defaults. `pkg/principal` adds
    `ErrBreakGlassPassword`.
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

## Consequences

- ADR 55 decision 5 now reads: a local user signs in with a passkey alone,
  with a user name and a password, or with both. ADR 46 decision 1 covers
  each local user, also next to an SSO provider. ADR 46 decision 2 covers
  the persons who sign in through the SSO provider only.
- ADR 237 decision 3 adds `--demo` to the flags of `dev`.
- The question O1 of RFC 21 closes with "no". This change marks it so in
  the RFC. The work item for local
  passwords joins the work items of RFC 21: the store migrations of both
  dialects, the Argon2id hasher, the blocklist, the routes, the console
  pages, the e2e step and the user documentation page.
- `golang.org/x/crypto` becomes a direct requirement.
- The RFC 21 work items for WebAuthn (W3), recovery codes (W6) and built-in
  MFA (W7) implement the ports of `pkg/mfa`.
