# 67. Who creates and administers a service principal

Status: accepted · 2026-10-14. The `server:` action names superseded in part by ADR 210. Decision 7 superseded in part by ADR 215: persons, workloads and groups of a namespace

Changed by:

- ADR 70 changes decisions 2, 3, 4 and 5: reach checks on token rights and
  personal tokens.
- ADR 70 extends decision 6: the access review flags tokens and lists trust
  rule authors.
- ADR 210 changes: the route paths and the server: action names.
- ADR 215 changes decision 7: persons, workloads and groups of a namespace.
- ADR 369 extends decisions 1 and 6: a service principal from an external
  identity system, keyed by its external ID, with a named owner.

## Context

ADR 53 decision 3 makes the issue of each kind of access token an action of
the authorization model. The code then added an escalation check: a person
who makes or rotates a token for another principal must hold every action
that the grants of that principal give.

Issue #531 found a gap in that check. The check runs only when the token is
made or rotated. A token acts with the grants of its principal at each
request. So when a site administrator later gives the principal a new role,
the person who made the token can use that role through the token, though
nobody gave it to that person.

The project owner found the cause on 2026-10-14. The check compared the
actions of the caller with the actions of the principal. Other products ask
another question: "Am I an administrator of this service principal?" The
owner proposed: "Alice is an admin or has been granted access to mint tokens
for the service principal, therefore the check passes."

The lead compared other products on 2026-10-14:

- Google Cloud: a person needs its own role (Create Service Accounts) to
  make a service account. The creator gets no access to the new account. A
  separate role on the account, or on its project, decides who can act as
  it. A key or a
  short-lived token acts with every role of the account, now and later.
- AWS IAM: making a user and making its access keys are separate
  permissions.
- Microsoft Entra ID: by default any user can register an application, and
  the creator becomes its first owner. An administrator can turn this off
  and give the right to a role instead.
- Kubernetes: a service account belongs to one namespace.
- HCP Terraform first let any member of a team manage the team token, and
  then added a setting so that administrators can take that right away.

The owner accepted three points on 2026-10-14 ("Strong yes"):

1. Making a service principal stays an installation administration action.
2. Nobody administers a service principal as a side effect of making it.
3. A namespace can have its own service principals, which can only ever hold
   grants inside that namespace.

## Decision

1. **Making a service principal of the installation is an administration
   action.** It needs `server:manage-principals` on the installation, as
   today. The creator gets no grant on the new principal.
2. **The right to make tokens for a service principal is a grant on that
   principal.** The action `principal:manage-tokens` has the service
   principal as its object. A caller can make, rotate or revoke an access
   token of a service principal when it holds this action on the principal,
   or on the installation that holds the principal. The built-in roles that
   hold `server:manage-service-tokens` on the installation hold it.
3. **The server does not compare the actions of the caller with the actions
   of the principal.** This replaces the escalation check of token creation
   and rotation. A holder of `principal:manage-tokens` on a principal can act
   as that principal, with every grant that the principal has now or gets
   later. Issue #531 describes this behavior, and this decision accepts it.
4. **Only a principal that can manage the access of a service principal can
   give the right to make its tokens.** A grant of a role with
   `principal:manage-tokens` on a principal needs `server:manage-access` on
   the installation, or, for a principal of a namespace (decision 7),
   `namespace:manage-access` on that namespace.
5. **A person makes the personal tokens of that person.** Decision 10 of
   ADR 53 does not change. Only the person, or an installation administrator
   as ADR 53 allows, makes a personal token.
6. **The access to a service principal is visible.**
   - The access-review export (ADR 52 decision 10) lists, for each service
     principal, the principals and groups that hold
     `principal:manage-tokens` on it.
   - The audit entry of a grant to a service principal names the principals
     and groups that can make its tokens at the time of the grant. The
     console shows this list before a person sends such a grant.
7. **A namespace can have its own service principals.**
   - A principal of a namespace belongs to one namespace. A holder of the new
     action `namespace:manage-principals` on the namespace makes it. The built-in
     namespace administrator role holds this action.
   - A grant to a principal of a namespace can only name that namespace, or
     an object inside it. The server refuses any other grant.
   - A principal of a namespace cannot be a member of a group, because a
     group belongs to the whole installation (ADR 52 decision 2).
   - A holder of `principal:manage-tokens` on the namespace can make the
     tokens of every principal of that namespace. The built-in namespace
     administrator role holds it.
   - When a person deletes the namespace, the server revokes the tokens of
     its principals and disables them. A purge of the namespace deletes them.
8. **This changes ADR 52.** Decision 1 of ADR 52 says that every principal
   belongs to the whole installation. A principal of a namespace (decision 7)
   is the exception. Decisions 3 and 4 of ADR 52 do not apply to a principal
   of a namespace where decision 7 limits them.

## Not decided here

- Whether a workload of ADR 54 can be a principal of a namespace. The trust
  rules of ADR 54 already belong to a namespace.
- A principal of a namespace that moves with its namespace to another
  installation (RFC 66).

## Consequences

- The escalation check of token creation goes away. A token service asks
  the authorizer for `principal:manage-tokens` on the target principal.
- A grant can name a principal as its object. The access model, the stores
  and the access-review export need this object kind.
- The security model guide states that the right to make tokens for a
  principal is the right to act as it.
- An account administrator can delegate the tokens of one service
  principal to a team without giving that team the access of the principal.
- A team can make and run its own service principals inside its namespace
  without an installation administrator, and without access outside it.

## Note on the tokens of a namespace

The action `namespace:manage-principal-tokens` has a namespace as its
object. It gives `principal:manage-tokens` on the principals of that
namespace only, through a grant on that namespace. The built-in role
`admin` holds it. It gives no other action on a principal. On the
installation, only `principal:manage-tokens` gives the tokens of a
principal. A grant of a role with it needs
every data action on the namespace of its object, or `server:manage-access`
on the installation. A grant on the installation needs
`server:manage-access`.
