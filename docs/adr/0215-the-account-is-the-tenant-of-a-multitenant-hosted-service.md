# 215. The account is the tenant, and the hosted service is multitenant (owner)

Status: accepted · 2026-10-18 · supersedes in part ADR 52, ADR 59 and ADR 204 (see decisions 1, 6 and 10)

## Context

ADR 59 decision 6 says: "Each hosted customer has its own installation and
its own host name". It also says that "account" is only a console label for
a namespace, and that the server has no account object. RFC 68
("Multi-tenancy") and RFC 37 (design point 7) recommend one installation for
each customer first. RFC 33 asks for keys that one customer can revoke.

ADR 52 decisions 1 and 2 make principals and groups objects of the whole
installation. The code has the role `account-administrator` for "the tenant
side of a hosted installation" (`pkg/access/roles.go`), and ADR 70 decision
23 gives that role to the break-glass accounts of a hosted tenant. So the
code already expects a tenant, but no object holds it.

The project owner decided on 2026-10-18, as the project manager relayed:

1. OpenDustpan Cloud does not run one installation for each customer. It is
   multitenant from now on. The operator can split it into shards inside,
   and a tenant does not see a shard.
2. The tenant is the account. The account is an object of its own. It owns
   namespaces, members and groups, sign-in (the tenant administrator sets
   the identity provider), limits, keys and billing.

The owner asked that the design say what changes for self-hosted users and
for the API. The owner pointed to "accounts". The project manager read the
account as a top-level namespace with extra properties. This ADR records
both options and gives the reasons for the choice.

Comparisons, not names that we use: the Pulumi Cloud organization and the
HCP Terraform organization each hold members, teams, SSO, policies and
billing. Each holds its stacks or workspaces directly. OpenDustpan puts
namespaces between the account and the units, because each tool already
calls a namespace its organization (ADR 18).

## Decision

### The hosted service

1. **OpenDustpan Cloud is multitenant (owner).** OpenDustpan Cloud runs
   many accounts on shared installations. It never runs an installation for
   one customer. This replaces the sentence of ADR 59 decision 6: "Each
   hosted customer has its own installation and its own host name".
   ADR 59 decisions 1 to 3 (no feature gap) do not change.
2. **Shards are invisible to tenants (owner).** The operator can run more
   than one installation, each a shard. A shard holds whole accounts. An
   account is on one shard at a time. No API answer, error text, ID form or
   host name tells a tenant which shard holds its account. Only the
   operator moves an account between shards (RFC 76).

### The account

3. **The account is a new object above namespaces.** The containment chain
   becomes installation > account > namespace > collection > unit. Each
   namespace belongs to exactly one account, and never moves to another
   account. An account has:
   - an ID with the prefix `acc-` and 20 characters (ADR 19 scheme). The ID
     never changes, and the server never gives it to a second account.
   - a account name, which a tenant administrator can change.
   - one or more account host names (decision 9).
   - its principals and its groups (decision 6).
   - its sign-in settings, with its identity provider (ADR 216).
   - its settings and limits, as a settings scope between the installation
     and the namespace (decision 12).
   - its account key and, as an option, a customer key (ADR 217).
   - its audit scope (decision 13).
   - a billing reference: one opaque string that only an operator action
     sets (ADR 216). The server never reads it to choose a behavior (ADR 59
     decision 3).
   - a state: `active`, `suspended` or `deleted` (RFC 76 for the rules of
     `suspended`).
4. **Why a new object, not a top-level namespace.** Two options:
   - **Option A, a new object (chosen).** The account holds what a
     namespace cannot hold: the principals and groups that ADR 52 gives
     the whole installation now, the identity provider, the customer key
     and the billing reference.
   - **Option B, a top-level namespace with extra properties (the project
     manager's reading).** One less kind of object. But each tool calls a
     namespace its organization, so a namespace that holds namespaces has
     no meaning in the Pulumi CLI or in OpenTofu. The fence, the backup,
     the issuer and the audit scope of a namespace (ADR 218) would each
     need a rule for a parent namespace. A grant on a parent namespace
     would reach every child, so a namespace administrator could become a
     tenant administrator by mistake.

   Option A keeps one meaning for "namespace" in each tool, and puts
   account rules in one place. The project owner can choose option B with
   a new ADR before the first tag (ADR 73).
5. **The word.** The account is the tenant. Text says "account". It never
   says "tenant" outside of design documents. "Account" no longer means a
   console label for a namespace. Each tool keeps its own word for a
   namespace: "organization".

### Members and groups

6. **Principals and groups belong to an account.** This replaces ADR 52
   decisions 1 and 2: a principal and a group belong to one account, not to
   the whole installation. A person who works in two accounts has two
   principals, one in each account, with separate sign-ins and tokens. A
   group holds principals of its own account only. A grant names a holder
   and an object of the same account. The rest of ADR 52 does not change.
7. **The operator account.** Each installation has exactly one operator
   account. Only principals of the operator account can hold a grant on the
   installation object. On a self-hosted installation, the default account
   (decision 13) is the operator account. On OpenDustpan Cloud, the operator
   account holds the operator's staff and no tenant.
8. **A grant stops at the account boundary.** A grant on the installation
   gives the operator actions of ADR 216, and gives its other actions only
   in the operator account. It never gives an action inside another
   account. So no operator role reads the data of a tenant through
   containment. Access to the data of an account needs a grant that an
   administrator of that account makes (ADR 216).

### Names and hosts

9. **The account comes from the host name.** Each account has one or more
   host names. The server resolves the account of each request from the
   `Host` header before it authenticates the request. A token works only in
   its own account. A request with a token of another account gets 401.
   - On OpenDustpan Cloud, each account has a host name under the domain of
     ADR 77, for example `acme.opendustpan.com`. The operator points the
     host name at the shard that holds the account. So a request reaches
     its shard through DNS, and no server routes a request to another
     installation (ADR 221).
   - The host name of an account never changes once a tenant uses it. A
     cloud trust holds it (decision 10), and so do the CLI login of each
     person and each backend block. A change of the account name keeps the
     host name.
   - RFC 76 decides the form of the host name and custom domains.
10. **The issuer of a namespace on a server with more than one account.**
    ADR 204 decision 1 takes the issuer host from the installation setting
    `issuerBaseURL`, else from `publicURL`. On an installation that holds
    more than one account, the issuer host is an account host name of the
    account, unless the namespace sets `issuerURL`. So
    `https://acme.opendustpan.com/oidc/ns-...` reaches the shard of the
    account. A move of the account to another shard keeps every cloud
    trust. A shared issuer host would need a route from a namespace ID to a
    shard, which decision 2 and ADR 221 do not allow. The rest of ADR 204
    does not change.
11. **A namespace name is unique in its account.** Two accounts can each
    have a namespace `platform`. A name that is unique in the installation
    would tell a tenant that a name exists in another account, which is a
    read across accounts (ADR 220). The server finds a namespace by the
    account of the request and the name. A namespace ID and a unit ID stay
    unique in the installation (ADR 210 decision 3), so `namespaces/-` reads
    work as ADR 210 decision 4 says, inside one account.

### Settings, limits and audit

12. **The account is a settings scope.** The precedence of ADR 63 gets an
    account layer between the installation and the namespace. An account
    value of a limit can only be lower than the installation value. A
    namespace value can only be lower than the account value, as a unit
    switch narrows the namespace switch (ADR 44 decision 6). A value that
    the static layer locks applies to every account (ADR 219).
    - The fair-use storage limit is `limits.storageBytes`, shown as
      "storage limit" (owner). It replaces the name `quota.storageBytes` of
      RFC 68, because the catalog of ADR 115 uses "quota" for another
      concept. RFC 68 "Storage quota" keeps its other rules.
    - The request rate limits of RFC 68 get an account bucket next to the
      token bucket and the namespace bucket (ADR 220).
13. **The account is an audit scope.** The audit log has three scopes: the
    installation (`@server`), an account, and a namespace. Entries of
    principals, groups, roles, grants, sign-in, keys and account settings
    go to the account scope. A tenant administrator reads the account scope
    with `account:read-audit` (ADR 216). The entries of the installation
    scope stay the operator's (ADR 65).

### Self-hosted installations

14. **A self-hosted installation has one default account.** A new
    installation, and an installation that upgrades, has one account: the
    default account. It is the operator account (decision 7). With one
    account, the server answers the default account on every host name, so
    no host name setting is needed.
15. **What changes for a self-hosted user.**
    - Nothing in the Pulumi CLI, OpenTofu and the OpenTofu HTTP backend.
      The tool-compatible APIs keep their paths and shapes (`make e2e`).
    - The principals, groups, roles, sign-in settings and grants of today
      move into the default account. A grant on the installation of today
      still gives the same actions, because decision 8 lets an installation
      grant reach the operator account.
    - The administration actions get the area `account:` (ADR 216). A
      custom role that holds an old name changes in the same migration.
      No tag shipped the old names (ADR 73), so the server keeps no alias.
    - The tool-neutral API gets the account routes of ADR 216. The paths of
      objects that move from the installation to the account do not change
      (ADR 216 decision 5).
    - An operator can make more accounts on a self-hosted installation,
      because OpenDustpan Cloud does (ADR 59 decision 2). Each extra
      account needs its own host name.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- AWS SaaS Lens and the AWS whitepaper "SaaS Tenant Isolation Strategies":
  the silo, pool and bridge models. This ADR uses the pool model for data
  inside a shard, and shards as a bridge that the operator controls. ADR
  220 gives the isolation.
- Google Cloud, "Multi-tenant SaaS on Google Cloud": a tenant identity on
  each request, taken before authentication.
- SOC 2 CC6.1 (logical access to protected information assets) and ISO/IEC
  27001:2022 Annex A 8.3 (information access restriction): decision 8 keeps
  operator roles out of tenant data.

## Not decided here

RFC 76 keeps these open: the form of account host names and custom domains,
the move of an account between shards, the rules of a suspended account,
and whether one person can later use one principal in many accounts.

## Consequences

- `GLOSSARY.md` gets **Account**, **Account ID**, **Account host name**,
  **Default account**, **Operator account**, **Shard** and **Storage
  limit**. The entries **Namespace**, **Installation** and the flagged
  ambiguity "Account" change.
- One migration in each dialect adds the accounts table, the default
  account, and an account ID on each principal, group, grant, role,
  namespace and setting row. ADR 220 says which other rows get it.
- The store finds a namespace by account and name. Each place that reads a
  namespace by name changes. `storetest` gets two accounts with the same
  namespace name.
- The route resolver takes the account from the host before
  authentication. The route test checks that every route under
  `/api/dustpan/v1` and every tool-compatible route reads the account of
  the request.
- ADR 59 and ADR 52 get status lines. The console replaces the label
  "account" for a namespace with the real account (a console change, ADR
  58).
