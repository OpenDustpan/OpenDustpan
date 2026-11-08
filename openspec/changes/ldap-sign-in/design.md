## Context

`internal/ldapauth` (method, config, connection), `internal/signinsvc`
(`CompletePassword`) and `session.PasswordMethod` exist and have tests. The gap is
the wiring: no provider settings, no method built from them, no route.

## Decisions

1. **One constructor from settings to method.** A provider of the mode `ldap`
   stores `session.LDAPSettings`. One function turns the stored settings into an
   `ldapauth.Config` and then into a `Method`. `ldapauth.NewMethod` stays the one
   place that validates the config (`checkConfig`). The settings struct has no
   field that `ldapauth.Config` does not have, so the two cannot drift. A new
   `Config` field that the settings lack fails a test that compares the field
   lists. This makes "the setting exists but nothing uses it" impossible, which
   is the bug this change fixes.
2. **The method set is built from the stored providers, not from flags.** Each
   enabled provider gets one `Method`. A provider change builds that method
   again. The route looks up the method by provider ID, so a route can reach only
   a method that a stored provider made. A provider that is off has no method, and
   the route answers as for an unknown provider.
3. **The plain-connection check stays at the point of use.** The setting is read
   on each connection in `ldapauth`. The wiring passes a reader of the setting,
   not its value. No cache holds the value, so a change of the setting acts on the
   next connection.
4. **No new tooling.** The test directory for e2e is a container image in the
   existing e2e harness, started as the other fakes are. No new dependency.

## Risks

- A wrong default makes a plain connection possible. The default of the setting is
  off, and the hosted service locks it off (ADR 292). The route test covers both.
- The password reaches `ldapauth` only as `session.Password`, which does not print.
  Logs and audit entries never hold it. A hostile test checks the handler logs.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Settings | Allowlist of each field at the provider route and in `NewMethod` | Filter without `{user}`, URL with a path, user information in the URL, bind password in the body |
| Connection | Resolved address check, egress rules, TLS or the opt-in | URL that resolves to a refused address, plain connection with the opt-in off |
| Method | Empty password refused before any bind, filter shape check, no referral | `*)(uid=*` as the user name, empty password, a referral answer |
| Handler | Body size limit, one answer for each failure, no secret in logs | Oversize body, unknown provider, wrong password |
