## Context

`internal/ldapauth`, `internal/signinsvc` (`CompletePassword`, `BindGate`) and
`session.PasswordMethod` exist and have tests. PR #2953 merged the service. PR
#2968 (stacked on #2981) is the wiring. This change is the spec of that wiring and
adds the lockout setting. The gap before it: no provider settings, no method built
from them, no route.

## Decisions

1. **One static provider, set at start.** The settings come from flags, variables
   and file keys, with one precedence rule for every setting. `dustpan.WithLDAPProvider`
   builds the `ldapauth.Method`. The server checks the provider at start and fails
   the start on an invalid setting. So a running server never holds an invalid
   provider. The stored provider (issue #2950) is a later change and reuses the
   same method constructor.
2. **The password has one way in.** The bind password is a `session.Password`.
   Its `String` and `GoString` hide the value. The settings struct that the API
   shows has `bindPasswordSet` and no field for the password. So no format, log or
   API answer can print it: the type does not hold it in a printable form. The
   serve command clears the variable after the read.
3. **One check for every password sign-in route.** The local route and the LDAP
   route call `sessionauth.Checker.SignInFromBody` before the budget. A new
   password route gets the same helper, so login CSRF is closed in one place.
4. **The plain-connection check stays at the point of use.** The method reads
   `egress.allowPlainConnections` at each sign-in. No cache holds the value.
5. **Equal cost for an unknown user.** When the search finds no entry, the method
   sends one bind with a random DN and a random password, and `signinsvc` adds the
   same name failures as for a known person. So the binds and the store writes of an
   unknown user equal those of a wrong password. PR #2968 measured the gap on
   PostgreSQL and SQLite: below 0.6 ms.
6. **The lockout limit is one setting with a shown source.** `ldap.subjectFailureLimit`
   is 5 by default, 1 to 100. The provider list shows its effective value and its
   source (default, file, variable or flag). A value outside the range stops the
   start. No new tooling.
7. **Private directories need a named range.** `--ldap-allowed-ranges` opens
   private ranges only (at least /8 for IPv4, /32 for IPv6). Loopback, link-local
   and metadata addresses never open. The server checks each range again at start.

## Risks

- A wrong default makes a plain connection possible. The default of the setting is
  off, and the hosted service locks it off (ADR 292). The whole-server test flips
  the setting between sign-ins.
- A limit of 5 lets anyone who knows a user name lock that person out of LDAP
  sign-in from one network (ADR 320 decision 20). The guide says so and names the
  administrator unlock, which is a later change. Until then, the lock ends by time.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Settings (cmd) | Allowlist of each setting; password only from variable or file; owner-only password file | No URL, no password, a password file that others can read, a bad mask, a CA file with no certificate, a range too wide, a loopback range, a limit of 0 and 101 |
| Settings (SDK) | `NewServer` checks each provider again | `http` public URL, URL with a path or user information, StartTLS with `ldaps`, filter with no `{user}`, reserved IDs, an OIDC ID |
| Connection | Resolved address check, egress rules, TLS or the opt-in | Plain connection with the opt-in off, a refused address |
| Method | Empty password refused before any bind, filter shape check, no referral | `*)(uid=*`, empty password, a referral answer |
| Handler | `SignInFromBody`, body limit, budget before the body, one answer for each failure | text/plain from another site, foreign Origin, Origin `null`, 9 KiB body, unknown provider |
