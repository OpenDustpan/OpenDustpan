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
  rule, with no stored grant record. It stays valid until the operator deletes it
  from the configuration and restarts the server.
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

- Only a person in a console session makes or accepts a link. A principal
  that is not a person, a disabled person and a break-glass account get 403
  with the code `git-host.link-person-required`. On the accept, the server
  tests the freshness first, so a request with no console session, for
  example one with an access token, gets 403 with the code
  `mfa.fresh-step-required`. A session whose sign-in is older than the token
  freshness window gets the same answer. The answer also covers a window of
  0, a window out of range, a policy read that fails, and a sign-in dated in
  the future ([Git host account links API](git-host-links-api.md)).

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
  the role. Only a person counts. The person must be enabled, not deleted and
  in no namespace, and must hold the role through a current grant on the
  installation, directly or through an active membership of a group that
  exists. A grant on a namespace or a collection does not count, and a
  service principal does not count. The server answers 409 with the code
  `access.last-site-administrator` and changes nothing. The store counts the
  administrators in the same transaction as the change. The store does not
  refuse a change when the count is already 0 before the change. The refusal
  writes an audit entry with the outcome `failure` and no reason
  ([Delete a principal](../guides/principals-and-groups.md#delete-a-principal)).
  Three paths differ:
  - A sign-in through an identity provider that reports the last
    administrator as disabled keeps the person. The server refuses the
    sign-in with the outcome `failure`. The entry stores the text `identity
    provider reports the last administrator as disabled, and the server keeps
    the person` as a change with the key `reason`, with no route suffix. The
    server uses the same reason when the disable returns
    `principal.ErrNoWorkingBreakGlass`.
  - When a group claim at sign-in would end the last membership that gives the
    role, the membership stays and the server answers no 409. The server
    writes an entry with the action `group-claim:refuse-member`, the outcome
    `denied` and the object `group:<id>`. The entry stores the text `the
    removal would leave no site administrator` as a change with the key
    `reason`, not in the reason field of the entry.
  - The expiry of a temporary administrator (`ExpireTempAdmin`, the same in
    both dialects) runs no guard, and the disable of the principal tests no
    break-glass rule either. In one transaction it ends the record of the
    temporary administrator (`tempadmin.ErrExpired` when the record already
    ended, `tempadmin.ErrNotFound` when none exists), disables the principal,
    ends the grants of the principal and the grants held on it, ends its group
    memberships, and ends its live tokens, which are the tokens that no one
    revoked or disabled and whose expiry is later than the time of the
    expiry. It does this also when the principal is the last site
    administrator. In maintenance mode it writes the expiry to the
    revocation journal, and a restore replays it. It writes the audit entry
    in the same transaction. This behavior is under owner review.

## Grants and roles

<!-- code-rule: access.ErrEscalation -->
- A person cannot give more than they hold. The server refuses a change that
  gives more on its object than the person who makes it holds there. This
  covers the create and the revoke of a grant, the create and the update of
  a role, the delete, the enable and the disable of a principal, the changes
  of a group and of its members, a group change that would lift an access
  limit, a gate or an approval rule that names the group, the grants on a
  service principal, and the delegation and token actions. The server
  answers 403.
  The code depends on the path:
  - The move of a unit into a collection answers `access.denied` with the
    message `forbidden`.
  - A member add first tests the grants of the group. A refusal of that test
    is a plain refusal: 403 with no catalog code and the text of the error. A
    member add then tests the reach of the group over service principals. A
    group grant that reaches a service principal outside the reach of the
    caller answers `access.denied`. The same answer applies when the server
    has no reach port and the caller is not an administrator.
  - In an installation that keeps no grants, the guard allows only an
    administrator to change who holds grants. Any other caller who adds a
    member, deletes a principal or makes a similar change gets `access.denied`
    with the message `forbidden`.
  - In other cases the answer has no catalog code.
  - The grant routes, the role routes and the revoke route write a denied
    audit entry. When the error names missing actions, the reason is
    `outside the reach of the caller: <action> on <object>, ...`, with at most
    20 actions and then `, and N more`. Else the reason is
    `grant gives more than the granter holds`. Each reason gets the route
    suffix. The reason holds at most 20 actions, and the byte budget of the
    reason can give a shorter list. The limiter can suppress the entry, and
    the server writes none for an empty subject. When the namespace of the
    entry does not exist, the entry moves to the installation scope and the
    reason ends with ` (namespace <name> does not exist)` after the route
    suffix.
  - The principal routes and the group routes write no denied entry. The
    exception is `principal.ErrNoWorkingBreakGlass`, which writes a denied
    entry (see the break-glass rule above).
  - A failed change on a grant route, a role route or the revoke route also
    writes a second entry with the outcome `failure` and no reason, when the
    change names an object. A failed change on a principal route or a
    group route writes a `failure` entry when the change has a record. That
    entry holds the reason of the missing actions for any escalation refusal
    that names at least one missing action.
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
  `own account`. When the server has no public URL, the answer is
  `local-user.public-url-required` first, and when password sign-in is off,
  the answer is `local-user.password-sign-in-off`. The service refuses the
  delete of the own password too, but no administrator route calls it. A person changes
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
    the delete of a passkey has a route. The service method that deletes a
    password has no route. No service method deletes a local user, only the
    store does.
  - The delete of a passkey of the demo user answers 409 with the code
    `local-user.demo-fixed` first.
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
  their agent started. The service tests for an agent before it tests for the starter. An agent
  that tries to approve gets `job.agent-denied`, not `job.self-approval`. The server answers 403 with the code `job.self-approval`
  and writes an audit entry for the action `job:approve` with the outcome
  `denied` and the reason `the person who started the job cannot approve it`,
  with the route suffix.
<!-- code-rule: job.ErrAgentDenied -->
- An agent cannot approve or confirm a job, cannot ask for break-glass, cannot
  fire a trigger, and cannot start a job that its agent mode does not allow.
  The server answers 403 with the code `job.agent-denied`. A refused approval,
  a refused start and a refused confirm write an audit entry with the outcome
  `denied` and the reason `an agent cannot do this`, with the route suffix.
  The action is `job:approve` for an approval and the start action of the job
  for a confirm. For a start, the action is the start action of the planned
  job when the refusal names it, else `unit:run-plan` on the unit of the
  request. The drift start route answers the same 403 and writes a
  `denied` entry with the reason `an agent cannot do this`, with the route
  suffix, the action `unit:run-plan` and the unit. The trigger route answers
  the same 403 and writes no audit entry. Limit: no production code sets a
  non-empty agent on the actor of a start, an approval, a confirm, a drift
  start or a trigger. The agent rules guard the service, and no route reaches
  them today.
<!-- code-rule: job.ErrNotRequester -->
- Only the person who asked the agent for a job can confirm it, whatever
  grant another principal holds. The rule applies to a verdict of approve
  only. The service runs its tests in this order: the start action, the state
  of the job, the agent, and then the requester. The server answers 403 with
  the code `access.denied` and writes an audit entry with the outcome
  `denied` and the reason `only the person who started the job can confirm
  it`, with the route suffix.
