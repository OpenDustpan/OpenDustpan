# Code rules

A code rule is a rule that the server applies and that no grant, setting or
administrator can change. Each rule here names what the server refuses or
shows, and what a person sees. Read this page to learn why the server refused an action
that a grant seems to allow.

Some entries quote the reason of an audit entry. The refusal guard of a route
appends ` on <route>` to such a reason, where the route is the pattern of the
mux, or the method and the path when the mux sets no pattern. When the guard held
back refusals, it also appends `; N further refusals since the last entry`.
The whole reason holds at most 500 bytes. An entry here says "with the route
suffix" for each reason that gets this text.

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
  Code that must fail closed on an unknown action reads the family itself,
  and a person sees the effect in the `protected` flag of a group. The flag
  is `true` when the group holds a sensitive action on the installation or on
  a principal of the installation. A read that meets an unknown action
  (`access.ErrUnknownAction`) answers 500 with the code `server.internal`,
  never `protected: false`. A read that meets an invalid input answers 400
  with no code. Invalid inputs are an object with no kind, an object with an
  unknown kind, a group ID that is empty or not valid, more than
  `principal.MaxLimit` group IDs, and a group on the page with no flag. A
  create or a change that cannot compute the protected flag of the stored
  group answers `protected: true`, and the server logs a warning with the
  group ID and the type of the error
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
  the outcome `denied`, the reason `principal.no-working-break-glass` and the
  scope `@server`. Only the principal routes write it. When the server cannot write this entry, it still
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
  the role. Only a person counts. The person must be enabled, not
  deleted and in no namespace, and must hold the role directly or through an
  active group membership. A service principal does not count. The server
  answers 409 with the code `access.last-site-administrator` and changes
  nothing. The store counts the administrators in the same transaction as the
  change. The store does not refuse a change when the count is already 0
  before the change. The refusal writes an audit entry with the outcome
  `failure` and no reason
  ([Delete a principal](../guides/principals-and-groups.md#delete-a-principal)).
  Three paths differ:
  - A sign-in through an identity provider that reports the last
    administrator as disabled keeps the person. The server refuses the sign-in
    with the reason `identity provider reports the last administrator as
    disabled, and the server keeps the person`.
  - When a group claim at sign-in would end the last membership that gives the
    role, the membership stays and the server answers no 409. The server
    writes an entry with the action `claim-refuse`, the outcome `denied` and
    the reason `the removal would leave no site administrator`.
  - The expiry of a temporary administrator (`ExpireTempAdmin` in each
    store) runs no guard. It disables the principal and ends its grants and
    memberships, also when the principal is the last site administrator. This behavior is
    under owner review.

## Grants and roles

<!-- code-rule: access.ErrEscalation -->
- A person cannot give more than they hold. The server refuses a change that
  gives more on its object than the person who makes it holds there. This
  covers the create and the revoke of a grant, the create and the update of
  a role, the changes of a group and of its members, the grants on a service
  principal, and the delegation and token actions. The server answers 403.
  The code is `access.denied` when the refusal wraps a denial. This covers
  the move of a unit into a collection and the add of a member to a group
  whose grants exceed the reach of the caller. In other cases the answer has
  no catalog code.
  - The grant routes, the role routes and the revoke route write a denied
    audit entry. When the error names missing actions, the reason is
    `outside the reach of the caller: <action> on <object>, ...`, with at most
    20 actions and then `, and N more`. Else the reason is
    `grant gives more than the granter holds`. Each reason gets the route
    suffix. The limiter can suppress the entry, and the server writes none for
    an empty subject.
  - The principal routes and the group routes write no denied entry.
  - A refused grant or role change also writes a second entry with the
    outcome `failure` and no reason. The principal routes write the
    `failure` entry with the reason of the missing actions.
<!-- code-rule: access.ErrBuiltIn -->
- A built-in role cannot be changed or deleted. The server answers 409 with
  no specific code. The server writes an audit entry with the outcome
  `failure` and no reason.

## Access tokens

<!-- code-rule: accesstoken.ErrSignInRequired -->
- An access token cannot create or rotate an access token. A token that
  made tokens would outlive its maximum lifetime and a revoke. The server
  answers 403 with the code `token.requires-sign-in`.
<!-- code-rule: accesstoken.ErrNotOwnToken -->
- A person creates and rotates only their own personal tokens, after a
  sign-in, whatever grant they hold. The refusal is a denial (403) with the
  code `access.denied`. On an own-scope route, the token of another person
  answers 404, so the route tells nothing about it. One exception exists: the
  host recovery issuer (`HostIssuer.Recover`) makes the first token of a new
  temporary administrator. The store inserts it only when the person
  is at most 5 minutes older than the token (`HostFirstTokenWindow`), a
  temporary administrator record covers the expiry of the token, and the
  person holds no token of any status. Any other host token gets the same denial.

## Local passwords and passkeys

<!-- code-rule: localuser.ErrOwnAccount -->
- An administrator cannot reset their own password through the administrator
  route. The server answers 403 with the code `local-user.own-account` and
  writes an audit entry with the outcome `denied` and the change cause
  `own account`. When password sign-in is off, the answer is
  `local-user.password-sign-in-off` first. The service refuses the delete of
  the own password too, but no administrator route calls it. A person changes
  their own password with the change-password route.
<!-- code-rule: localuser.ErrLastCredential -->
<!-- code-rule: mfa.ErrLastCredential -->
- The store refuses a change that deletes the last sign-in credential of a
  local person.
  - The store refuses the delete of a password always, also when the person
    has a passkey. A missing user, person or password answers not found, and
    the demo user answers its own refusal, before this one.
  - The store refuses the delete of a local user while the user has a
    password.
  - The store refuses the delete of a passkey when the person would keep no
    passkey that is not suspended and the person is a break-glass account, or
    has a local user with no password. A suspended passkey does not count. A
    person with no local user, for example one who signs in through an
    identity provider, can delete the last passkey.
  - The server answers 409 with the code `local-user.last-credential`. Only
    the delete of a passkey has a route. The service methods that delete a
    password and a local user have no route.
- One person holds at most 20 passkeys (`mfa.MaxPasskeys`). The server
  answers 409 with the code `mfa.passkey-limit` and the argument `limit`.

## Break-glass accounts: kind and review

<!-- code-rule: principal.ErrBreakGlassKind -->
- Only a person can be a break-glass account. A write that sets the flag on
  a principal of another kind fails validation. The rule is in
  `Principal.Validate`, which the service calls on a create and on an update,
  and in the store create of each dialect. The service never sets the flag.
<!-- code-rule: breakglass.ErrSelfReview -->
- A person who took part in a break-glass use cannot record the verdict of its
  review. The rule compares the reviewer, without case, with the subject of
  the use, with each person of the use, and with each `ActAs` entry of the
  reviewer and of the verdict. The review store refuses the verdict with
  `ErrSelfReview`. Limit: no production code fills `ActAs` yet. The server
  fills only the people of a use, and only for a temporary administrator. The
  rule does not yet cover a principal that could act as the person.

## Directory groups and sign-in targets

<!-- code-rule: principal.ErrManagedByDirectory -->
- The API cannot change the members of a group that a directory keeps. The
  directory sync is the source of truth. The server answers 409 with the code
  `principal.managed-by-directory`. The refusal writes the generic failure
  entry of the change, with no specific reason.
<!-- code-rule: principal.ErrNotSignInTarget -->
- A sign-in through an identity provider, and a disable by an identity
  provider, apply only to a person. A principal of another kind (service,
  workload or agent), a principal of a namespace, a break-glass account and a
  temporary administrator are never a target. The server refuses the sign-in
  with the outcome `denied` and the reason `principal cannot sign in through
  an identity provider`. It refuses the disable with the outcome `failure` and
  the reason `identity provider reports a principal as disabled that it cannot
  manage`. The audit log records the reason of each.

## Jobs and agents

<!-- code-rule: job.ErrSelfApproval -->
- The principal that started a job cannot approve it, unless a gate of the
  phase sets `allowSelf`. This also covers a person who approves a job that
  their agent started. The service refuses an agent that tries to approve earlier, with
  `job.agent-denied`. The server answers 403 with the code `job.self-approval`
  and writes an audit entry for the action `job:approve` with the outcome
  `denied` and the reason `the person who started the job cannot approve it`,
  with the route suffix.
<!-- code-rule: job.ErrAgentDenied -->
- An agent cannot approve or confirm a job, cannot ask for break-glass, cannot
  fire a trigger, and cannot start a job that its agent mode does not allow.
  The server answers 403 with the code `job.agent-denied`. A refused approval,
  a refused start and a refused confirm write an audit entry with the outcome
  `denied` and the reason `an agent cannot do this`, with the route suffix.
  The action is `job:approve` for an approval, the start action of the job for
  a confirm, and the start action for a start. The trigger route answers the
  same 403 and writes no audit entry.
<!-- code-rule: job.ErrNotRequester -->
- Only the person who asked the agent for a job can confirm it, whatever
  grant another principal holds. The rule applies to a verdict of approve
  only. The service runs its tests in this order: the start action, the state
  of the job, the agent, and then the requester. The server answers 403 with
  the code `access.denied` and writes an audit entry with the outcome
  `denied` and the reason `only the person who started the job can confirm
  it`, with the route suffix.
