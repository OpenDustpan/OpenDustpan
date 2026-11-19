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

## Settings changes that need a fresh sign-in

- A change of a setting that reduces security (`mayReduceSecurity` is `true`
  on the setting) needs a fresh authentication. A server that has no console
  sign-in asks for none, and the change runs with no fresh authentication.
  The server has no console sign-in when it has no provider in its
  configuration, no dev sign-in, and no store that keeps sessions with a
  valid public URL (https, or http on a loopback host). The sign-in runs
  with zero stored providers. The dev sign-in counts when it is set. The
  check does not read the installation kind, but the server accepts a dev
  sign-in only on an installation of kind dev. The code checks only this
  condition. It does not read the setting of built-in MFA or the list of SSO
  providers. This rule stays because such a server has no sign-in step to
  ask for.
- The rule has no effective value in the API. The unmounted sign-in routes
  of the server show its condition. Each change that the rule lets through
  writes the normal audit entry of a settings change with the changed keys.
  The server writes no separate record that it skipped the step.
- A server that has a console sign-in answers 403 with the code
  `mfa.fresh-step-required` to a change with no fresh authentication
  ([Changes that need a fresh sign-in](../guides/settings.md#changes-that-need-a-fresh-sign-in)).

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

## IP allowlists

- Once a list check calls the read, the server reads the namespaces that have
  an IP allowlist for a list check of a principal whose scope covers every
  object. The read returns at most 1000 namespaces. When more than 1000
  namespaces have a list, the read fails with the error
  `ipallow: too many namespaces have an IP allowlist`, and the server does not
  cover every object. No setting changes the limit. Then the server refuses a
  principal whose scope covers every object on every list until the number of
  namespaces with a list is 1000 or fewer
  ([Limits](../guides/ip-allowlists.md#limits)).
