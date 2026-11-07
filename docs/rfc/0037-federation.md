---
rfc: 37
title: Namespace placement and routing
status: draft
created: 2026-10-10
---

# RFC 37. Namespace placement and routing

Status: draft. Opened on 2026-10-10 as "One view over many OpenDustpan
installs". On 2026-10-13 the project lead asked for a rewrite in place with a
wider question. RFC 37 was not accepted, so an edit is allowed (ADR 15). This
RFC replaces the separate multi-tenancy RFC that RFC 68 names. It decides
nothing.

On 2026-10-18 the owner decided parts of it. See "Outcome": the hosted
service is multitenant with the namespace as the tenant (ADR 215), and
routing between installations is an opt-in capability (RFC 79).

On 2026-11-07 [RFC 126](0126-placement-replication-and-moves.md) took up
the open design points of this RFC with the model of ADR 248: the namespace
row and a static peer list as the placement map, forwarding of the tool APIs
with 503 when the primary installation does not answer, IDs with no
placement hint, and the move of a namespace (design point 6). Read RFC 126
part A for the current proposal. The text below is kept as history.

## Problem

Where does a namespace live, and how does a request reach it?

Four needs ask this question. This RFC looks for one design that covers all
four:

1. **Primary and replica installations.** ADR 248 gives each namespace one
   primary installation. Other installations answer 421 today, with "later: forward the
   request".
2. **Several installations.** The first scope of this RFC: an organization
   runs one installation for each business unit or network zone. The owner
   answered on 2026-10-12: "Yes. We can proxy traffic between instances."
3. **Hosted multi-tenancy.** A customer is a set of namespaces placed on an
   installation. RFC 68 holds the lead recommendation: one installation for
   each customer first.
4. **Forwarding instead of 421.** A client that reaches the wrong place gets
   its answer, not an error.

Facts from the code on `main` (commit `1d4e302`):

- A namespace row names its primary installation. `namespace.ErrNotHome` reports a
  write outside the primary installation. Only the Pulumi adapter maps it to 421
  (`internal/api/pulumi/pulumi.go`). The TFE-compatible adapter and the
  OpenTofu http adapter have no mapping for it.
- Replication between installations is not built (ADR 4, ADR 248).
- Most Pulumi routes name the namespace in the path:
  `/api/stacks/{namespace}/...`. Three do not: `/api/user`,
  `/api/user/stacks` and `/api/capabilities`.
- Many TFE-compatible routes name an ID, not a namespace:
  `/api/v2/workspaces/{id}`, `/api/v2/state-versions/{state_version_id}`,
  and the upload routes `/api/v2/state-uploads/{id}/{token}`. The IDs are
  opaque (`ws-...`, `sv-...`) and say nothing about where the workspace
  lives.
- The OpenTofu http backend names the namespace in the path:
  `/tofu/state/{namespace}/{workspace}`.
- ADR 47 decision 6: "A namespace moves between installations" for failover
  or disaster recovery.
- ADR 48: each instance registers with an advertised address and a lease. The
  calls between instances use the public API with an administrator action.

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. This section decides nothing. An
ADR records a decision (ADR 15).

- Question "Is there a real need, beyond regions in one install?": "Yes. We can
  proxy traffic between instances."

## Owner direction (2026-10-13)

The owner gave these constraints on 2026-10-13. The product manager relayed
them, and the project lead passed them to this RFC. They decide nothing in
this RFC.

1. Routing is in the open-source server, in Go. A Cloudflare Worker or
   another edge proxy is only an optional fast path, never the only path.
   No behavior exists only in the hosted service.
2. Asynchronous geo replication (ADR 4) stays planned for the open-source
   server. With replication, routing sends requests to the promoted primary installation.
   Without it, failover is a restore from a backup, and the docs must state
   the recovery time objective (RTO) and the recovery point objective
   (RPO).
3. Do not block RFC 68. The placement settings are inputs to it.

The owner added a correction on 2026-10-13, relayed the same way:
replication does not replace backups. For compliance they are two different
controls. Every setup keeps backups and tested restores: with or without
replication, hosted or self-hosted. Read point 2 with this correction:
without replication, a restore from a backup is the only way to fail over.
With replication, the backups stay. See "Two controls: replication and
backup".

## Terms in this RFC

- **Placement**: where a namespace lives: its primary installation.
- **Placement map**: the record that answers "where is namespace X".
- **Router**: the code in the server that reads the placement map for a
  request and serves it, forwards it, or refuses it.
- **Forward**: the router sends the request to the primary installation and streams the
  answer back to the client.
- **Front door**: the address that a client uses, for example
  `https://dustpan.example`. A front door can serve namespaces of more than
  one installation.

These words are proposals. The ADR that accepts this RFC adds the words that
stay to `GLOSSARY.md`. "Installation" and "instance" keep their glossary
meanings.

## Design points

### 1. The placement map

Where does the answer to "where is namespace X" live?

- **Option A. The namespace row.** Each installation knows its own
  namespaces and their primary installations. This is the state today, for
  primary installations.
  It cannot answer for a namespace on another installation.
- **Option B. A placement map in installation settings (RFC 68).** A list of
  entries: a namespace name, or a name prefix, and the installation that
  holds it, with its address. It comes from the static layer, so it fits
  the "file wins" rule of RFC 68.
- **Option C. A directory service.** One shared database or service that
  every installation reads. It scales to many namespaces. It is one more
  system to run and to keep available, and every installation depends on
  it.
- **Option D. DNS.** One host name for each namespace or each customer. The
  client chooses the installation by its URL. The server needs no map for
  that case.

**Recommendation: A for the namespace row, B for installations, D for the hosted
service first.** With one installation for each customer, each customer gets
a host name, and no request crosses installations. Option B covers an
organization that runs a few installations behind one front door. Option C
waits until a real count of namespaces needs it.

**Security.** A placement entry decides where a client token goes. An entry
that the API can change can send the tokens of a namespace to a host that an
attacker runs. This is the risk class of the backup target in RFC 68.
Recommendation: the static layer only, or an API that can choose only an
address from an allowlist that the static layer sets.

### 2. How a request reaches its primary installation

- **Option A. Refuse with 421.** The state today, in the Pulumi adapter
  only. The CLIs show an error. The person must find the right URL.
- **Option B. Redirect (307 or 308).** The Pulumi CLI and OpenTofu are Go
  programs. The Go HTTP client may drop the `Authorization` header on a
  redirect to another host, so the request can arrive with no token. A
  redirect also changes the URL that the CLI keeps for later calls in some
  cases. `make e2e` must show what each CLI does.
- **Option C. Forward.** The router streams the request to the primary installation and the
  answer back, with `net/http/httputil.ReverseProxy` from the standard
  library. The client sees one host. The owner answer of 2026-10-12 points
  here.

**Recommendation: option C, with A when the primary installation cannot answer.** When the
primary installation does not answer, the router answers 503 with `Retry-After` and the
name of the primary installation. It never serves a write in a place that is not the primary installation,
because the fence (ADR 4) would refuse it anyway. Every adapter, not only
the Pulumi adapter, maps `ErrNotHome` to the same answer.

Rules for forwarding:

- **Stream, never buffer.** Large bodies (ADR 45, RFC 58) pass through as a
  stream. The forwarded bytes count against the bound on bodies in flight.
- **One hop.** The router adds a header that names itself. A request that
  already carries it is never forwarded again. Two placement maps that
  disagree then give an error, not a loop.
- **Pass the token unchanged.** The router does not sign in the caller. The
  primary installation authenticates and authorizes the request (ADR 17). See design point 3.
- **The client address.** The router passes the client IP in a header. The
  primary installation trusts that header only from an address in its placement map or its
  instance registry. RFC 22 keeps trusted proxies open. This RFC needs the
  same answer.
- **Absolute URLs.** Upload URLs and discovery documents hold the public
  URL (ADR 27). After a forward, they must hold the front door, not the
  primary installation, because the client may not reach the primary installation. Open: the router
  rewrites them, or the primary installation builds them from a forwarded host that it
  trusts.

### 3. Trust between installations

The primary installation must accept the token of the caller.

- **One identity database (open).** ADR 248 decision 4 says a replica
  installation holds an asynchronous copy. ADR 248 decision 13 says the primary
  installation refuses a token of another installation. Whether one token works
  at more than one installation is open. This RFC does not decide it.
- **Several installations.** Each installation keeps its own tokens in its
  own database, so a token works only at the installation that made it.
  Options:
  - A token works only at its own installation. A front door for several
    installations then needs the person to hold one token for each. This is
    simple and safe.
  - The installations trust one OIDC provider (ADR 55, ADR 54), so a person
    signs in to each with the same identity. Each installation still makes
    its own token.
  - A shared identity store. This makes several installations behave like
    one, with the costs of option C of design point 1.

  Recommendation: one token for each installation, with one identity
  provider for sign-in. A later RFC can add more.
- **Calls between installations** (a forward, a federated read): the router
  never adds its own rights. It passes the caller's token. Mutual TLS
  between installations (ADR 54) can prove that the forward came from a
  known router.

### 4. Routes with no namespace in the path

- `/api/user`, `/api/capabilities`: the front door answers them itself.
- `/api/user/stacks`: lists the stacks of the caller across namespaces.
  Behind a front door with several installations, this is a fan-out.
  Options: the front door asks each installation and merges the lists, or it
  lists only the local installation. Recommendation: local only first, with
  a note in the answer when the list is not complete (as ADR 17 says for a
  list that leaves out objects).
- TFE-compatible routes with an ID (`ws-...`, `sv-...`) and the upload
  routes: the router cannot find the namespace from the path. Options:
  - **An ID that holds a placement hint**, for example an installation code
    in the ID. A new ID form must still pass the e2e suite, because the
    OpenTofu `remote` and `cloud` backends keep IDs.
  - **A lookup table** from ID to namespace on the front door.
  - **Host names**: each installation has its own host, and the TFE-compatible
    discovery document (`/.well-known/terraform.json`) gives that host. Then
    an ID never reaches the wrong installation.

  Recommendation: host names first. They need no change to the IDs. Keep
  the placement hint as an open question for a shared front door.

### 5. Installations, replication and backups

#### Two controls: replication and backup

Replication and backup are two different controls. Neither one replaces the
other.

- **Replication** copies every change to the replica installation, also a bad
  state write, a delete or a corruption. It reduces downtime when a primary
  installation fails. It does not recover data that a bad change destroyed, because the
  replica installation holds the same bad change.
- **A backup** is a separate copy of one point in time, which a later bad
  change cannot reach. It recovers data. ADR 30 and ADR 42 test the
  restores.
- Standards treat backup as a control of its own, for example ISO/IEC 27001
  Annex A 8.13 (information backup) and the DORA regulatory technical
  standards (Commission Delegated Regulation (EU) 2024/1774) on backup
  policies and restore tests.

So every setup keeps backups and tested restores: one installation or many, with
or without replication, hosted or self-hosted. The reference architectures
of RFC 68 each show both controls where they apply.

#### With replication

ADR 248 keeps asynchronous replication to replica installations and a manual
failover.

- A promotion moves the primary installation and increments the failover version of
  the namespace. The router reads the new primary installation from the namespace row and
  forwards there. The fence refuses late writes to the old primary installation.
- **Reads at a replica installation.** Options: serve them from the replica (fast,
  but they can be stale by the replication lag), or forward them to the
  primary installation (always current). A tool CLI reads what it just wrote, for example a
  checkpoint and then the stack. Recommendation: forward every request of a
  tool API. Allow replica reads only for the console inventory, with the
  lag shown.
- Backups continue on their schedule. Recover a bad change that replication
  copied to the replica installation from a backup, not from the replica installation.

#### Without replication

- When a primary installation fails, a restore from a backup into
  another installation (ADR 30, ADR 38, ADR 42) is the only way
  to fail over. The placement changes after the restore.

#### Recovery objectives

The docs must state the recovery time objective (RTO) and the recovery point
objective (RPO) for each reference architecture of RFC 68 (owner decision 5
there). This RFC gives no numbers.

- **Failover by promotion (with replication).** The RPO is the replication
  lag at the time of the failure. The RTO is the time to detect the failure,
  promote the replica installation and route to it.
- **Recovery by restore (every setup).** The RPO is the time since the last
  backup that succeeded. A backup schedule is deferred (ADR 30). Until it
  exists, the RPO is whatever the operator schedules outside the server.
  The RTO is the time to start a server, restore the backup, and change the
  placement or DNS. It grows with the size of the backup. A setup with
  replication still needs this path, for a bad change that replication
  copied.
- Proposal: the reference architecture tests in CI measure a restore and
  publish the time, so the docs quote a measured RTO and the backup
  interval as the RPO. A setup with replication also publishes the measured
  replication lag and promotion time.

### 6. Moving a namespace

ADR 47 decision 6 lets a namespace move between installations. A move
changes its placement. Proposed steps, as a first idea:

1. The source refuses new work in the namespace, as maintenance mode does
   for the installation (ADR 38), but for one namespace.
2. The source exports the namespace: rows, blobs and data keys.
3. The target imports it, with the same key-encryption key or a rewrap
   (ADR 23).
4. The placement changes to the target. The source increments the failover
   version of the namespace, so late writes fail at the fence.
5. The router forwards to the target. The source keeps the namespace for a
   retention period, then deletes it.

Open: a backup of one namespace is out of scope in ADR 30. A move needs it.
RFC 66 covers moving units, not namespaces. A move of a namespace keeps its
units, so the questions of RFC 66 (grants, profiles) do not apply inside one
namespace. They still apply to principals and groups of the installation,
which do not move with the namespace (ADR 52, groups are installation-wide).

### 7. Hosted multi-tenancy

A customer is a set of namespaces placed on an installation.

- **Option A. One installation for each customer** (the lead recommendation
  in RFC 68). Placement is DNS: one host name for each customer. No request
  crosses installations. State, secrets and the key-encryption key stay
  apart for each customer. The router of this RFC is not needed for it.
- **Option B. A shared installation with a tenant layer above namespaces.**
  The placement map holds the tenant of each namespace. Namespace names
  must be unique across all customers behind one front door, or the front
  door needs the tenant in the URL. RFC 68 lists what its settings must not
  block for this case.
- **Option C. Shared installations, with customers placed on them.** A
  middle way: a few customers on each installation, and the router of this
  RFC behind one front door. A customer can move to its own installation
  with design point 6.

Recommendation: A first, as RFC 68 says. This RFC keeps B and C possible:
the placement map has a tenant field that is empty now, and the router
already forwards by namespace.

### 8. A read view over many installations

The first idea of this RFC was a read-only inventory across installations.
With a placement map and forwarding, this is a fan-out of inventory reads
(ADR 13) to each installation in the map, with the caller's token on each.
Recommendation: later, after design point 4 decides the fan-out of
`/api/user/stacks`. The same code serves both.

### 9. Where the code goes

A proposal, so that the ports have owners:

- `pkg/placement`: a tool-neutral port. `Lookup(namespace)` answers the primary installation and the address. Adapters: the namespace
  store (the primary installation of each namespace), the placement map from RFC 68 settings
  (installations), and later a directory. A conformance suite in
  `placementtest`.
- The router is middleware in `pkg/dustpan`, before each tool adapter. Each
  adapter gives a resolver from a request to a namespace, as the route table
  already resolves an object for authorization (ADR 17). Adapters never
  call each other (ADR 12). The router is shared core, not an adapter.
- An installation route, for example `GET /api/server/placements/{namespace}`,
  with its own action, answers the placement. An optional edge proxy (owner
  constraint 1) reads this route and caches it. The edge proxy is a fast
  path only. The Go router serves every request that the edge does not.

### 10. Inputs to RFC 68

Owner constraint 3: the placement settings are inputs to RFC 68. These are
proposals for its inventory:

| Setting | Scope | Layer | Note |
| --- | --- | --- | --- |
| Installation name | installation | static | Names this installation in the placement map and in forward headers. |
| Placement map | installation | static, or API with an allowlist from the static layer | See the security note of design point 1. |
| Forwarding on or off | installation | API, live | Off means 421 for every adapter, as today for the Pulumi adapter. A default, not a gate: every build has it. |
| Forward timeout | installation | API, live | How long the router waits for the primary installation before 503. |
| Trusted router addresses | installation | static | Which addresses may set the client IP header. Same class as trusted proxies (RFC 22). |
| Installation name | instance | static (bootstrap) | Already in RFC 68 (ADR 248 decision 5). |
| Advertised address | instance | static | Already in RFC 68 (ADR 48). |

Each row passes the "No setting gates a feature" check of RFC 68: none reads
a license, an edition or a hosted flag, and each exists in every build.

## Open questions

1. Which status does every adapter answer when the primary installation cannot answer: 503
   with `Retry-After`, or 421 as the Pulumi adapter does today?
2. What does each CLI do with a 307 or 308 to another host? (`make e2e`)
3. How do absolute URLs (upload URLs, discovery) hold the front door after a
   forward?
4. Does a shared front door need an installation code in the IDs, or are
   host names enough?
5. Does `/api/user/stacks` fan out across installations, or list only the
   local one?
6. Can the API change the placement map, with an allowlist, or only the
   static layer?
7. What are the measured RTO and RPO of each reference architecture, for
   a restore from a backup and, where replication exists, for a promotion?
8. How does a backup of one namespace work, for a move (ADR 30 keeps it out
   of scope)?
9. Do replica reads serve the console inventory, and how does the console
   show the lag?

## Outcome

Decided in part on 2026-10-18 (owner decisions):

- [ADR 215](../adr/0215-the-namespace-is-the-tenant-of-a-multitenant-hosted-service.md):
  design point 7. OpenDustpan Cloud runs the namespaces of many customers
  on one shared installation, with the namespace as the tenant and no layer
  above it. It answers on one host, with the namespace in the path. Option
  A (one installation for each customer) is not taken. ADR 215 decision 5
  keeps a namespace name unique in the installation.
- Routing, forwarding and replication between installations are allowed as
  an opt-in capability. [RFC 79](0079-opt-in-federation-routing-and-geo-replication.md)
  designs it from design points 1 to 6 of this RFC. ADR 248 drops RFC 79 (below). An earlier draft of
  ADR 221 that said "no routing across installations" is reversed.

Decided on 2026-10-19 (owner decision in ADR 248):

- [ADR 248](../adr/0248-federation-model.md): the server has no regions. Each namespace
  has one primary installation, and replica installations hold asynchronous copies.
  RFC 79 is dropped.

The other design points and open questions stay open.
