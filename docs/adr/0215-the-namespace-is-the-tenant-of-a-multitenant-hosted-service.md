# 215. The namespace is the tenant, and OpenDustpan Cloud is multitenant (owner)

Status: accepted · 2026-10-18 · supersedes in part ADR 52 (decisions 1 and 2), ADR 59 (decision 6) and ADR 67 (decision 7)

## Context

ADR 59 decision 6 says: "Each hosted customer has its own installation and
its own host name". It also says that "account" is only a label of the
hosted console for a namespace, and that the server has no account object.
RFC 68 ("Multi-tenancy") gives two options: one installation for each
customer, or a tenant layer above namespaces. RFC 37 design point 7 gives
the same two options and a third one, with a router.

ADR 52 decisions 1 and 2 make principals and groups objects of the whole
installation. ADR 67 decision 7 is the one exception: a service principal
can belong to one namespace. `GLOSSARY.md` calls the namespace "the unit of
tenancy" already.

The project owner decided on 2026-10-18, as the project manager relayed:

1. OpenDustpan Cloud is multitenant. It does not run one installation for
   each customer.
2. "Account is just what namespaces are called in hosted." The tenant is
   the namespace. The server gets no account object.
3. The hosted URL is `app.opendustpan.com/<account>`: one host, with the
   namespace in the path.
4. On the hosted service, each namespace has its own principals. The
   namespace owns its members, its SSO and the deprovisioning of its
   members. The namespace administrator sets the SSO.
5. A suspended namespace is read-only for 30 days. It is disabled after 31
   days and deleted after 90 days, each step after a notice. Work in
   progress runs to its end, and no new work starts.
6. Billing is not decided. The server gets no billing object and no usage
   counter for billing.

An earlier draft of this ADR put an account object above namespaces. The
owner did not take it. This ADR replaces that draft under the same number.

Comparisons, not names that we use: the Pulumi Cloud organization and the
HCP Terraform organization each hold members, teams, SSO and policies. A
namespace is what each tool calls its organization (ADR 18), so the
namespace is the natural tenant.

## Decision

### The tenant

1. **OpenDustpan Cloud is multitenant (owner).** OpenDustpan Cloud runs
   many namespaces of many customers on one shared installation. It never
   runs an installation for one customer. This replaces this sentence of
   ADR 59 decision 6: "Each hosted customer has its own installation and
   its own host name". ADR 59 decisions 1 to 3 (no feature gap) do not
   change.
2. **The namespace is the tenant (owner).** The server has no object above
   the namespace other than the installation. The containment chain stays
   installation > namespace > collection > unit. There is no account
   object, no account ID, no `account:` action area and no settings layer
   between the installation and the namespace.
3. **The word "account" (owner).** "Account" is the label of the hosted
   console for a namespace, as ADR 59 decision 6 says. The API, the CLI
   output and each error text say "namespace". The Pulumi CLI and the
   TFE-compatible API keep their own word, "organization". Design documents
   say "tenant" only to name the role of the namespace.

### The hosted URL

4. **One host, with the namespace in the path (owner).** OpenDustpan Cloud
   answers on one host name, `app.opendustpan.com`. The console page of a
   namespace is `https://app.opendustpan.com/<namespace name>`. The
   server finds the namespace of each request in the path, as it does now:
   - The Pulumi Cloud-compatible API has the organization in each stack
     path.
   - The TFE-compatible API has the organization in its paths, or an ID
     that names one workspace, run or state version.
   - The OpenTofu HTTP backend has `/tofu/state/{namespace}/{workspace}`.
   - The tool-neutral API has `/api/dustpan/v1/namespaces/{namespace}/...`
     (ADR 210 decision 3).

   So the request path needs no change, and the server reads no `Host`
   header to find a namespace.
5. **A namespace name is unique in the installation.** ADR 59 decision 6
   keeps this rule. On one host, the namespace name is the first path
   segment of the console, so two customers cannot use one name. A taken
   name tells a new customer that the name exists, as an organization name
   does on other hosted products.
6. **Reserved names.** The server refuses a namespace name that is equal to
   a first path segment that the server or the console serves, for example
   `api`, `oidc`, `tofu`, `runner`, `sign-in`, `mcp`, `metrics`, `chat`,
   `.well-known`, `health` (the probe paths `/health/live` and
   `/health/ready`) and `webhooks` (the prefix of each route that receives
   a webhook). The list is a constant in `pkg/namespace`. The route test
   fails when a new first path segment is not in the list. The console
   repository adds its own first path segments to the list.
7. **The issuer does not change.** ADR 204 decision 1 applies as written.
   The issuer of a namespace is `https://<issuer host>/oidc/<namespace ID>`.
   On OpenDustpan Cloud the issuer host is one host of the installation, so
   no namespace needs its own host name for its cloud trusts.
8. **Unknown paths.** A path that names a namespace that does not exist, or
   that the caller cannot read, answers 404. The answer holds no data of a
   namespace: no name, no ID and no count. ADR 166 decision 14 gives the
   rule.

### Principals of a namespace

9. **Each principal and each group has a home (owner).** The home of a
   principal or a group is the installation or one namespace. This extends
   ADR 67 decision 7 from service principals to persons, workloads and
   groups:
   - A principal of a namespace holds grants only on its namespace or on
     objects inside it. The server refuses each other grant.
   - A group of a namespace holds principals of that namespace only. A
     principal of a namespace can be a member of a group of its namespace.
     This replaces the sentence of ADR 67 decision 7 that a principal of a
     namespace cannot be a member of a group.
   - A custom role of a namespace can be used only in grants on that
     namespace and on objects inside it.
   - A principal of the installation, a group of the installation and a
     custom role of the installation keep ADR 52 decisions 1 and 2.
10. **A setting chooses the home of new principals, never a build flag.**
    The installation setting `identity.principalHome` has two values:
    - `installation` (the default): ADR 52 decisions 1 and 2 apply as
      today. Sign-in is set for the installation. A namespace has only the
      service principals of ADR 67 decision 7.
    - `namespace`: each namespace administrator sets the sign-in of the
      namespace, with its identity provider (ADR 216). A person who signs
      in through it becomes a principal of that namespace. Groups and
      custom roles that a namespace administrator makes belong to the
      namespace. The sign-in of the installation stays, for the principals
      of the installation only.

    OpenDustpan Cloud locks the value `namespace` in the static layer (ADR
    63). The setting exists in every build, and it reads no plan, edition
    or "hosted" flag (ADR 59 decision 3, RFC 68 "No setting gates a
    feature"). One installation holds both kinds of principals at the same
    time: with `namespace`, the operator's staff are principals of the
    installation, and each customer's people are principals of its
    namespaces.
11. **A change of the setting converts nothing.** A change from
    `installation` to `namespace` keeps each existing principal and group in
    the installation. It makes no copy and no move. RFC 76 asks whether a
    move of one principal into a namespace is needed. No tag shipped a
    principal row, so the server keeps no conversion code (ADR 73).
    - A change from `namespace` back to `installation` keeps each
      principal, group, role and sign-in of each namespace. Only the sign-in
      of new persons changes: a new person becomes a principal of the
      installation.
    - A self-hosted operator who then sets
      `access.installationGrantsReachNamespaces: false` ends the access
      of each person of the installation in a namespace, except a support
      grant of decision 11 of ADR 216 (RFC 76 section 2).
12. **One person in two namespaces has two principals (owner).** Each
    principal is in one home. A person who works in two namespaces of
    OpenDustpan Cloud has two principals, two sign-ins and two sets of
    tokens. The server never links two principals on a matching email
    address or a matching subject (ADR 216 decision 9).
13. **The namespace owns deprovisioning (owner).** The namespace
    administrator ends the membership of each member, and the identity
    provider of the namespace can end them (ADR 70 decisions 19 and 20).
    A purge of the namespace deletes its principals, groups and custom
    roles. A delete of the namespace revokes their tokens and disables
    them, as ADR 67 decision 7 does for service principals.

### The CLI consequence

14. **One credential for each host.** The Pulumi CLI keeps one access token
    for each backend URL. OpenTofu keeps one token for each host name. On
    one host, a person with two principals cannot hold both tokens in one
    login. The Pulumi CLI and OpenTofu do not change (`CLAUDE.md`).
15. **The smallest fix: a token for each namespace in the environment.**
    The server changes nothing in the tool-compatible APIs. The docs of
    OpenDustpan Cloud tell a person in two namespaces to do one of these:
    - Run `pulumi login` or `tofu login` again when the person changes
      namespace.
    - Set the token of each namespace in the shell of each project, for
      example with direnv: `PULUMI_ACCESS_TOKEN` for the Pulumi CLI, and
      `TF_TOKEN_app_opendustpan_com` for OpenTofu. Each tool reads its
      variable before its stored login.

    When a token of one namespace reaches a path of another namespace, the
    answer is the 404 of decision 8. The 404 of the tool-neutral API also
    names the namespace of the token and the login command, and never
    names the namespace of the path. RFC 76 asks whether a path prefix for
    each namespace in the backend URL works with each real CLI. `make e2e`
    must show it before the server serves one.

### A suspended namespace

16. **Four states (owner).** A namespace has one of these states:

    | State | From | What works | What the server refuses |
    | --- | --- | --- | --- |
    | `active` | creation, or a resume | everything | nothing |
    | `suspended` | an operator action (ADR 216) | sign-in, reads, exports, the end of work in progress, and deprovisioning | the start of new work and each other write |
    | `disabled` | 31 days after the suspension | sign-in of the namespace administrators, the read of the namespace state and of its notices, and deprovisioning | each other request |
    | `deleted` | 90 days after the suspension | nothing. The purge of ADR 47 runs. | each request |

    - **Work in progress runs to its end (owner).** A Pulumi update with a
      lease, a held OpenTofu lock and a running job phase in a `suspended`
      namespace can write until they end. In a `disabled` namespace, the
      server refuses them, but no work of a suspended namespace is still
      running by then in the usual case. RFC 68 "A limit never stops work in
      progress" gives the rule. Work in progress is a lease, lock or job
      phase that existed at the suspension. It writes for at most
      `namespaces.suspension.inFlightHours` (24 by default).
    - **Deprovisioning always works.** Deprovisioning is the removal of a
      member, the revocation of a token, a session or a grant, and the
      deprovisioning calls of the identity provider. No state except
      `deleted` refuses a write that ends access.
    - **Notices (owner).** The server sends a notification (ADR 145) to the
      administrators of the namespace at the suspension, 7 days before the
      `disabled` state, and 7 days and 1 day before the delete. Each notice
      names the date of the next state and the operator contact. RFC 76
      asks the owner for the days of each notice.
    - **The periods are installation settings.** `namespaces.suspension.disableAfterDays`
      is 31 by default, and `namespaces.suspension.deleteAfterDays` is 90 by
      default. A self-hosted operator can change them.
    - **A resume.** The operator sets the state back to `active` at any
      time before the delete (ADR 216). The server writes an audit entry in
      the namespace scope.
    - **This ADR does not say why the operator suspends a namespace.**
      Billing is not decided (decision 18).
17. **The state is in the fenced row.** Each write transaction already
    reads the namespace row for its fence (ADR 4). The same read takes the
    state, so a write in a `suspended` namespace fails in that transaction.
    The error code is `namespace.suspended` or `namespace.disabled` (ADR
    182). The message says that the operator suspended the namespace, and
    names the date of the next state.

### Billing

18. **Not decided (owner).** The server has no billing object, no billing
    reference and no usage counter for billing. The fair-use limits of RFC
    68 (for example the storage limit of ADR 220) are not billing. A later
    ADR decides billing.

### Self-hosted installations

19. **What changes for a self-hosted user.** With the default
    `identity.principalHome: installation`, nothing changes:
    - The Pulumi CLI, OpenTofu and the OpenTofu HTTP backend keep their
      paths and shapes (`make e2e`).
    - Principals, groups, custom roles and sign-in stay objects of the
      installation, as ADR 52 decisions 1 and 2 say.
    - A grant on the installation still reaches every namespace, because
      `access.installationGrantsReachNamespaces` is true by default (ADR
      216 decision 7).
    - The new parts are the namespace states of decision 16, the reserved
      names of decision 6, and the namespace actions of ADR 216.
20. **A self-hosted installation can work as the hosted one.** An operator
    can set `identity.principalHome: namespace` and
    `access.installationGrantsReachNamespaces: false`. The installation then
    works as OpenDustpan Cloud does (ADR 59 decision 2). Each namespace
    administrator sets the sign-in of the namespace.

### Federation and other installations

21. **One installation that scales out.** OpenDustpan Cloud is one
    installation with many instances on one database (ADR 72). More than one
    installation behind `app.opendustpan.com` needs a route from a
    namespace to its installation. The owner allows routing between
    installations as an opt-in capability. RFC 79 designs it, and nothing
    in this ADR depends on it.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- AWS whitepaper "SaaS Tenant Isolation Strategies" and the AWS SaaS Lens:
  the pool model, with a tenant context on each request. Here the tenant
  context is the namespace in the path.
- The Pulumi Cloud organization and the HCP Terraform organization: one
  object that holds members, teams, SSO and policies, with the organization
  name in the URL path.
- NIST SP 800-53 AC-2 (account management): decision 13 gives the
  namespace the deprovisioning of its members.

## Not decided here

RFC 76 keeps these open: a move of one principal of the installation into a
namespace, a path prefix for each namespace in the CLI backend URL, the days
of each notice, and the parts of a namespace backup that ADR 218 leaves
open. Billing is not decided (decision 18).

## Consequences

- `GLOSSARY.md`: the entries **Namespace**, **Principal**, **Principal of a
  namespace** and **Group** change. **Principal home** and **Namespace
  state** are new. The flagged ambiguity **Account** keeps its text.
- `pkg/identity` gets the home of a principal and of a group. The policy
  check refuses a grant across homes, and `accesstest` gets the cases.
- `pkg/namespace` gets the states, the reserved names and the
  `namespace.suspended` and `namespace.disabled` error codes (ADR 182).
- A schema change in each dialect adds the home to the principal, group and
  role rows, and the state to the namespace row. No tag shipped these
  tables, so the change follows ADR 73.
- The route test fails for a first path segment that is not in the
  reserved list (decision 6).
- ADR 52, ADR 59 and ADR 67 get status lines.
