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

<!-- code-rule: principal.ErrNoWorkingBreakGlass -->
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

## Site administrators

<!-- code-rule: access.ErrLastSiteAdministrator -->
- The server refuses each change that leaves the installation with no site
  administrator. This covers the delete or the disable of the last site
  administrator, and the end of the last grant, membership or group that gives
  the role. The server answers 409 with the code
  `access.last-site-administrator` and changes nothing. The server counts the
  administrators in the same transaction as the change. The refusal writes
  an audit entry with the outcome `failure` and no reason. A sign-in that
  would disable the last administrator gets its own sign-in refusal reason
  ([Groups](../guides/principals-and-groups.md#groups)).

## Grants and roles

<!-- code-rule: access.ErrEscalation -->
- A person cannot give more than they hold. The server refuses a grant that
  gives more on its object than the person who makes it holds there. The
  server answers 403 with no specific code, and the refused grant is
  written to the audit log.
<!-- code-rule: access.ErrBuiltIn -->
- A built-in role cannot be changed or deleted. The server answers 409 with
  no specific code. The refusal writes no audit entry.

## Access tokens

<!-- code-rule: accesstoken.ErrSignInRequired -->
- An access token cannot create or rotate an access token. A token that
  made tokens would outlive its maximum lifetime and a revoke. The server
  answers 403 with the code `token.requires-sign-in`.
<!-- code-rule: accesstoken.ErrNotOwnToken -->
- A person creates and rotates only their own personal tokens, after a
  sign-in, whatever grant they hold. The refusal is a denial (403).

## Local passwords and passkeys

<!-- code-rule: localuser.ErrOwnAccount -->
- An administrator cannot reset or clear their own password through the
  administrator routes. The server answers 403 with the code
  `local-user.own-account`. A person changes their own password with the
  change-password route.
<!-- code-rule: localuser.ErrLastCredential -->
<!-- code-rule: mfa.ErrLastCredential -->
- The server refuses each change that removes the last sign-in credential of
  a local person: the password when they have no passkey, or the last passkey
  when they have no password. The server answers 409 with the code
  `local-user.last-credential`.
- One person holds at most 20 passkeys (`mfa.MaxPasskeys`). The server
  answers 409 with the code `mfa.passkey-limit` and the argument `limit`.

## Break-glass accounts: kind and review

<!-- code-rule: principal.ErrBreakGlassKind -->
- Only a person can be a break-glass account. A write that sets the flag on
  a principal of another kind fails validation, in the service and in the
  store.
<!-- code-rule: breakglass.ErrSelfReview -->
- A person who took part in a break-glass use cannot record the verdict of its
  review. This covers the person who used break-glass, the owner of the
  account, and each principal that could act as the person at the time of the
  use. The review store refuses the verdict with `ErrSelfReview`.

## Directory groups and sign-in targets

<!-- code-rule: principal.ErrManagedByDirectory -->
- The API cannot change the members of a group that a directory keeps. The
  directory sync is the source of truth. The server answers 409 with the code
  `principal.managed-by-directory`. The refusal writes the generic failure
  entry of the change, with no specific reason.
<!-- code-rule: principal.ErrNotSignInTarget -->
- A sign-in through an identity provider, and a disable by an identity
  provider, apply only to a person. A service principal, a namespace
  principal, a break-glass account and a temporary administrator are never
  a target. The server refuses the sign-in with the sign-in refusal reason
  `not a target`, which the audit log records.

## Jobs and agents

<!-- code-rule: job.ErrSelfApproval -->
- The principal that started a job cannot approve it, unless the decision
  allows it. This also covers a person who started the job through an agent.
  The server answers 403 with the code `job.self-approval` and writes an
  audit entry for the action `job:approve` with the outcome `denied`.
<!-- code-rule: job.ErrAgentDenied -->
- An agent cannot approve, confirm or break a freeze, and cannot start a job
  that its agent mode does not allow. The server answers 403 with the code
  `job.agent-denied`. A refused approval writes an audit entry with the reason
  `agent-denied`.
<!-- code-rule: job.ErrNotRequester -->
- Only the person who asked the agent for a job can confirm it, whatever
  grant another principal holds. The server answers 403 with the code
  `access.denied`.
