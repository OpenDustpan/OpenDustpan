# 277. No implicit authority: every right is a stored grant

Status: accepted · 2026-10-25. Decisions 1 to 11 are owner decisions of 25 October 2026 (issue 1841). Decisions 12 to 22 are the design of the author, and follow from them. Supersedes in part ADR 6, ADR 267 decision 14 and the decision r3 of issue 1216. Extends ADR 52 decision 14, ADR 67 and ADR 70

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
each quarter. PCI DSS v4.0.1 requirements 8.6.1 to 8.6.3 manage the risk of a
system account with a credential that a person can present. A principal with
no such credential leaves nothing for them to manage. ARBAC97 section 3.2
keeps the grants that an administrator made when the administrator loses a
role. Its model of delegation (section 3.1) bounds a grant by the rights of
the granter.

## Decision

### Decisions of the owner

1. **No magic (owner).** Every permission is a stored grant with a holder,
   a role, an object, a creator and a time. Users can list, audit and revoke
   it. Code may define roles and actions. Code never defines the holder of a
   grant.
2. **Server-made principals are first-class (owner).** A server-made
   principal is a principal of its namespace. Its record says that the
   server made it, and holds its purpose, the object that it serves, its
   accountable owner, its grants and its last use. Each principal that can
   read access on the namespace sees it in the default list. The access-review
   export and the admin access report list it.
3. **The server grants through the same path as a person (owner).** The
   server makes the grants of a server-made principal through the grant path
   of a person. The escalation check runs against the accountable owner: a
   person can never give a server-made principal more rights than the person
   holds. The audit entry of the grant names the server as its actor, and
   gives the reason, for example the save of a trigger.
4. **Any revoker can revoke one grant at a time (owner).** A principal with
   the right to revoke a grant can revoke any grant of a server-made
   principal, one grant at a time. When a purpose needs the revoked grant,
   the server pauses the triggers that need it, with a reason that the API
   shows. The server deletes no trigger.
5. **No impersonation (owner).** No credential, token, trust rule or act-as
   edge can name a server-made principal. Only the server process acts as it.
   A namespace has at most one server-made principal for each purpose.
6. **An accountable owner (owner).** By default, the owner is the last saver
   of the trigger. A person can transfer it to another person or to a group.
   When the owner loses access, the grants stay, and runs fail closed. The
   access review flags "owner lost access" until a new owner takes over.
   No revoke cascades from the owner to the grants.
7. **No network origin (owner).** A server-made principal has no network
   origin, so the IP allowlist does not apply to it. It gets no
   configurable exemption. A switch in each namespace turns off all runs
   that the server starts in that namespace.
8. **The admin flag goes, in one step (owner).** `Principal.Admin` and the
   admin-only default authorizer go in one step, because the project has no
   users yet. The bootstrap administrator is a stored principal with a stored
   grant of the role `site-administrator` on the installation. A person can
   revoke the grant. The server makes the principal and the grant at the
   first boot, and audits both. Break-glass stays an explicit, audited and
   pre-staged policy. The temporary administrator of ADR 245 is the model.
9. **Self actions are stored grants (owner).** The self actions, for example
   `token:manage-own` and `caller:manage-preferences`, become stored grants
   of a built-in role. Each person gets a grant of that role. The API lists
   it as every other grant.
10. **One catalog of code rules (owner).** Each rule that stays in code is in
    one catalog. Examples are the unit limit of a job token, the rights of a
    runner credential, and the exemption of break-glass sessions from the
    access limits. The "explain why" answer of ADR 52 decision 14 shows the
    rules that applied. A test fails when a new rule in code has no entry in
    the catalog. For each allowed action, the answer names a grant or an
    entry of the catalog.
11. **A review each quarter (owner).** The access review lists the
    server-made principals, and a reviewer reviews them each quarter.

### Design decisions

12. **A new principal kind `server`.** A server-made principal has the kind
    `server` (`principal.KindServer`), not the kind
    `service` with a marker. A service principal can hold tokens (ADR 67). A
    server-made principal can never hold one. A separate kind lets each
    guard refuse it by its kind alone. `principal.ParseKind` refuses
    `server`, so no request can make or name the kind. Only
    `principal.ServerPrincipalStore` stores it. `accesstoken.KindFor` gives
    no token kind for it. The kind joins `identity.Kinds` and the audit kinds
    in the slice that stores the first such principal.
13. **The purpose is a kind and a served unit.** A purpose
    (`principal.Purpose`) is a purpose kind and the unit that it serves, by
    the stable ID of the unit. The first kinds are `scheduled-runs` and
    `upstream-runs`. So "one server-made principal for each purpose" means
    one for each pair of kind and unit. A grant for the schedule triggers of
    unit A never serves unit B, or the follows triggers of unit A. Each
    purpose kind lists in code the actions that its principal can hold. For
    both first kinds, the list is `unit:run-plan` on the served unit.
14. **The record.** `principal.ServerPrincipal` holds the ID of the
    principal record, the namespace, the purpose, the owner, the time of
    the last change of the owner, the time of the create and the last use.
    It holds no grant and no credential. The grants are ordinary rows of the
    access store. The subject of the principal record starts with
    `system:`, a prefix that the principal API already refuses. Its form is
    `system:server:<namespace>:<purpose kind>:<unit ID>`.
15. **The ports.** This ADR adds these ports. The consuming package owns
    each one, and its doc comments hold the contract:
    - `principal.ServerPrincipalStore`: `EnsureServerPrincipal`,
      `ServerPrincipalFor`, `ListServerPrincipals` and `SetOwner`. The
      writes of `RecordStore`, `NamespaceRecordStore` and `MemberStore`
      answer `principal.ErrServerPrincipal` for a principal of the kind
      `server`. Their reads list it.
    - `trigger.PurposeGrants`: `Ensure` makes the principal of a purpose
      and its grants at the save of a trigger, through the grant path of a
      person, with the escalation check against the owner.
    - `trigger.PauseStore`: `PauseNeeding`, `Paused` and `Resume`.
    - `access.GrantRevoked`: the access service tells the trigger service
      that a revoke ended a grant of a server-made principal.
    - `job.ServerRunsSettings`: `ServerRuns` and `SetServerRuns`, the
      switch of decision 7.
    - `identity.CodeRule`: the type of an entry of the catalog of decision
      10, with the effects `allows`, `limits` and `exempts`.
    - `access.Reason`: one action of an "explain why" answer, with a grant
      ID or a code rule ID.
    The existing `access.GrantStore.RevokeGrant`, `ListGrantsOn` and
    `GrantLookups.GrantsOf` list and revoke the grants of a server-made
    principal. They need no new method.
16. **The Authorizer port has no special principal.** The contract of
    `identity.Authorizer` changes in this ADR. An Authorizer allows an
    action only for a stored grant, or for a code rule with the effect
    `allows`. A subject, a kind, a credential or a flag never allows an
    action by itself. A principal of the kind `server` with another
    credential than `server` is denied every action. A principal with the
    credential `server` of another kind is denied too. The paragraph on the
    namespace service principal stays until a later slice deletes it.
17. **A purpose cap stays in front of every Authorizer.** A decorator
    replaces `identity.NamespaceServiceRule`. For a principal of the kind
    `server`, it denies each action that is not in the list of its purpose
    kind, and each object that is not the served unit. Then it asks the
    inner Authorizer, which reads the stored grants. The cap only takes
    away: a server-made principal with no grant gets nothing. So a stored
    grant decides, and the cap is a defense-in-depth layer for an
    Authorizer of an embedder. The cap is an entry of the catalog, with the
    effect `limits`.
18. **The owner check at use stays.** Each start by a server-made principal
    still checks the current grants of the owner, as ADR 267 decision 14
    checks the last saver. A missing, deleted or disabled owner means deny.
    For an owner group, the server checks the grants of the group. This
    check is an entry of the catalog, with the effect `limits`. The flag
    "owner lost access" is not stored: the access review computes it from
    the current grants of the owner. The time comes from the audit entry of
    the first refused start.
19. **A pause is visible, not the lock.** A revoke commits first. Then the
    access service calls `access.GrantRevoked`, and the trigger service
    pauses the triggers that need the grant. An error of the pause does not
    undo the revoke. The Authorizer refuses a start with no grant, also when
    no pause exists. When the Authorizer refuses a start for a missing grant,
    the dispatcher pauses the trigger with the reason `grant-missing`. A new
    save by an owner that holds the actions makes the grant again and ends
    the pause.
20. **The server-runs switch.** The switch of each namespace is on by
    default. The job service reads it in the transaction of each start by a
    server-made principal, and refuses the start with `job.ErrServerRunsOff`
    when the switch is off. The dispatcher reads it before it starts, as a
    second layer. A new action `namespace:manage-server-runs`, in the family
    data, changes the switch. The built-in role `admin` holds it. The audit
    entry of the change holds its reason. Starts by a person do not change.
21. **The bootstrap administrator is a person with the source `bootstrap`.**
    A person holds `DUSTPAN_ADMIN_TOKEN`, so the bootstrap administrator is a
    principal of the kind `person`, not a server-made principal. Its record
    has the new source `bootstrap`. The server makes the record and the grant
    of `site-administrator` on the installation at the first boot. The audit
    entry names the server as the actor. The guard of the last site
    administrator applies to it, as to any site administrator. ADR 53
    decision 9 still asks the first person who signs in to retire the
    bootstrap token. The creator of a namespace gets the stored grant of
    `admin` on it also when the creator is the bootstrap administrator.
22. **The self role.** A new built-in role `own-account` holds the actions
    of the family `self`. The server gives each principal of the kind
    `person` a grant of `own-account` on the installation, at the create of
    the record, in the same transaction. The actions of the family `self`
    apply to the caller object only, so the grant on the installation gives
    no right on any other principal. `identity.SelfAllowed` then goes. A
    person can revoke the grant. Without it, the person cannot manage the
    tokens or the preferences of their own account.

### The first entries of the catalog

| ID | Effect | What it does |
| --- | --- | --- |
| `server-principal-purpose-cap` | limits | A server-made principal acts only with the actions of its purpose kind, on its served unit (decision 17). |
| `server-principal-owner-cap` | limits | A start by a server-made principal needs the current grants of its owner (decision 18). |
| `server-principal-no-origin` | exempts | The IP allowlist does not apply to a server-made principal (decision 7). |
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
the cases here. Each slice adds its cases to the suite of its port before an
adapter implements the method.

- `principaltest`, a new `RunServerPrincipals`:
  1. `EnsureServerPrincipal` makes the principal record of the kind
     `server` and the record of the purpose in one transaction, with one
     audit entry.
  2. A second call for the same purpose returns the same principal and
     appends no entry. Another unit or another purpose kind gives another
     principal.
  3. A stale fence, a fence of another namespace, a served unit of another
     namespace and an unknown purpose kind change nothing.
  4. `ListServerPrincipals` lists only the principals of its namespace, in
     order, with the total. `ListNamespacePrincipals` lists them with the
     kind `server`.
  5. `SetOwner` keeps every grant and appends one entry. An ended owner
     gives `ErrNotFound`.
  6. `CreatePrincipal`, `CreateNamespacePrincipal`,
     `UpdateNamespacePrincipal`, `DeleteNamespacePrincipal` and `AddMember`
     refuse a principal of the kind `server`.
- `accesstest`: a grant to a server-made principal on its served unit is
  stored, listed by `GrantsOf` and `ListGrantsOn`, and ended by
  `RevokeGrant`. A grant to it on an object outside its namespace gives
  `ErrInvalid`.
- `identitytest`, a new `RunServerPrincipal` that replaces
  `RunNamespaceService`:
  1. A server-made principal with a stored grant of `unit:run-plan` on its
     unit is allowed. With no grant, it is denied.
  2. Each other action, another unit and another namespace are denied, also
     with a grant that gives them (the purpose cap).
  3. The kind `server` with another credential, and the credential
     `server` with another kind, are denied every action.
  4. `ListObjects` gives the scope of its stored grants, inside the cap.
  - `identitytest.Run` loses the cases of the admin flag, and gains a case
    for each kind and credential: no subject, kind, credential or flag
    allows an action with no grant.
- `jobtest`: the switch is on for a namespace that never set it. A change
  stores the switch and its reason, with one audit entry. A stale fence and
  an unknown namespace change nothing. With the switch off, a start by a
  server-made principal gives `ErrServerRunsOff` and makes no job, and a
  start by a person makes its job.
- `triggertest`, for `PauseStore`: `PauseNeeding` pauses each trigger of
  the purpose kind on the unit once, and returns their IDs. A second call
  pauses nothing new. `Paused` lists the pauses. `Resume` ends one pause.
  A stale fence changes nothing.
- A guard test of the catalog: it lists each type of the module that
  implements `identity.Authorizer` or `identity.Authenticator`, and each
  check of `Principal.Kind` or `Principal.Credential` in a decision of
  access. It fails for each such place that does not name a catalog entry.
  It fails for an entry that names no place too.
- An authenticator test: no authenticator of the module gives the
  credential `server` or the kind `server`.

### What stays, and what this ADR supersedes

- **ADR 6, in part.** The sentence "The default authorizer allows
  administrators only" goes, and so does the admin flag that it implies.
  The rest of ADR 6 stays: the two ports, the tokens that OpenDustpan
  issues, the sign-in through SSO for people, and `DUSTPAN_ADMIN_TOKEN` as
  the bootstrap credential. That token now authenticates the stored
  bootstrap administrator of decision 21.
- **ADR 267 decision 14, in part.** "That principal holds only the action
  that starts a plan" as a rule of code goes. The start runs as the
  server-made principal of the purpose of the trigger, with stored grants.
  The rest of decision 14 stays: check 2 of ADR 213 decision 1 uses the
  current grants of the owner, which is the last saver by default. The
  server checks at each read and fails closed, never rebuilds the admin
  flag, gives the starting principal no read action, and records the saver.
- **Issue 1216, decision r3, in part.** "Grants fixed to `unit:run-plan`
  (grant API refuses widening)" goes. The grants are stored and revocable.
  The purpose cap of decision 17 keeps the principal to its purpose. The
  rest of r3 stays: the principal has no token, the server owns it, and the
  dispatcher checks the owner at each start.
- **Extends ADR 52 decision 14.** The answer names a grant or a code rule
  for each allowed action.
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
  Rejected for decision 12.
- **One server-made principal for each namespace and purpose kind, for all
  units.** Fewer records. But one grant of `unit:run-plan` on the namespace
  then serves every unit, and the owner of each trigger cannot be the owner
  of the principal. Rejected for decision 13.
- **One server-made principal for each trigger.** The owner maps one to one.
  But two schedule triggers of one unit then need two principals for one
  purpose. Rejected, because the owner chose one principal for each purpose.
- **A stored flag "owner lost access".** It can drift from the grants.
  Rejected for decision 18.
- **A pause in the transaction of the revoke.** The access store would need
  to know the triggers. The pause is not a security layer, so a call after
  the commit is enough. Rejected for decision 19.
- **Self actions as a listed code rule.** Most products allow self actions
  by default. The owner chose stored grants, as in AWS IAM, where a policy
  allows a user to change their own password.

## Consequences

- This pull request adds the ADR, the glossary terms, the port types and
  their contracts. The doc comment of `identity.Authorizer` states the new
  contract. `principal.ParseKind` and `accesstoken.KindFor` refuse the kind
  `server` in their own case, with their own message. Both refused it
  before, through their default case, with the same sentinel error. No
  other behavior changes.
- The namespace service principal, `NamespaceServiceRule`,
  `RunNamespaceService`, `Principal.Admin`, `AdminOnly`, `AdminScope`,
  `AdminSubjects` and `SelfAllowed` stay until their slices delete them.
- An embedder that uses `dustpan.WithAuthorizer` must decide by stored
  grants. The SDK is a public API, but no tag shipped it, so no
  deprecation period applies (ADR 73).
- The console shows server-made principals, their purpose, owner and
  grants, the paused triggers and their reason, and the server-runs switch.
  Each slice that adds a visible feature adds its page in the user
  documentation (ADR 214).
- Each slice that changes the Pulumi Cloud-compatible API or the
  TFE-compatible API runs `make e2e`. The slices of this ADR do not plan
  such a change.
