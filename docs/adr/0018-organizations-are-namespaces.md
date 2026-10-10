# 18. Organizations are namespaces

Status: accepted · 2026-10-10

## Context

ADR 4 says that a Pulumi Cloud organization is a namespace here. The Pulumi
CLI also reads organizations from the server: the list in `GET /api/user`, the
default organization, and the stacks across all organizations. The server did
not answer these as it would if namespaces were organizations. OpenTofu's
`cloud` block names an organization too (RFC 1).
[RFC 2](../rfc/0002-organizations-are-namespaces.md) holds the options and
the arguments.

The project owner delegated this decision to an agent session on 2026-10-10,
and allowed the reviewed recommendation of the agent to become the decision.
The owner can supersede it with a new ADR.

## Decision

The project accepts RFC 2 as written. In short:

- **One concept.** Every client-facing organization is a namespace with the
  same name. There is no personal organization.
- **No implicit creation, no first namespace in milestone 1.** The server never
  creates a namespace by itself, and no setting names a first namespace. A new
  installation needs `opendustpan namespace create <name>`.
- **Organizations of a principal.** They are the namespaces that the principal
  may read. `GET /api/user` lists them as objects, with `githubLogin` and `name`
  set to the namespace name. A new `namespace.Store` method lists namespaces,
  with conformance tests first. The API filters the list with the
  `Authorizer`. The endpoint declares the `namespace:read` action (ADR 17).
- **Default organization.** It is the only readable namespace if there is
  exactly one. Otherwise the server answers 404, and the CLI chooses.
- **Stack list.** `GET /api/user/stacks` without `organization` lists the
  stacks in all readable namespaces, with `continuationToken`.
- **Error text.** When the CLI fallback names a namespace that does not exist,
  and the name equals the subject of the principal, the error names the fix:
  `pulumi org set-default <namespace>`.

## Consequences

- The Console reads only `githubLogin` and `name` from `/api/user`. It does not
  read `organizations`. The field has always been empty, so no released
  client received an entry. The change from strings to objects breaks no
  released client.
- A fresh installation is not usable until an administrator creates a
  namespace. The milestone 1 walkthrough must show `opendustpan namespace
  create`.
- Listing costs one authorization check for each namespace. This is acceptable
  for tens of namespaces. Memberships may replace it later.
- A listing from a standby region reads replicated data, which may lag (ADR 4).
- Open follow-ups, not decided here:
  - A namespace list endpoint for the Console, so that the Console can stop
    asking users to type the namespace.
  - A configured first namespace, if the manual step is a poor first run.
  - A default namespace for each principal, when users and tokens can carry
    one (ADR 6).
