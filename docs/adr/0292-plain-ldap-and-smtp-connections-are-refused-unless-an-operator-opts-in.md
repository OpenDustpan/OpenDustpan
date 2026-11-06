# 292. Plain LDAP and SMTP connections are refused unless an operator opts in

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026. Answers question O3 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md) with "yes", with an opt-in

## Context

An LDAP bind sends a service account password. An SMTP login sends the SMTP password and the email. Both cross the network in clear text when the connection has no TLS. RFC 21 question O3 asks whether the server refuses such a connection, except to a loopback address.

Some operators run a trusted network where TLS is not in use. They need a way to say so.

## Decision

1. **Refused by default (owner).** The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)
2. **An explicit opt-in (owner).** An installation can allow plain connections with one setting. The setting is off by default. (owner)
3. **The setting is visible (owner).** The API shows its effective value and its source. Each change writes an audit entry. The console shows the state in a quiet way, in the settings and in the status of the connection. It shows no warning banner. (owner)
4. **The hosted service never turns it on (owner).** The hosted service does not allow the setting. (owner)
5. **Fail closed.** If the setting is missing or not valid, the value is off. A refusal names the address, the setting to change and the one command or call that changes it.
6. **TLS checks stay.** A connection with TLS keeps the certificate checks of RFC 21. The opt-in does not turn off a certificate check.

## Consequences

- RFC 21 marks question O3 as answered by this ADR.
- The LDAP adapter and the SMTP sender read the setting at the point of use. A test sends a plain connection with the setting off, and expects a refusal.
- The settings resource gets the new key. The user documentation says what the setting risks.
