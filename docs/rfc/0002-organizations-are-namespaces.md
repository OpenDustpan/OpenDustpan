---
rfc: 2
title: Organizations are namespaces, in every client
status: accepted
adr: 18
created: 2026-10-09
was: proposed under number 10
---

# RFC 2. Organizations are namespaces, in every client

Status: accepted as ADR 18. It was proposed under number 10 until ADR 15 moved every
undecided proposal to `docs/rfc/`.

## Context

ADR 4 says a Pulumi Cloud organization is a namespace here. The e2e suite
(Pulumi CLI v3.200.0, v3.250.0, v3.268.0) shows three places where the CLI
relies on organizations and the server does not answer as one would expect if
namespaces were organizations:

1. `GET /api/user` returns `organizations`. The CLI decodes each entry as an
   object, `{"githubLogin", "name", "avatarUrl", "email"}`, and rejects an
   entry without `githubLogin`. The server declares the field as a list of
   strings. It works only because the list is always empty; listing a single
   namespace would break every CLI command. `whoami --verbose` therefore shows
   no organizations.
2. Default organization. The CLI picks one in this order: `PULUMI_DEFAULT_ORG`
   or `pulumi org set-default` (stored locally per backend URL), then the
   server's `GET /api/user/organizations/default` (`{"GitHubLogin": "..."}`;
   404 means "no opinion"), then the user's own login as an organization name.
   The server answers 404, so `pulumi stack init dev` creates
   `admin/<project>/dev` and fails with "namespace not found".
3. `pulumi stack ls` and `stack ls --all` call `GET /api/user/stacks` without
   `organization`, meaning "every stack I can see". The server requires
   `organization` and answers 400. With `--organization` it works.

OpenTofu's `cloud` block names an organization as well (RFC 1).

## Decision

- Every client-facing organization is a namespace, with the same name. There
  is no second concept, no personal organization and no implicit creation.
- A principal's organizations are the namespaces it may read. The API layer
  computes them by listing namespaces and filtering with the `Authorizer`.
  That needs a new `namespace.Store` method to list namespaces (conformance
  tests first). The `Authorizer` port stays a yes/no check until
  memberships exist and filtering stops scaling.
- `/api/user` lists them as objects: `githubLogin` and `name` set to the
  namespace name, `avatarUrl` empty.
- The default organization is the principal's configured default namespace
  once users and tokens can carry one (ADR 6). Until then, it is the only
  readable namespace if there is exactly one, and otherwise none (404), which
  leaves the choice to the CLI.
- `GET /api/user/stacks` without `organization` lists stacks across the
  principal's readable namespaces, paged with `continuationToken`.
- When a request names a namespace that does not exist and equals the
  principal's subject, the error says so and names the fix: `pulumi org
  set-default <namespace>`.

## Consequences

- Changing `organizations` from strings to objects alters a field the console
  may read. No released client can have seen a string in it, because it has
  always been empty, but the console must be checked and the change lands here
  first (ADR 8).
- Listing across namespaces costs one authorization check per namespace. It is
  fine for tens of namespaces; memberships will replace it.
- Multi-region: a namespace homed elsewhere is still an organization the user
  belongs to. Listing its stacks from a standby region reads replicated data
  (ADR 4), which may lag.

## Outcome

Accepted as ADR 18 on 2026-10-10.

Region terms in this RFC are superseded by [ADR 248](../adr/0248-federation-model.md): the server has no regions, and each namespace has a primary installation and replica installations.
