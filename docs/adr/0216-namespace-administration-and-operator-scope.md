# 216. Namespace administration and operator scope: where each installation action goes (owner)

Status: accepted · 2026-10-18 · supersedes in part ADRs 65, 70, 124 and 163 (see decisions 3, 8, 11 and 17)

## Context

ADR 215 makes the namespace the tenant of OpenDustpan Cloud. The project
owner decided on 2026-10-18, as the project manager relayed: on the hosted
service, only the operator administers the installation. Each namespace
administrator administers the namespace, its members and its SSO. The
owner asked that each installation action go to operator scope or to
namespace scope.

ADR 210 decision 7 renames the area `server:` to `installation:`, with the
same verbs. The code still has the old values (`pkg/identity/identity.go`).
`pkg/access/access.go` gives each action one family: operator,
administration, data, delegation or self. The role `installation-operator`
holds the operator family. `site-administrator` holds all families. The
role `account-administrator` holds administration, data and delegation on
the installation, except the two site-only actions `manage-sign-in` and
`restore-access`.

The installation actions in the code and in accepted ADRs are: `backup`,
`restore`, `maintenance`, `observe`, `read-audit`, `manage-settings`,
`read-settings`, `set-audit-chain`, `disable-audit-chain`, `manage-audit`,
`read-principals`, `manage-principals`, `manage-groups`, `manage-roles`,
`manage-access`, `read-access`, `create-namespace`, `read-tokens`,
`manage-tokens`, `manage-service-tokens`, `manage-token-policy`,
`manage-sign-in`, `restore-access`, `manage-agent`,
`accept-agent-secret-access`, `link-namespaces`, `review-break-glass`,
`manage-notifications` and `manage-calendars`.

## Decision

### Two scopes

1. **Operator scope and namespace scope (owner).** The operator scope is
   the installation object `@server`. The namespace scope is one namespace.
   On a self-hosted installation with the defaults of ADR 215 decision 19,
   one site administrator holds both scopes. So a self-hosted user sees no
   change in what an administrator can do.
2. **The rule.** An action has operator scope when it changes how the whole
   installation runs, or when it reads or writes data of more than one
   namespace. An action has namespace scope when it changes who can do what
   in one namespace, or when it changes the data of one namespace.
3. **The map of each action.** Operator actions keep the area
   `installation:`. A namespace action takes the area `namespace:` and has
   the namespace as its object. "Both" means that the installation action
   stays for objects of the installation, and the namespace action is new
   or exists already for objects of one namespace.

   | Installation action (ADR 210 name) | Scope | Namespace action | Note |
   | --- | --- | --- | --- |
   | `installation:backup` | operator | `namespace:backup` (ADR 218) | A backup of the installation holds every namespace. |
   | `installation:restore` | operator | `namespace:restore` (ADR 218) | It replaces every namespace (ADR 38). |
   | `installation:maintenance` | operator | none | It stops writes in every namespace. |
   | `installation:observe` | operator | none | The metrics of the installation (ADR 32). |
   | `installation:read-audit` | operator | `namespace:read-audit` (exists) | The `@server` scope (ADR 65). |
   | `installation:manage-settings`, `installation:read-settings` | operator | `namespace:manage-settings`, `namespace:read-settings` (exist) | One settings resource for each scope (ADR 212 decision 7). |
   | `installation:set-audit-chain`, `installation:disable-audit-chain` | operator | none | The chain covers the whole log (ADR 64). |
   | `installation:manage-audit` | both | `namespace:manage-audit` (new) | The installation sinks and checkpoints, or the sinks of one namespace. |
   | `installation:read-principals`, `installation:manage-principals` | operator | `namespace:manage-principals` (exists) | The installation directory: the principals of the installation. |
   | `installation:manage-groups` | both | `namespace:manage-groups` (new) | The groups of each home (ADR 215 decision 9). |
   | `installation:manage-roles` | both | `namespace:manage-roles` (new) | The custom roles of each home. |
   | `installation:manage-access`, `installation:read-access` | operator | `namespace:manage-access`, `namespace:read-access` (exist) | Grants on `@server`. |
   | `installation:create-namespace` | operator | none | The sign-up shim of ADR 59 calls it with a service principal of the installation. |
   | `installation:read-tokens`, `installation:manage-tokens` | operator | `namespace:manage-principal-tokens` (exists) | The tokens of the principals of the installation. |
   | `installation:manage-service-tokens` | operator | none | Deprecated for `principal:manage-tokens` (ADR 67). This ADR does not change it. |
   | `installation:manage-token-policy` | both | `namespace:manage-token-policy` (new) | The installation sets the bounds. A namespace value can only be stricter. |
   | `installation:manage-sign-in` | both | `namespace:manage-sign-in` (new) | The sign-in of the installation, or of one namespace (ADR 215 decision 10). |
   | `installation:restore-access` | operator | `namespace:restore-access` (new) | The access tables of the installation, or of one namespace (ADR 70 decision 7). |
   | `installation:manage-agent` | both | `namespace:manage-agent` (exists) | The model connection of the installation, or of one namespace (ADR 129). |
   | `installation:accept-agent-secret-access` | both | `namespace:accept-agent-secret-access` (new) | The opt-in of ADR 124 decision 6 for one namespace. |
   | `installation:link-namespaces` | none | `namespace:accept-link` (new) | The installation action goes. Decision 8 replaces it. |
   | `installation:review-break-glass` | both | `namespace:review-break-glass` (exists) | The break-glass accounts of each home. |
   | `installation:manage-notifications` | both | `namespace:manage-notifications` (exists) | The rules of the installation, or of one namespace (ADR 145). |
   | `installation:manage-calendars` | both | `namespace:manage-calendars` (exists) | The calendars of the installation, or of one namespace (ADR 140). |

4. **New operator actions.** These act on namespaces as objects of the
   installation. They have operator scope and the operator family:
   - `installation:suspend-namespace`: sets the state `suspended` or
     `active` (ADR 215 decision 16). The request takes a reason, and the
     audit entry of each scope records it.
   - `installation:delete-namespace` and `installation:purge-namespace`:
     the delete and the purge of ADR 47 by the operator, for example at the
     end of the period of ADR 215 decision 16.
   - `installation:read-namespaces`: lists the ID, the name, the state and
     the creation time of each namespace. It never reads a unit, a
     principal, a grant or content of a namespace.
5. **A test for the table.** A test in `pkg/access` iterates each action
   constant of the area `installation:` (and of the old area `server:`
   while the code has it). It fails for each constant that the table of
   decision 3 or the list of decision 4 does not name. A new installation
   action cannot merge without a row.

### Grants across the boundary

6. **Groups and custom roles of a namespace.** With
   `identity.principalHome: namespace`, groups and custom roles that a
   namespace administrator makes belong to that namespace (ADR 215
   decision 9). The groups and custom roles of the installation stay, for
   the operator.
7. **A setting decides whether an installation grant reaches a namespace.**
   The installation setting `access.installationGrantsReachNamespaces` is
   `true` by default. With `true`, a grant on `@server` reaches each
   namespace, as ADR 52 decision 5 says. With `false`, a grant on `@server`
   gives only the actions of the operator scope. It never gives an action
   inside a namespace. OpenDustpan Cloud locks the value `false` in the
   static layer (ADR 63).
8. **A join across namespaces needs both namespaces.** A namespace link
   (ADR 163), an output-sharing rule (RFC 15) and a dependency edge from a
   unit of one namespace to a unit of another namespace each join two
   namespaces. Such a join is active only when an administrator of each
   namespace accepted it, with `namespace:accept-link` (link) or the action
   of the join on each side.
   - The first administrator proposes the join. The join is pending until
     an administrator of the other namespace accepts it.
   - An administrator of either namespace can end the join alone.
   - The operator cannot make a join alone, also not with a grant on
     `@server`. This replaces the sentence of ADR 163 decision 9 that an
     installation administrator makes a link.
   - Each step writes an audit entry in the scope of each namespace.
9. **A sign-in authenticates only principals of its own home.** A sign-in
   through the identity provider of a namespace finds or makes a principal
   of that namespace only. A sign-in through the identity provider of the
   installation finds or makes a principal of the installation only. The
   server never links two principals because their email addresses or
   subjects match. A subject is unique only in its home and its identity
   provider.
10. **Wildcard reads answer only granted namespaces.** A read with
    `namespaces/-` (ADR 210 decision 4) answers only the namespaces in
    which the caller has a grant, or a grant on `@server` when decision 7
    lets it reach them. An operator with `access.installationGrantsReachNamespaces: false`
    reads no namespace through `namespaces/-`.

### Support access

11. **Support access is an ordinary grant.** When support of the
    operator needs to see the data of a namespace, an administrator of that
    namespace grants a role to a named principal of the installation. This
    replaces the recovery through support of ADR 70 decision 28 as a path to
    data:
    - The grant has an end time of at most 72 hours. The server refuses a
      grant to a principal of the installation with no end time or with a
      later end time, when decision 7 is `false`.
    - The grant needs a fresh MFA step of the namespace administrator (ADR
      56).
    - The grant, each action of the support principal under it, and its end
      each write an entry in the audit scope of the namespace.
    - The console of the namespace shows the grant while it lasts.
12. **Recovery of a locked-out namespace.** ADR 70 decision 28 still lets
    the provider check the identity of a requester out of band. The recovery makes a
    temporary administrator of the namespace for one named person of the
    namespace, never a grant to an operator principal. The audit scope of
    the namespace records each step.
13. **Break-glass accounts of a namespace.** On OpenDustpan Cloud, the
    break-glass accounts of ADR 70 decision 23 are principals of the
    namespace with the role `admin` on it, not holders of
    `account-administrator` on the installation. The guard of ADR 70
    decision 24 counts the break-glass accounts of each namespace. The
    operator keeps its own break-glass accounts on the installation.

### Routes and audit

14. **Routes.** The paths of ADR 210 decision 3 do not change. A namespace
    object is under `/api/dustpan/v1/namespaces/{namespace}/...`, for
    example `.../namespaces/{namespace}/groups/{id}` and
    `.../namespaces/{namespace}/sign-in`. An object of the installation is
    under `/api/dustpan/v1/<collection>`. An operator route answers 404 to a
    caller that has no grant on `@server`, as for a path that does not
    exist (ADR 215 decision 8).
15. **The route test covers every route.** The route test of ADR 17 checks
    each route of the mux, also the routes outside `/api/dustpan/v1`: the
    issuer routes `/oidc/...`, the runner protocol `/runner/v1/...`, `/mcp`,
    the webhook and chat routes, and `/sign-in/{id}/callback`. Each route
    declares its actions and the scope of each object, or is on the public
    list. A route without a declaration fails the test.
16. **Audit entries in the scope of the namespace.** Each sign-in through
    the identity provider of a namespace, each failed sign-in there, each
    use of a support grant (decision 11) and each change of a group, role,
    grant, sign-in setting or key of the namespace writes an entry in the
    audit scope of the namespace. The `@server` scope gets the operator
    actions of decision 4 too, without data of the namespace.

### Roles

17. **Built-in roles.** The namespace role `admin` holds each namespace
    action of decision 3 on its namespace. `installation-operator` holds the
    operator actions of decisions 3 and 4. `site-administrator` holds both,
    and reaches a namespace only when decision 7 lets it.
    `namespace:manage-sign-in` and `namespace:restore-access` belong to the
    namespace `admin` role, because the namespace administrator sets the
    SSO of the namespace (owner). This replaces ADR 70 decision 11 for the
    namespace scope. RFC 76 asks what happens to the role
    `account-administrator`.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- NIST SP 800-53 AC-6 (least privilege) and AC-6(5) (privileged accounts):
  decisions 2, 7 and 11.
- NIST SP 800-53 AC-2(2) (temporary accounts that end after a time) and
  IA-11 (re-authentication): the end time and the fresh MFA step of
  decision 11.
- AWS SaaS Lens, "tenant admin and system admin" roles, as a comparison of
  the two scopes.
- The confused deputy problem, as AWS describes it for cross-account
  access: decisions 8 and 9.

## Not decided here

- What happens to the role `account-administrator` (RFC 76).
- The days of each notice of a suspension (ADR 215, RFC 76).

## Consequences

- `pkg/identity` gets the new `namespace:` and `installation:` constants.
  `pkg/access` puts each one in a family, and the test of decision 5 lands
  with them.
- The policy check of ADR 17 gets cases: a principal of namespace A with
  `admin` on A is refused each action on namespace B and on `@server`. A
  `site-administrator` with `access.installationGrantsReachNamespaces: false`
  is refused each data action in a namespace.
- `pkg/unmanaged` changes the link to a two-sided join (decision 8).
- The route test covers the routes of decision 15.
- ADR 65, ADR 70, ADR 124 and ADR 163 get status lines.
- `docs/reference/` lists each action with its scope.
