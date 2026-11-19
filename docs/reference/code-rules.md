# Code rules

A code rule is a rule that the server applies and that no grant, setting or
administrator can change. Each rule here names what the server refuses or
shows, and what a person sees. Read this page to learn why the server refused an action
that a grant seems to allow.

## Bootstrap admin token

- The bootstrap admin token holds every action on the installation by a code
  rule, with no stored grant record. It ends on the first sign-in of a person.
  The access-review export shows it as a row with source `"code-rule"` and
  rule `"bootstrap admin token"`, with no grant ID, no creator and no time.

## Sensitive actions

- Each action of the operator, the administration and the delegation families
  is sensitive, except each read-only server action (`server:read-*`), which
  cannot raise a privilege. A new action of those families is sensitive with
  no other change, and an action that no family classifies is not sensitive.
  A check that must fail closed on an unknown action reads the family itself,
  and a person sees the effect in the `protected` flag of a group. The flag
  is `true` when the group holds a sensitive action on the installation or on
  a principal of the installation. A read that cannot check the group answers
  500 with the code `server.internal`, never `protected: false`. A create or a
  change that cannot check the stored group answers `protected: true`, and
  the server logs the failed check
  ([Groups](../guides/principals-and-groups.md#groups)).

## Git host links

- Only a person in a console session makes or accepts a link. An accept needs
  a recent sign-in: a session whose sign-in is older than the token freshness
  window gets 401 with the code `mfa.fresh-step-required`. An access token
  gets 403 `git-host.link-person-required`, except on the accept, which
  answers `mfa.fresh-step-required`, and a break-glass account cannot make a
  link ([Git host account links API](git-host-links-api.md)).

## Break-glass accounts

- The server refuses to disable or delete the last enabled break-glass account
  with 409 and the code `principal.no-working-break-glass`. The request
  changes nothing. Before the answer, the server writes one audit entry with
  the outcome `denied`. When the server cannot write this entry, it still
  answers 409 and logs the failure
  ([Keep a break-glass account](../guides/break-glass-accounts.md#the-rule)).
- A restore does not replay a disable that would leave no enabled break-glass
  account. The account stays as the restored data holds it, and the server
  writes an audit entry with the outcome `denied` and the reason
  `principal.no-working-break-glass`
  ([Revocations in maintenance mode](../guides/backup-restore-upgrade.md#revocations-in-maintenance-mode)).
- A restore stops with the code `principal.no-working-break-glass` when it
  lowers the count of working break-glass accounts and leaves fewer than two
  ([Other paths](../guides/break-glass-accounts.md#other-paths)).

## Background operations

- A background operation (a durable operation) whose holder cannot renew its
  claim in time is released at the local deadline of the claim. The server
  counts these releases in a row for each operation. A
  successful renewal sets the count to 0. At 5 releases in a row, the next
  claim ends the operation failed. The last error of the operation names the
  cause: "durable operation holders could not renew the claim in time too
  often". The server also logs one warning for each release. No grant, setting
  or administrator changes the limit of 5.
