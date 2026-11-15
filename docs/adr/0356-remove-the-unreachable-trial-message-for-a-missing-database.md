# 356. Remove the unreachable trial message for a missing database

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026.

Changes:

- Changes ADR 238 decision 3: the trial variant of the message for a missing database goes. The trial variant of the key message stays. Both full messages stay.

## Context

ADR 238 decision 3 says that `serve` checks the key first, then the database. The trial hint is "For a trial, run: opendustpan dev". The hint can go in the message for a missing key and in the message for a missing database. It goes in the hint only when the operator set no setting of `serve` and stderr is a terminal.

The trial variant of the database message cannot be shown:

- `requireDecisions` in `cmd/opendustpan/main.go:747` returns `errNoDatabaseTrial` only when `trial` is true (`main.go:758`).
- The code reaches the database check only after the key check passed (`main.go:750`). A key comes from `DUSTPAN_KEY_ENCRYPTION_KEY`, `--key-encryption-key-file` or `DUSTPAN_KEY_ENCRYPTION_KEY_FILE`.
- Each of these counts as a setting of `serve`. So `trial` is false at the database check, and `errNoDatabaseTrial` is never returned.

A test (`TestRefusalTextsOfTheDecision` in `cmd/opendustpan/key_test.go`) only pinned the text of the dead error.

The project owner decided on 7 November 2026 to remove the trial variant of the database message.

## Decision

1. **The trial variant of the database message goes (owner, 2026-11-07).** `serve` gives one message for a missing database. The message reads: "no database is set: set --database (DUSTPAN_DATABASE), for example sqlite:/var/lib/opendustpan/dustpan.db". The code removes `errNoDatabaseTrial`.
2. **What stays (owner, 2026-11-07).** `serve` still checks the key first, then the database, and reports only the first missing one. The trial variant of the key message stays. The full key message and the full database message stay. The rule for which settings count as a setting of `serve` stays for the key message.

## Consequences

- A missing database always gives the full message. A person at a terminal who set a key and no database sees the command that helps: set `--database`.
- The code, one test and this ADR change in the same pull request. No API changes.
