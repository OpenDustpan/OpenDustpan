# 216. Account administration and operator scope: where each installation action goes (owner)

Status: accepted · 2026-10-18 · supersedes in part ADR 65, ADR 70 (decision 11) and the `installation:` area of ADR 210 decision 7

## Context

ADR 215 makes the account the tenant. The project owner decided on
2026-10-18, as the project manager relayed: on the hosted service, only the
operator administers the installation. Tenant administrators administer
their account. The owner asked that each `installation:` action goes to
account scope or to operator scope.

ADR 210 decision 7 (in review) renames the area `server:` to
`installation:`, with the same verbs. `pkg/access/access.go` gives each
action one family: operator, administration, data, delegation or self.
The role `installation-operator` holds the operator family.
`account-administrator` holds administration, data and delegation, except
the two site-only actions `manage-sign-in` and `restore-access`.
`site-administrator` holds all families.

The actions of the installation today, in the code and in accepted ADRs:
`backup`, `restore`, `maintenance`, `observe`, `read-audit`,
`manage-settings`, `read-settings` (ADR 212, in review), `set-audit-chain`,
`disable-audit-chain`, `manage-audit`, `read-principals`,
`manage-principals`, `manage-groups`, `manage-roles`, `manage-access`,
`read-access`, `create-namespace`, `read-tokens`, `manage-tokens`,
`manage-service-tokens` (deprecated), `manage-token-policy`,
`manage-sign-in`, `restore-access`, `manage-agent`,
`accept-agent-secret-access`, `link-namespaces`, `review-break-glass`.

## Decision

1. **Two scopes (owner).** The operator scope is the installation object
   `@server`. Only principals of the operator account hold actions there
   (ADR 215 decision 7). The account scope is one account object. Its
   administrators are principals of that account. On a self-hosted
   installation, one site administrator holds both scopes, because the
   default account is the operator account. So a self-hosted user sees no
   change in what an administrator can do.
2. **The rule.** An action that changes how the whole installation runs,
   or that reads data of more than one account, has operator scope. An
   action that changes who can do what in one account, or that changes the
   data of one account, has account scope.
3. **The map of each action.** Operator actions keep the area
   `installation:`. Account actions take the area `account:` with the same
   verb, and their object is the account.

   | Action today (ADR 210 name) | Scope | Action after this ADR | Note |
   | --- | --- | --- | --- |
   | `installation:backup` | operator | `installation:backup` | A backup of the whole installation holds every account. |
   | `installation:restore` | operator | `installation:restore` | It replaces every account (ADR 38). |
   | `installation:maintenance` | operator | `installation:maintenance` | It stops writes in every account. |
   | `installation:observe` | operator | `installation:observe` | Metrics of the installation (ADR 32). A tenant reads its usage with `account:read-usage`. |
   | `installation:read-audit` | operator | `installation:read-audit` | The `@server` scope (ADR 65). |
   | `installation:manage-settings`, `installation:read-settings` | both | `installation:...` for the installation scope, `account:manage-settings` and `account:read-settings` for the account scope | One settings resource for each scope (ADR 212 decision 7). |
   | `installation:set-audit-chain`, `installation:disable-audit-chain` | operator | no change | The chain covers the whole log (ADR 64). The second action stays with an administrator of the operator account, so a change still needs two roles. |
   | `installation:manage-audit` | both | `installation:manage-audit` for the installation sinks and checkpoints, `account:manage-audit` for the sinks of one account | A tenant ships its account log and its namespace logs to its own sink. |
   | `installation:read-principals`, `installation:manage-principals` | account | `account:read-principals`, `account:manage-principals` | Principals belong to an account (ADR 215 decision 6). |
   | `installation:manage-groups` | account | `account:manage-groups` | |
   | `installation:manage-roles` | account | `account:manage-roles` | A custom role belongs to one account. |
   | `installation:manage-access`, `installation:read-access` | account | `account:manage-access`, `account:read-access` | Grants on the account object. Grants on the installation stay with `installation:manage-access` in the operator account. |
   | `installation:create-namespace` | account | `account:create-namespace` | |
   | `installation:read-tokens`, `installation:manage-tokens` | account | `account:read-tokens`, `account:manage-tokens` | The tokens of the principals of one account. |
   | `installation:manage-service-tokens` | none | removed | Deprecated in favor of `principal:manage-tokens`. No route declares it. A custom role that holds it loses it in the migration. |
   | `installation:manage-token-policy` | both | `installation:manage-token-policy` sets the bounds, `account:manage-token-policy` sets the policy inside them | An account value can only be stricter than the installation value. |
   | `installation:manage-sign-in` | account | `account:manage-sign-in` | The tenant administrator sets the identity provider (owner). The installation keeps the sign-in of the operator account. |
   | `installation:restore-access` | both | `installation:restore-access` for a restore of the installation, `account:restore-access` for a restore of a namespace in one account | |
   | `installation:manage-agent`, `installation:accept-agent-secret-access` | account | `account:manage-agent`, `account:accept-agent-secret-access` | Each account connects its own model (ADR 129). The installation can allow model hosts in the static layer. |
   | `installation:link-namespaces` | account | `account:link-namespaces` | A namespace link never joins two accounts (ADR 220). |
   | `installation:review-break-glass` | account | `account:review-break-glass` | The operator account reviews its own break-glass uses with the same action on itself. |

4. **New operator actions.** These act on accounts as objects of the
   installation. They have operator scope and the operator family:
   - `installation:create-account`, `installation:read-accounts`,
     `installation:suspend-account`, `installation:delete-account`.
   - `installation:manage-account-limits`: sets the ceiling of each limit
     of one account (ADR 215 decision 12).
   - `installation:set-billing-reference`: sets the billing reference of an
     account (ADR 215 decision 3). The sign-up and billing shims of ADR 59
     call these routes with a service principal of the operator account.

   None of them reads or writes data inside an account. `read-accounts`
   answers the ID, the account name, the host names, the state, the limits
   and the usage counters of each account, never its namespaces, units or
   principals.
5. **New account actions.** `account:read` (the account and its usage),
   `account:update` (the account name), `account:read-usage`,
   `account:read-audit` and `account:manage-key` (ADR 217). They are in the
   administration family, except `account:read`, which is in the data
   family so that each member can read the account it signs in to.
6. **Routes.** The paths that ADR 210 decision 3 gives to "an object of the
   installation" now serve an object of the account of the request (ADR 215
   decision 9), for example `/api/dustpan/v1/groups/{id}`. The paths do not
   change. Three groups of routes are new:
   - `/api/dustpan/v1/account`: `GET`, `PATCH` and the custom methods of
     the account of the request, for example `.../account/key/rewrap`.
   - `/api/dustpan/v1/accounts[/{account_id}]`: the operator routes of
     decision 4.
   - The operator routes of the installation keep their paths
     (`/api/dustpan/v1/backups`, `.../maintenance`, `.../restores`). On an
     installation with more than one account, they answer only on the
     operator host names of the static layer. A tenant host name answers
     404 for them, so a tenant cannot tell that they exist.
7. **Roles.** The role `account-administrator` holds every account action
   of decision 3 and decision 5, and every data and delegation action, on
   its account. Each tenant has it on its account. `installation-operator`
   holds the operator actions of decisions 3 and 4. `site-administrator`
   holds both, and a grant of it on the installation reaches only the
   operator account (ADR 215 decision 8). The two site-only actions of
   `pkg/access/roles.go` stop being site-only in the account scope:
   `account:manage-sign-in` and `account:restore-access` belong to
   `account-administrator`, because the tenant sets its own identity
   provider (owner). This replaces ADR 70 decision 11 for the account
   scope.
8. **Operator access to tenant data.** No operator role holds a data action
   in another account. When support needs to see the data of an account, an
   administrator of that account grants a role to a named principal of the
   operator account. The grant has an end time of at most 72 hours. Each
   action of that principal writes an entry in the audit scope of the
   account, and the console of the account shows the grant while it lasts.
   The recovery of ADR 70 decision 28 makes a temporary administrator in
   the account, not an operator grant. The audit scope of the account
   records each step.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- SOC 2 CC6.1 and CC6.3 (least privilege, and access that the data owner
  approves): decisions 1, 2 and 8.
- ISO/IEC 27001:2022 Annex A 8.2 (privileged access rights) and A 5.15
  (access control): decisions 7 and 8.
- AWS SaaS Lens, "tenant admin and system admin" roles, as a comparison of
  the two scopes.

## Not decided here

- The list of model hosts that the static layer can allow (decision 3,
  `manage-agent`). It follows ADR 129.

## Consequences

- `pkg/identity` gets the `account:` constants and `AccountObject`.
  `pkg/access` puts each new action in a family, and the route test checks
  that each route declares an action of the scope of its object.
- The policy check of ADR 17 gets a case: a principal of account A with
  `account-administrator` on A is refused each action on account B, on
  the installation, and on each object of B.
- The status lines of ADR 65 and ADR 70 point to this ADR. When ADR 210
  merges, its status line gets this ADR too.
- `docs/reference/` lists each action with its scope.
