# 277. No implicit authority: every right is a stored grant

Status: accepted · 2026-10-25. Decisions 1 to 13 are owner decisions, and each one names its source. The sources, all of 25 October 2026, are the no-magic principle and decisions 1 to 4 of the owner in issue 1841. They are also the five research recommendations that the owner approved, and the owner points P1 to P3. Decisions 14 to 30 are design decisions of this ADR, and follow from them. Supersedes in part ADR 6, ADR 267 decision 14 and the decision r3 of issue 1216. Extends ADR 52 decision 14, ADR 67 and ADR 70

## Context

The owner asked for a platform with no magic: observable and intuitive. Each
permission must be a record that a person can list, audit and revoke. Today
some authority comes from code, with no stored grant. A research pass on
`main` at `ce44d837` found 14 places that decide access. In 8 of them, part
or all of the authority has no stored grant:

| # | Where | What code allows | Visible today |
| --- | --- | --- | --- |
| 1 | `identity.NamespaceService`, `identity.NamespaceServiceRule`, `access.Authorizer` | `unit:run-plan` on every unit of a namespace, for starts by run triggers | No. The principal API refuses the subject, and the scope of the principal is empty. |
| 2 | `identity.Principal.Admin` | Every action. It also skips the escalation check. | Partly. The access-review export has one row for each bootstrap subject. |
| 3 | `identity.StaticToken`, `DUSTPAN_ADMIN_TOKEN` | A principal with the admin flag and no record | Partly, as row 2 |
| 4 | `identity.AdminOnly`, `identity.AdminScope` | The default authorizer: allow by the admin flag only | No. It is a choice of the wiring. |
| 5 | `identity.SelfAllowed`, the family `self` | The self actions, for example `token:manage-own` and `caller:manage-preferences`, for each signed-in principal | Partly. The comments of the actions say so. |
| 9 | `pkg/runner` | A runner key claims the jobs of its pool through the protocol only | Partly. The API lists runners, not grants. |
| 10 | `identity.CredentialJobToken` | A job token acts only on the unit of its job, while its claim is live | Partly. The job names its starter. |
| 11 | Break-glass sessions (ADR 223 decision 7) | The access limits do not apply | Partly. The admin access report lists the accounts. |

The other 6 places already rest on stored grants. They are the grant of the
creator of a namespace, the built-in roles, the check of the last saver of a
trigger, the temporary administrator of ADR 245, the dev administrator of
ADR 252 and the act-as edges of ADR 67 and ADR 70.

Issue 1841 found a port behavior with no contract: the rule of the namespace
service principal exists only in `access.Authorizer`. A first fix put a
decorator around every Authorizer. The owner then decided that a principal
that the server makes is a first-class principal with stored grants.

Precedent agrees. ISO/IEC 27002:2022 control 8.2 asks for a record of each
privilege that an administrator allocates, and for no generic administrator
identity. Kubernetes from v1.29 binds the `admin.conf` of kubeadm through a
ClusterRoleBinding that a person can revoke, and keeps a separate
`super-admin.conf` for break-glass use. CIS Controls v8.1 Safeguard 5.5 asks
for an inventory of service accounts with an owner, a purpose and a review
each quarter. It allows a department as the owner. PCI DSS v4.0.1
requirements 8.6.1 to 8.6.3 manage the risk of a system account with a
credential that a person can present. A principal with no such credential
leaves nothing for them to manage. ARBAC97 section 3.2 keeps the grants that
an administrator made when the administrator loses a role. Its model of
delegation (section 3.1) bounds a grant by the rights of the granter.

## Decision

### Decisions of the owner

Each decision in this section is a decision of the owner, with its source.
The text keeps the words of the owner. The design decisions that follow
add the details.

1. **No magic.** Source: the owner principle "No magic" of 25 October 2026.
   Every permission is a stored grant with a holder, a role, an object, a
   creator and a time. A user can list, audit and revoke it. Code may define
   roles and actions. Code never defines the holder of a grant. Each
   automatic action writes a record of who or what did it and why. Each
   default shows its effective value in the API. A user can ask why the
   platform allowed an action, and the answer names the grant, the rule or
   the event. A rule that must stay in code is in the catalog of code rules,
   with its documentation.
2. **No IP allowlist for the server itself.** Source: issue 1841, owner
   decision 1. A principal that the server makes has no network origin, so
   the address check does not apply to it. It gets no configurable
   exemption.
3. **A switch in each namespace turns off server-started runs.** Source:
   issue 1841, owner decision 2. An administrator can stop all runs that the
   server starts in a namespace, scheduled and upstream-triggered.
4. **Server-made principals are first-class and visible.** Source: issue
   1841, owner decision 3. Each one shows that the server made it, its
   purpose, for example "scheduled runs of unit X", and what it may do.
   Users can view its permissions and revoke them one at a time, as for any
   other principal. Its grants are not a hidden rule in the Authorizer.
5. **No impersonation, no use outside its purpose.** Source: issue 1841,
   owner decision 4. No credential that a client presents can act as a
   server-made principal. Only the server builds it, and it can do only
   what the server made it for.
6. **An accountable owner, the last saver by default.** Source: research
   recommendation 1, approved by the owner. The owner of a server-made
   principal is the last saver of its trigger by default. A person can
   transfer the ownership.
7. **A revoke pauses the trigger.** Source: research recommendation 2,
   approved by the owner. When a person revokes a grant that a trigger
   needs, the server pauses the trigger. It deletes no trigger.
8. **The admin flag goes in one step.** Source: research recommendation 3,
   approved by the owner. `Principal.Admin` and the admin-only default
   authorizer go in one step, because the project has no users yet.
9. **A review each quarter.** Source: research recommendation 4, approved
   by the owner. The access review lists the server-made principals, and a
   reviewer reviews them each quarter.
10. **Self actions are stored grants.** Source: research recommendation 5,
    approved by the owner. The self actions, for example `token:manage-own`
    and `caller:manage-preferences`, become stored grants of a built-in
    role. The API lists them as every other grant.
11. **One server-made principal for each trigger.** Source: owner point P1.
    The server makes one server-made principal for each trigger, not one
    for each purpose kind and unit. The owner of the trigger is the owner of
    its principal. A save does not overwrite a transfer of the ownership,
    unless this ADR says so in a rule (decision 18). The grants that cap
    check 2 of ADR 213 decision 1 for the jobs of a trigger are the grants
    of the owner of that trigger.
12. **A person or a group as the owner.** Source: owner point P2. The
    accountable owner can be a person or a group. The default is the last
    saver.
13. **A principal can always revoke its own tokens and sessions.** Source:
    owner point P3. To revoke one's own tokens and sessions is always
    allowed. This is one entry of the catalog of code rules.

### Design decisions

14. **A new principal kind `server`.** A server-made principal has the kind
    `server` (`principal.KindServer`), not the kind `service` with a marker.
    A service principal can hold tokens (ADR 67). A server-made principal
    can never hold one. A separate kind lets each guard refuse it by its
    kind alone. `principal.ParseKind` refuses `server`, so no request can
    make or name the kind. Only `principal.ServerPrincipalStore` stores it.
    `accesstoken.KindFor` gives no token kind for it. The kind joins
    `identity.Kinds` and the audit kinds in the slice that stores the first
    such principal. The server acts as a server-made principal only with the
    credential `server` (`identity.CredentialServer`). The pair of the kind
    `server` and the credential `server` is the only valid pair for either
    of them.
15. **The purpose is the runs of one trigger.** A purpose
    (`principal.Purpose`) is a purpose kind, the ID of one trigger and the
    unit of that trigger, by the stable ID of the unit. The purpose kind
    follows the kind of the trigger: `scheduled-runs` for a schedule
    trigger, `upstream-runs` for a follows trigger and `review-request-runs`
    for a review-request-push trigger. A save never changes the unit or the
    kind of a trigger, so the purpose of a principal never changes. A grant
    for one trigger never serves another trigger, also of the same unit.
    The trigger limits of a unit and of a namespace bound the number of
    server-made principals. Each purpose kind lists in code the actions that
    its principal can hold. For the three first kinds, the list is
    `unit:run-plan` on the unit of the trigger.
16. **The role of a purpose grant.** A new built-in role `plan-starter`
    holds one action, `unit:run-plan`. Its actions equal the list of the
    three first purpose kinds. The server gives each server-made principal a
    grant of `plan-starter` on the unit of its trigger, and no other grant.
    A purpose kind with another list of actions in a later ADR names its own
    built-in role, with actions that equal its list. A person can grant
    `plan-starter` to a person or to a service principal as any other
    built-in role.
17. **The record.** `principal.ServerPrincipal` holds the ID of the
    principal record, the namespace, the purpose, the owner, the source of
    the owner, the time of the last change of the owner, the time of the
    create, the time of the end and the last use. It holds no grant and no
    credential. The grants are ordinary rows of the access store. The
    subject of the principal record starts with `system:`, a prefix that the
    principal API already refuses. Its form is
    `system:server:<namespace>:trigger:<trigger ID>`. When a person deletes
    a trigger, the trigger service revokes each grant of its server-made
    principal through the access service, with the server as the actor,
    and then ends the record. The record stays for the audit log and the
    access review, with its end time.
18. **The owner of a trigger.** The owner (`principal.Owner`) is a person or
    a group, with a source: `last-saver` or `transfer`.
    - The first save of a trigger makes the saver the owner, with the source
      `last-saver`.
    - While the source is `last-saver`, each save makes the saver the
      owner. That is the default of decision 6.
    - A transfer, by the owner route of slice 9, sets the new owner with the
      source `transfer`. After a transfer, a save keeps the owner and its
      source: a save does not overwrite a transfer. The save of a person who
      is not the owner still needs the actions of the save on the unit, and
      the purpose grant still needs the actions of the owner (decision 19).
    - A transfer to the next saver sets the source `last-saver` again. The
      owner route offers that choice by name.
    - Each change of the owner appends one audit entry with the old owner,
      the new owner and the source.

    For a group as the owner, the server uses the stored grants of the group
    itself: the grants that name the group as their holder. The grants of
    its members do not count, so a member who leaves changes nothing. The
    access review shows a group owner as the group, with its name, its ID
    and the number of its members.

    The check at use stays: each start by a server-made principal checks
    the current grants of the owner, as ADR 267 decision 14 checks the last
    saver. Check 2 of ADR 213 decision 1 for a job of the trigger also uses
    the grants of the owner of that trigger. For a person, a missing,
    deleted or disabled record means deny. For a group, a deleted group
    means deny. These checks are the catalog entry
    `server-principal-owner-cap`, with the effect `limits`.

    "Owner lost access" is not stored: the access review computes it from
    the current grants of the owner. It holds when the owner does not hold
    each action of the purpose on the unit. For a person, it holds also when
    the record is deleted or disabled. For a group, it holds when the group
    is deleted, or when the grants of the group do not give the actions. A
    group with no member is not "owner lost access". The access review flags
    it as "owner has no member", because no person answers for the
    principal, and the runs continue. The time of "owner lost access" comes
    from the audit entry of the change that ended the access of the owner: a
    revoke, a change of a membership of a group that held the grant, or a
    disable or delete of the record. It never comes from a refused start,
    because a start can never happen, for example when the switch is off or
    the trigger is paused. When the owner loses access, the grants of the
    server-made principal stay, and the starts fail closed. No revoke
    cascades from the owner to the grants.
19. **The grant path of a server-made principal.** The server makes the
    grant of decision 16 through the grant path of a person, with the
    escalation check against the owner of the trigger. A person can never
    give a server-made principal more rights than the owner holds. The
    escalation check reads the stored record of the owner and its stored
    grants only. It never reads `Principal.Admin`, the static-token
    principal or any other flag, also before the admin flag goes. An owner
    with `Admin: true` and no stored grant gets `access.ErrEscalation`. The
    audit entry of the grant names the server as its actor, and gives the
    reason, for example the save of a trigger. Only
    `trigger.PurposeGrants.Ensure` makes a grant to a server-made principal.
    The grant API refuses a request of a person for a grant to a principal
    of the kind `server` with an error that wraps
    `principal.ErrServerPrincipal`. So no holder of the access management of
    a unit can make a revoked grant again past the owner, the escalation
    check and the pause. A person with the right to revoke a grant can
    revoke a grant of a server-made principal, one grant at a time.
20. **Visible in the default list.** Each principal that can read the access
    of a namespace sees its server-made principals in the default list of
    principals. The access-review export and the admin access report list
    them, with the purpose, the owner, the grants and the last use.
21. **The ports.** This ADR adds these ports and types. The consuming
    package owns each one, and its doc comments hold the contract:
    - `principal.ServerPrincipalStore`: `EnsureServerPrincipal`,
      `ServerPrincipalFor`, `ListServerPrincipals`, `SetOwner` and
      `EndServerPrincipal`.
    - `trigger.PurposeGrants`: `Ensure` makes the principal of a trigger and
      its grant at the save of the trigger.
    - `trigger.PauseStore`: `Pause`, `Paused`, `ListPaused` and `Resume`,
      each for one trigger.
    - `access.GrantRevoked`: the access service tells the trigger service
      that a revoke ended a grant of a server-made principal.
    - `job.ServerRunsSettings`: `ServerRuns` and `SetServerRuns`, the
      switch of decision 3.
    - `identity.CodeRule`: the type of an entry of the catalog, with the
      effects `allows`, `limits` and `exempts`.
    - `identity.DeniedError` and `identity.DenyCause`: the cause of a
      denial.
    - `access.Reason`: one action of an "explain why" answer, with a grant
      ID or the ID of an `allows` rule, and the `limits` and `exempts` rules
      that applied.

    The existing `access.GrantStore.RevokeGrant`, `ListGrantsOn` and
    `GrantLookups.GrantsOf` list and revoke the grants of a server-made
    principal. They need no new method. The writes of `RecordStore`,
    `NamespaceRecordStore` and `MemberStore` answer
    `principal.ErrServerPrincipal` for a principal of the kind `server`, and
    their reads list it. Slice 3 writes each of these clauses in the doc of
    its own port, with its conformance cases.
22. **The Authorizer port has no special principal.** The contract of
    `identity.Authorizer` changes in this ADR. An Authorizer allows an
    action only for a stored grant, or for a code rule with the effect
    `allows`. A subject, a kind, a credential or a flag never allows an
    action by itself. These guards apply before any grant:
    - A principal with a `system:` subject is denied every action, unless it
      has the kind `server` and the credential `server`. The comparison of
      the prefix ignores the letter case, so `SYSTEM:` is denied too. The
      namespace service principal is decided by `NamespaceServiceRule`
      while it exists.
    - A principal of the kind `server` with another credential is denied
      every action.
    - A principal with the credential `server` of another kind is denied
      every action, except the namespace service principal while it exists.

    These guards are the catalog entry `server-subject-guard`, with the
    effect `limits`. `access.Authorizer` applies them, and a decorator
    `identity.ServerSubjectGuard` applies them outside every Authorizer, the
    embedder's included. Each layer stops a forged principal alone.

    An Authorizer that cannot name the grant or the `allows` rule of an
    action denies it: the decision and its explanation never disagree. A
    denial wraps `identity.ErrDenied` in an `identity.DeniedError` with a
    cause: `no-grant` when no stored grant gives the action, `code-rule`
    when a rule with the effect `limits` took it away, with the ID of the
    rule, and `fail-closed` when an error kept the Authorizer from a
    decision. A denial with no `DeniedError` has an unknown cause.

    Until their slices delete them, these parts of the tree allow actions
    with no grant, against the new contract: `identity.AdminOnly` (slice
    11), `identity.SelfAllowed` (slice 12), the `Admin` shortcut of
    `access.Authorizer` and of the escalation check (slice 11), and the
    `FamilySelf` shortcut of `access.Authorizer` (slice 12). The doc comment
    of `identity.Authorizer` names them. The paragraph on the namespace
    service principal stays until slice 5 deletes it.
23. **A guard around every Authenticator.** A decorator in the wiring,
    `identity.ServerCredentialGuard`, wraps the whole chain of
    authenticators that the server builds, the authenticator of
    `dustpan.WithAuthenticator` and each claim mapping of an identity
    provider included. It refuses each principal with the credential
    `server`, the kind `server` or a `system:` subject in any letter case,
    with `identity.ErrUnauthenticated`, and writes an audit entry of the
    refusal. No authenticator can give the server a principal that only the
    server builds. This guard is the catalog entry
    `server-credential-authn-guard`, with the effect `limits`. It has its
    own test, so that it stops a forged principal also when the guard of
    decision 22 is gone. Both guards land in slice 2, before slice 7 exempts
    the server-made principal from the IP allowlist.
24. **A purpose cap stays in front of every Authorizer.** A decorator
    replaces `identity.NamespaceServiceRule`. For a principal of the kind
    `server`, it denies each action that is not in the list of its purpose
    kind, and each object that is not the unit of its trigger. It compares
    the unit by the stable ID that the core resolved for the object, and
    denies when no stable ID is resolved. Then it asks the inner Authorizer,
    which reads the stored grants. The cap only takes away: a server-made
    principal with no grant gets nothing. The cap is outside
    `identity.SelfAllowed` in the wiring, and `access.Authorizer` denies
    each action of the family `self` to the kind `server` and to the
    credential `server`. So no self action reaches a server-made principal
    before slice 12 deletes `SelfAllowed`. The cap is the catalog entry
    `server-principal-purpose-cap`, with the effect `limits`.
25. **A pause is visible, not the lock.** A revoke commits first. Then the
    access service calls `access.GrantRevoked`, and the trigger service
    pauses the one trigger of the server-made principal, with the reason
    `grant-revoked`. An error of the pause does not undo the revoke. The
    Authorizer refuses a start with no grant, also when no pause exists.

    The dispatcher pauses a trigger with the reason `grant-missing` only for
    a denial whose `identity.DeniedError` has the cause `no-grant` for the
    purpose action, on a start by the server-made principal of that
    trigger. Every other refusal fails the start, writes its audit entry and
    does not pause: the owner cap, the subject guard, the purpose cap, the
    switch, a cause `fail-closed` and a denial with an unknown cause. So an
    error of a store never leaves a trigger paused.

    A pause of either reason ends when a person saves the trigger again and
    `PurposeGrants.Ensure` makes the grant again: the owner holds the
    action, and the escalation check passes. The trigger service then
    resumes that trigger, with an audit entry. A save ends the pause of its
    own trigger only. A transfer of the owner does not end a pause by
    itself.

    A start that the Authorizer allowed before a revoke committed can still
    make its job, the same as for a person. The job service reads the
    switch in the transaction of the start, but not the purpose grant.
26. **The server-runs switch.** The switch of each namespace is on by
    default. The API shows the effective value also for a namespace that
    never set it, with the source "default". The job service reads it in
    the transaction of each start by a server-made principal, and refuses
    the start with `job.ErrServerRunsOff` when the switch is off. The
    dispatcher reads it before it starts, as a second layer. A new action
    `namespace:manage-server-runs`, in the family data, changes the switch.
    The built-in role `admin` holds it. The audit entry of the change holds
    its reason. Starts by a person do not change.
27. **The bootstrap administrator is a person with the source `bootstrap`.**
    A person holds `DUSTPAN_ADMIN_TOKEN`, so the bootstrap administrator is
    a principal of the kind `person`, not a server-made principal. Its
    record has the new source `bootstrap`. Its grant is a stored grant of
    the role `site-administrator` on the installation, and a person can
    revoke it.
    - The server makes the record and the grant once for each subject of
      the static tokens, at the first boot that sees the subject, with an
      audit entry that names the server as the actor. `identity.StaticTokens`
      gives one subject for each token (`admin`, `admin-2` and so on), so
      the server makes one stored person for each subject.
    - The installation stores a marker for each subject that it made. A
      later boot never makes the record or the grant of that subject again,
      also after a person revokes the grant or deletes the record. So a
      revoke holds, and no boot makes a hidden grant again.
    - After a revoke, the token authenticates a principal with no grant. It
      can do only what its other stored grants give. After a delete of the
      record, the token authenticates no principal and gets
      `identity.ErrUnauthenticated`.
    - A restore brings back the records, the grants and the markers of the
      backup. The next boot makes only the subjects that have no marker in
      the restored data.
    - The guard of the last site administrator applies to it, as to any site
      administrator. ADR 53 decision 9 still asks the first person who signs
      in to retire the bootstrap token. The creator of a namespace gets the
      stored grant of `admin` on it also when the creator is the bootstrap
      administrator.
    - Break-glass stays an explicit, audited and pre-staged policy. The
      temporary administrator of ADR 245 is the model.
28. **The self role.** A new built-in role `own-account` holds the actions of
    the family `self`. The server gives each principal of the kind `person`
    a grant of `own-account` on its own principal object
    (`identity.PrincipalObject`), at the create of the record, in the same
    transaction. The Authorizer resolves the caller object of a request to
    the principal object of the caller before it reads the grants, so the
    grant explains itself and needs no extra code rule. A grant on one
    principal object gives no right on any other principal, and the access
    review shows it on the principal, not on the installation.
    `identity.SelfAllowed` then goes. A person can revoke the grant. Without
    it, the person cannot manage its own preferences or make new tokens.
    Service principals and workloads lose the self actions that
    `SelfAllowed` gives them today: the domain services already refuse these
    actions for each kind but `person`.
29. **Revoke of one's own credentials.** Decision 13 is the catalog entry
    `own-credential-revoke`, with the effect `allows`. A new action
    `caller:revoke-own-credentials`, in the family `self`, revokes a token
    or a session of the caller, on the caller object only. The entry allows
    it to each principal of the kind `person`, with the credential `session`
    or `access-token`, also with no grant of `own-account`. It allows no
    other action: a person without the grant cannot list or make tokens.
30. **The limits of the catalog.** These rules keep magic out of the catalog:
    - An entry with the effect `allows` names at least one action and at
      least one credential. Its credentials authenticate no principal, for
      example the runner key and the join token of the runner protocol. The
      one exception is `own-credential-revoke` of decision 29, which names
      its action and its credentials exactly. The guard test checks each
      `allows` entry against these rules.
    - An "explain why" answer (`access.Reason`) names, for each allowed
      action, one stored grant or one `allows` rule, and the list of the
      `limits` and `exempts` rules that applied to the same decision, for
      example the owner cap.
    - The guard test finds each type of the module that implements
      `identity.Authorizer` or `identity.Authenticator`, and each decision
      of access that reads `Principal.Kind`, `Principal.Credential`, the
      `system:` prefix of a subject, `Principal.Admin`,
      `Principal.TemporaryAdministrator`, `Principal.JobTokenID` or
      `Principal.JobID`, or a break-glass marker. It fails for each such
      place that names no catalog entry, and for each entry that names no
      place.

### The first entries of the catalog

| ID | Effect | What it does |
| --- | --- | --- |
| `server-subject-guard` | limits | The Authorizer denies each `system:` subject unless it has the kind `server` and the credential `server`, and each mixed pair of kind and credential (decision 22). |
| `server-credential-authn-guard` | limits | No authenticator gives the credential `server`, the kind `server` or a `system:` subject (decision 23). |
| `server-principal-purpose-cap` | limits | A server-made principal acts only with the actions of its purpose kind, on the unit of its trigger (decision 24). |
| `server-principal-owner-cap` | limits | A start by a server-made principal needs the current grants of the owner of the trigger. Check 2 of ADR 213 decision 1 for its job uses them too (decision 18). |
| `server-principal-no-origin` | exempts | The IP allowlist does not apply to a server-made principal (decision 2). |
| `own-credential-revoke` | allows | A person can always revoke its own tokens and sessions (decisions 13 and 29). |
| `job-token-unit-limit` | limits | A job token acts only on the unit of its job, while its claim is live (ADR 100 decision 20, ADR 133 decision 12). |
| `job-token-output-reads` | limits | A read of outputs with a job token needs a sharing rule and the grants of the starter (ADR 213 decision 1). |
| `runner-key-claim` | allows | A runner key claims the jobs of its pool through the runner protocol only (ADR 133 decision 1). |
| `runner-join-token` | allows | A join token registers one runner in one pool (ADR 133 decision 2). |
| `break-glass-limit-exemption` | exempts | The access limits do not apply to a session of a break-glass account (ADR 223 decision 7). |
| `last-site-administrator` | limits | No change can leave the installation with no site administrator. |
| `host-recovery-command` | allows | The host command on the host of the server makes a temporary administrator with a stored grant (ADR 245). |

The slice of the catalog checks each row against the code, and adds the rows
that it finds.

### Conformance tests to add

The repository has no build tag for a suite that no adapter implements yet.
So this ADR writes each contract in the doc comments of the port, and lists
the cases here. In each slice, the first commit adds the cases to the suite
of its port, and the cases fail before the commit of the adapter.

- `principaltest`, a new `RunServerPrincipals`:
  1. `EnsureServerPrincipal` makes the principal record of the kind
     `server` and the record of the purpose in one transaction, with one
     audit entry.
  2. A second call for the same trigger returns the same principal. With
     the source `last-saver` and a new owner, it sets the owner and appends
     one entry. With the source `transfer`, it keeps the owner and appends
     no entry. Another trigger, also of the same unit, gives another
     principal.
  3. A stale fence, a fence of another namespace, a unit of another
     namespace, an unknown purpose kind and a second call with another unit
     or kind change nothing.
  4. `ListServerPrincipals` lists only the principals of its namespace, in
     order, with the total. `ListNamespacePrincipals` lists them with the
     kind `server`.
  5. `SetOwner` keeps every grant and appends one entry with the source. An
     ended owner gives `ErrNotFound`.
  6. `EndServerPrincipal` sets the end time once. A second call gives
     `ErrNotFound`.
  7. `CreatePrincipal`, `CreateNamespacePrincipal`,
     `UpdateNamespacePrincipal`, `DeleteNamespacePrincipal` and `AddMember`
     refuse a principal of the kind `server`.
- `accesstest`: a grant of `plan-starter` to a server-made principal on the
  unit of its trigger, made by the server path, is stored, listed by
  `GrantsOf` and `ListGrantsOn`, and ended by `RevokeGrant`. A grant to it
  on an object outside its namespace gives `ErrInvalid`. A grant request of
  a person for a server-made principal gives an error that wraps
  `principal.ErrServerPrincipal`, for every role and object. An owner with
  `Admin: true` and no stored grant gives `ErrEscalation`.
- `identitytest`, a new `RunServerPrincipal` that replaces
  `RunNamespaceService`:
  1. A server-made principal with a stored grant of `plan-starter` on the
     unit of its trigger is allowed `unit:run-plan`. With no grant, it is
     denied with the cause `no-grant`.
  2. Each other action, another unit, a unit with no resolved stable ID and
     another namespace are denied, also with a grant that gives them (the
     purpose cap).
  3. The kind `server` with another credential, and the credential `server`
     with another kind, are denied every action.
  4. A principal of the kind `person`, `service` or `workload`, or with no
     kind, with a `system:` subject in any letter case, and with any
     credential, is denied every action, also with a stored grant on its
     subject.
  5. Each action of the family `self` on the caller object is denied to the
     kind `server`.
  6. `ListObjects` gives the scope of its stored grants, inside the cap.
  - `identitytest.Run` loses the cases of the admin flag, and gains a case
    for each kind and credential: no subject, kind, credential or flag
    allows an action with no grant. An allowed action always has a reason.
- `jobtest`: the switch is on for a namespace that never set it, with the
  source "default". A change stores the switch and its reason, with one
  audit entry. A stale fence and an unknown namespace change nothing. With
  the switch off, a start by a server-made principal gives
  `ErrServerRunsOff` and makes no job, and a start by a person makes its
  job.
- `triggertest`, for `PauseStore`: `Pause` pauses one trigger once. A second
  call pauses nothing new and appends no entry. `Paused` and `ListPaused`
  give the pauses. `Resume` ends one pause. A stale fence changes nothing.
- A dispatcher test: a denial with the cause `no-grant` pauses with
  `grant-missing`. A denial with the cause `code-rule` or `fail-closed`, a
  plain `ErrDenied` and `ErrServerRunsOff` fail the start, write an entry
  and pause nothing.
- A guard test of the catalog, as decision 30 says.
- An authenticator test: no authenticator of the module gives the credential
  `server`, the kind `server` or a `system:` subject.
- A test of `ServerCredentialGuard` around a hostile authenticator that
  gives each such principal: each one gets `ErrUnauthenticated` and one
  audit entry.

### The slices

One pull request for each slice, in this order. Each slice is safe alone:
no slice leaves a path that allows more than `main` allows before it.

1. **Code-rule catalog.** `identity.CodeRules()` with the entries of the
   table, checked against the code. The guard test of decision 30.
   `access.Access` gains `Reasons []Reason`, and the "explain why" endpoint
   answers them. The Authorizer fails closed when it cannot name a reason.
   Docs page in `docs/explanation/`.
2. **Server subject guards.** The guards of decisions 22 and 23:
   `identity.ServerSubjectGuard` outside every Authorizer in `wireTokens`,
   the same guard in `access.Authorizer`, the denial of the family `self`
   to the kind `server` and to the credential `server`, and
   `identity.ServerCredentialGuard` around the whole chain of
   authenticators. The `identitytest` cases 3 to 5, the authenticator test
   and the test of the authenticator guard. The two catalog entries.
3. **Kind `server` and `ServerPrincipalStore`.** `identity.KindServer` in
   `Kinds`, the audit principal kind, and the four switches that map audit
   kinds. Migrations (SQLite, PostgreSQL), sqlc queries,
   `principaltest.RunServerPrincipals` first, both adapters. `RecordStore`,
   `NamespaceRecordStore` and `MemberStore` refuse the kind with
   `ErrServerPrincipal`, and each clause moves to the doc of its own port.
4. **Grant path for server-made principals.** The built-in role
   `plan-starter`. `access` accepts a grant to a principal of the kind
   `server` inside its namespace from the server path only, and refuses the
   request of a person. The escalation check reads the stored record and
   the stored grants of the owner, never `Admin`. The audit entry names the
   server and the reason. The `accesstest` cases. The `trigger.PurposeGrants`
   adapter, called by the trigger save, and the owner rules of decision 18.
5. **The dispatcher acts as the server-made principal.** The purpose cap
   decorator replaces `NamespaceServiceRule` in `wireTokens`, outside
   `SelfAllowed`. `identitytest.RunServerPrincipal` replaces
   `RunNamespaceService`. The owner check at use, and check 2 of ADR 213
   decision 1 against the owner. The end of the principal at the delete of
   a trigger. Delete `NamespaceService`, its prefix guard and the glossary
   entry. Update `docs/guides/run-triggers.md`. A trigger that a person
   saved before slice 4 has no server-made principal. Its starts fail
   closed, with an audit entry and no pause, until a person saves it again.
   The project has no users yet, so the slice makes no backfill.
6. **Server-runs switch.** `ServerRunsSettings` store, migration, the
   `jobtest` cases, the check in the job start transaction and in the
   dispatcher, the action `namespace:manage-server-runs`, the route with the
   effective value and its policy entry, the API reference and the guide.
7. **IP allowlist.** The gate skips the address check for a server-made
   principal (`server-principal-no-origin`), with its test row and its doc
   line. It needs slice 2: the guards keep a forged principal away from
   the exemption.
8. **Revoke pauses triggers.** `PauseStore`, migration, `triggertest`
   cases, the `access.GrantRevoked` call after a revoke, the `grant-missing`
   pause in the dispatcher on the cause `no-grant` only, the resume on
   save, the dispatcher test, the API field and the guide.
9. **Owner transfer and review.** The route for `SetOwner`, with the
   escalation check against the new owner, a person or a group. "Owner lost
   access" and "owner has no member" in the access-review export and in the
   admin access report. The quarterly review of server-made principals.
   Docs.
10. **Bootstrap administrator as a stored principal.** The source
    `bootstrap`, one record and one `site-administrator` grant for each
    static subject at its first boot, audited, with the marker of decision
    27. `DUSTPAN_ADMIN_TOKEN` authenticates to it. The namespace creator
    grant also for it. The admin flag stays in this slice.
11. **Delete the admin flag.** `Principal.Admin`, `AdminOnly`, `AdminScope`,
    `AdminSubjects` and every shortcut in `access`. The default authorizer
    is `access.Authorizer`. `identitytest.Run` loses the admin cases. ADR 6
    consumers in `pkg/dustpan` and `cmd/opendustpan`.
12. **Self actions as stored grants.** The built-in role `own-account`, a
    grant to each person on its own principal object at create, and a
    migration for the persons that exist. The action
    `caller:revoke-own-credentials` and the entry `own-credential-revoke`.
    Delete `identity.SelfAllowed` and the `FamilySelf` shortcut. The console
    shows the grant.

Slices 2 to 9 depend on each other in order. Slices 10 to 12 depend on
slice 1 only, and 11 depends on 10. Slice 7 depends on slice 2.

### What stays, and what this ADR supersedes

- **ADR 6, in part.** The sentence "The default authorizer allows
  administrators only" goes, and so does the admin flag that it implies.
  The rest of ADR 6 stays: the two ports, the tokens that OpenDustpan
  issues, the sign-in through SSO for people, and `DUSTPAN_ADMIN_TOKEN` as
  the bootstrap credential. That token now authenticates the stored
  bootstrap administrator of decision 27.
- **ADR 267 decision 14, in part.** "That principal holds only the action
  that starts a plan" as a rule of code goes. The start runs as the
  server-made principal of the trigger, with a stored grant. The check of
  the last saver becomes the check of the owner of the trigger (decision
  18). The owner is the last saver until a person transfers the ownership.
  After a transfer, check 2 of ADR 213 decision 1 and the check at each
  start use the grants of the new owner, not of the last saver. The rest of
  decision 14 stays: the server checks at each read and fails closed, never
  rebuilds the admin flag, gives the starting principal no read action, and
  records the saver.
- **Issue 1216, decision r3, in part.** "Grants fixed to `unit:run-plan`
  (grant API refuses widening)" goes. The grant is stored and revocable.
  The grant API refuses each grant request of a person for a server-made
  principal (decision 19), and the purpose cap of decision 24 keeps the
  principal to its purpose. The rest of r3 stays: the principal has no
  token, the server owns it, and the dispatcher checks the owner at each
  start.
- **Extends ADR 52 decision 14.** The answer names a grant or an `allows`
  rule for each allowed action, and the `limits` and `exempts` rules that
  applied.
- **Extends ADR 67.** A server-made principal is not a service principal.
  Nobody makes its tokens, because it has none.
- **Extends ADR 70.** No act-as edge reaches a server-made principal, so the
  reach of a principal never holds one.
- **Unchanged.** ADR 53, ADR 57, ADR 70 decisions 23 to 29, ADR 142, ADR 216
  decision 11, ADR 223 decision 7 (now a catalog entry) and ADR 245 stay as
  they are.

## Considered options

- **The kind `service` with a marker "made by the server".** Each guard
  would have to read two fields, and a service principal can hold tokens.
  Rejected for decision 14.
- **One server-made principal for each namespace and purpose kind, for all
  units.** Fewer records. But one grant of `unit:run-plan` on the namespace
  then serves every unit, and no trigger has its own owner. Rejected for
  decision 15.
- **One server-made principal for each purpose kind and unit.** Fewer
  records than one for each trigger. But the triggers of one unit then share
  one owner and one pause, and a save by one person changes the owner of
  the triggers of another person. The owner chose one for each trigger
  (decision 11).
- **A save always makes the saver the owner.** Simple, but a save then
  undoes a transfer with no request to do so. Rejected for decision 18.
- **The grants of the members of a group owner.** A member who leaves would
  then change what the principal can do. Rejected for decision 18: the
  stored grants of the group decide.
- **A stored flag "owner lost access".** It can drift from the grants.
  Rejected for decision 18.
- **A pause in the transaction of the revoke.** The access store would need
  to know the triggers. The pause is not a security layer, so a call after
  the commit is enough. Rejected for decision 25.
- **A pause on each refusal of the Authorizer.** A store error would then
  leave a trigger paused until a person saves it again. Rejected for
  decision 25.
- **A grant of `own-account` on the installation.** It needs an extra code
  rule, "a self action applies to the caller only", and gives the access
  review one installation row for each person. Rejected for decision 28.
- **Self actions as a listed code rule.** Most products allow self actions
  by default. The owner chose stored grants (decision 10), as in AWS IAM,
  where a policy allows a user to change their own password.

## Consequences

- This pull request adds the ADR, the glossary terms, the port types and
  their contracts. The doc comment of `identity.Authorizer` states the new
  contract and names the code that does not meet it yet.
  `principal.ParseKind` and `accesstoken.KindFor` refuse the kind `server`
  in their own case, with their own message. Both refused it before,
  through their default case, with the same sentinel error.
  `identity.DeniedError` is a new error type that no code returns yet. No
  other behavior changes.
- The namespace service principal, `NamespaceServiceRule`,
  `RunNamespaceService`, `Principal.Admin`, `AdminOnly`, `AdminScope`,
  `AdminSubjects` and `SelfAllowed` stay until their slices delete them.
- An embedder that uses `dustpan.WithAuthorizer` must decide by stored
  grants. An embedder that uses `dustpan.WithAuthenticator` can never give
  a `system:` subject, the kind `server` or the credential `server`. The
  SDK is a public API, but no tag shipped it, so no deprecation period
  applies (ADR 73).
- The console shows server-made principals, their trigger, owner and
  grants, the paused triggers and their reason, and the server-runs switch.
  Each slice that adds a visible feature adds its page in the user
  documentation (ADR 214).
- Each slice that changes the Pulumi Cloud-compatible API or the
  TFE-compatible API runs `make e2e`. The slices of this ADR do not plan
  such a change.
