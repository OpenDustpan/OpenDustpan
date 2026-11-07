# 333. Passkey challenges live in a sealed cookie, and name-lock alerts have a ceiling

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 285 decision 16: a ceiling of lock alerts for each installation and hour, then one summary alert.
- Extends ADR 285 decision 23: the audit kind `sign-in.name-lock-ended`.
- Extends ADR 285 decision 20: the challenge of a passkey sign-in lives in a sealed, short-lived cookie. A replay record is written only after a successful sign-in.
- Extends ADR 297 decision 4: the WebAuthn challenge of a passkey sign-in is a sealed cookie, not a stored row.

## Context

A passkey sign-in has two requests. The first request makes a challenge. The second request sends the response of the passkey. Today the server stores each challenge in a row. The passkeys guide states caps on open challenges: for each source address, for each site group and for the installation. A source at a cap gets an error and no row is stored. A flood of starts from many sources can fill the installation cap. Then every passkey sign-in fails, a break-glass sign-in included.

[ADR 297](0297-console-sessions-are-sealed-cookies-not-stored-sessions.md) decision 4 says that the start of a passkey ceremony keeps the sign-in ID in the WebAuthn challenge.

[ADR 285](0285-local-users-sign-in-with-a-password-and-a-passkey.md) decision 16 says that a lock by name or by factor writes an alert to the administrators. It writes at most one for each name in each hour. An attack against many names can still write many alerts. Decision 23 lists the audit entries of a lock. It has `sign-in.name-locked` and `sign-in.name-unlocked`. It has no entry for a lock that ends by time.

## Decision

Decided by the project owner on 7 November 2026.

1. **The challenge is a sealed, short-lived cookie (owner, 2026-11-07).** The challenge of a passkey sign-in lives in a sealed cookie with a short life. The server stores no row at the start of the ceremony.
2. **A replay record follows a successful sign-in (owner, 2026-11-07).** The server writes a replay record only after a successful sign-in. A start and a failed response write no record.
3. **This replaces the per-source caps (owner, 2026-11-07).** The caps on open passkey challenges do not apply any more. They were for each source address, for each site group and for the installation. No row holds an open challenge.
4. **A lock that ends has an audit kind (owner, 2026-11-07).** ADR 285 decision 23 gets the audit kind `sign-in.name-lock-ended`. The server writes it when a lock by name ends.
5. **Lock alerts have a ceiling (owner, 2026-11-07).** The alerts of ADR 285 decision 16 have a ceiling for each installation and hour. After the ceiling, the server writes one summary alert for the rest of the hour. The ceiling is 10 alerts for each installation and hour. The summary alert gives the count of alerts left out and the top sources. The ceiling is a setting. The API shows its effective value.

## Consequences

- No attacker can fill a table of open challenges. A passkey sign-in no longer fails because of that table.
- The cookie seal and its short life are the limit on a challenge. The replay record stops a second use of a response after a good sign-in.
- The audit log shows when a lock ends, and the summary alert shows that alerts were left out.
- This ADR does not decide the life of the challenge cookie.
- This ADR does not decide the limits on starts and on failed responses for each source address. ADR 285 decision 15 and the passkeys guide give them. They stay until a later decision changes them.
- The passkeys guide describes the caps now. It changes in the pull request that changes the code.
