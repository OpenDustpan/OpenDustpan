---
rfc: 79
title: Opt-in federation of installations, routing and geo-replication
status: dropped
created: 2026-10-18
---

# RFC 79. Opt-in federation of installations, routing and geo-replication

Status: dropped. [ADR 248](../adr/0248-federation-model.md) decides the
federation model. See "Outcome".

## Problem

An earlier draft of ADR 221 said "no routing and no reads across
installations". On 2026-10-18 the project owner reversed that part, as the
project manager relayed:

- Federation and routing between installations are allowed, as an
  optional, opt-in capability, with asynchronous geo-replication.
- The replicas of an installation must scale horizontally.
- Federations between namespaces may be considered. They must also work on
  self-hosted installations, and they must stay minimal. Nothing that they
  hold or synchronize may break when installations replicate
  asynchronously between distant regions, for example `af-south-1` and
  `ap-southeast-4`.

This RFC builds on RFC 37 (placement and routing) and ADR 4 (home regions
and the fence). It asks what a federation replicates, the conflict rules,
how the fence and the failover version work across installations, and
how the OIDC issuer of a namespace (ADR 204) keeps its URL under
replication and moves.

## Terms in this RFC

- **Federation**: a set of installations that agree on one namespace
  directory and one set of public host names. Each installation is a
  **member**.
- **Namespace directory**: the record of each namespace in the federation:
  its ID, its name, its home member and its home region.
- **Control home**: the one member and region that writes the rows of the
  installation scope and the namespace directory.

## 1. The capability is opt-in

- The static setting `federation.enabled` is `false` by default. A server
  with `false` has none of the behavior of this RFC. ADRs 215, 216 and 220
  hold alone.
- It is a setting in every build, never a build flag, and it reads no plan
  or "hosted" flag (ADR 59 decision 3).
- OpenDustpan Cloud starts with one installation (ADR 215 decision 21). It
  turns federation on only when one installation is not enough.

## 2. Routing

- Each member answers each public host name of the federation. It finds the
  namespace of a request in the path (ADR 215 decision 4).
- A route with no namespace in the path, for example a TFE route with a
  `ws-` ID, finds the namespace by the ID. IDs are random and unique (ADR
  19), so the directory can hold a map from a unit ID to its namespace.
  RFC 37 design point 4 gives the options. The home of the namespace writes
  the map row, and it replicates. A member that does not find an ID asks
  each member before it answers 404.
- When the home member of the namespace is another member, the member
  forwards the request there, with the token of the caller. The home member
  checks the token and the authorization again. It never trusts a decision
  of the member that forwarded the request.
- A request with a session (the console) forwards a short-lived assertion,
  signed by the federation CA, that names the principal of the session. The
  home checks the principal again. The sign-in failure budget is kept at
  the home of the principal, and each member forwards a sign-in attempt
  there.
- Members trust each other through mutual TLS with a federation CA. A member
  never forwards to a host that is not in the federation. RFC 37 design
  point 3 gives the options.
- Reads in a standby region: RFC 37 design point 5 recommends a forward of
  each request of a tool API to the home region, because a CLI reads what it
  just wrote. Only console reads of the inventory may use the replica, with
  the lag shown.

**Recommendation:** the router of RFC 37 design point 2, in Go in the
open-source server, behind `federation.enabled`.

## 3. What a federation replicates

Replication is asynchronous (ADR 4). Each row has one writer.

| Data | Writer | Replicated to | Note |
| --- | --- | --- | --- |
| Rows of a namespace (units, history records, grants, principals and groups whose home is the namespace, key records) | the home region of the namespace | each standby region of the namespace | wrapped keys only, never an open key |
| Blobs of a namespace (state, checkpoints, logs, item key blobs) | the home region | each standby region | a blob never changes. A rewrap writes a new item key blob (ADR 217 decision 18). |
| Rows of the installation scope (principals, groups and roles of the installation, installation settings, the namespace directory) | the control home | each member | |
| Audit entries | the home region of the namespace for a namespace scope, the control home for `@server`. Another region forwards its entry to that writer. | each region of its scope | the chain of ADR 64 has one writer for each scope |
| The sign-in failure budget | the home of the principal | not replicated | each member forwards a sign-in attempt to that home (section 2) |
| Sessions, caches, rate buckets | nobody | not replicated | regional state |
| Durable operations and schedules (ADR 72) | the home region of their namespace | not replicated | only the home region runs them |

The server destroys an old namespace key version only after each standby
region reports the new blobs and records.

**Open:** the key-encryption key in each region. Options: the same key in
each region, or one key for each region and one wrap of each namespace key
version for each key. A customer key in one KMS region adds the latency of
that region to each refresh (ADR 217 decision 12). AWS KMS multi-Region
keys and Google Cloud KMS multi-region locations can serve each region.
**Recommendation:** one wrap for each region key in the namespace key
record, and the docs tell a customer to use a multi-Region key.

## 4. Conflict rules

1. **One writer for each row.** A row of a namespace is written only in the
   home region of the namespace. A row of the installation scope is written
   only in the control home. There is no multi-writer replication and no
   last-writer-wins rule. An operator action on a namespace row, and each
   key state write of ADR 217 decision 14, are forwarded to the home of the
   namespace. A standby keeps the key state in memory only.
2. **Order and the fence.** Each replicated change carries the failover
   version of its namespace and a sequence number. A replica applies the
   changes in order. It refuses a change with a lower failover version than
   the one it holds. A promotion increments the failover version (ADR 4), so
   a late change of the old home is refused in each region.
3. **IDs never collide.** IDs are random (ADR 19), so two regions never make
   the same ID.
4. **Names that must be unique.** A namespace name is unique in the
   federation, because it is the first path segment of the console (ADR 215
   decision 5). A new namespace takes its name in the directory at the
   control home, in one synchronous call. A creation fails when the control
   home cannot answer. Namespace creation is rare. The other calls across regions that must get an answer are the home lease of section 5 and the check of rule 5. A claim ends after 10 minutes unless
   the namespace row replicates. A unit name is unique in its namespace,
   and only the home region writes it.
5. **Revocation lag.** A revocation of a row of a namespace is current at
   the home of the namespace. A revocation of a row of the installation
   scope (a principal, token or grant of the installation) reaches each
   member after the replication lag, also the home of a namespace. So a
   principal of the installation that acts inside a namespace is checked at
   the control home, in one synchronous call cached for at most 30 seconds,
   or the request is refused when the control home cannot answer. An end
   time is evaluated locally and has no lag.

## 5. The fence and the failover version

- ADR 4 applies as written. Each namespace has one home region. Each write
  carries a `namespace.Fence`, and the store checks it in the same
  transaction.
- The home member and the home region are in the namespace row, so a
  promotion is one fenced write, and it replicates as one change.
- **A promotion while the old home cannot answer.** The fence is local. A
  home holds a home lease, renewed at the control home every
  `federation.homeLeaseSeconds` (30 by default). A home refuses writes and
  signatures when its last renewal is older than the lease. A promotion of
  a standby waits until the lease of the old home has ended. The data that
  the old home wrote after its last replicated change is lost, and the docs
  state that window as the replication lag at the promotion. Option: a
  promotion only after an operator confirms that the old home is stopped.
- The control home has its own failover version, and its promotion follows
  the same rule.
- **A move between members is a promotion.** When the namespace already
  replicates to the target member, a move is a promotion of the target. It
  needs no export and no import. Without replication, a move is not
  possible yet. The namespace backup of ADR 218 restores only into its own
  installation. RFC 66 asks how units move.

## 6. The OIDC issuer under replication and moves

The issuer URL never changes (ADR 204 decision 1, one-way door).

- Each member has the same `issuerBaseURL`, a host name of the federation.
  So the issuer of a namespace is the same URL on each member and in each
  region.
- Any member answers the discovery document and the JWKS from its replica.
  The JWKS holds the `next`, `active` and `retired` keys (ADR 204 decision
  3). A replica that lags behind one rotation still answers the key that
  signs.
- Only the home region signs a token, after it reads the fence of the
  namespace. A former home signs nothing after a promotion.
- A rotation publishes the `next` key before it uses it. The time between
  the two steps must be at least the replication lag plus the cache time of
  the JWKS (300 seconds). The home uses the `next` key only after each
  member has acknowledged it, and not before 24 hours. Between `af-south-1` and `ap-southeast-4`, the
  docs give the measured lag.
- A move keeps the issuer keys. ADR 204 decision 4 has two cases: a restore
  into the same installation keeps the keys, and a copy does not. A move in
  a federation is a third case that keeps the keys. With the home lease,
  two members never sign with one key at the same time.

## 7. Horizontal scale

- Each member runs many instances on one database (ADR 72).
- Each standby region runs many instances that read its regional replica,
  and forward writes to the home region.
- No part of a federation needs one process. A schedule runs in the home
  region of its namespace, with the lease of ADR 72 decision 2.

## 8. Joins between namespaces

ADR 216 decision 8 makes a join across namespaces (a namespace link, an
output-sharing rule, a dependency edge) need an acceptance from each
namespace. In a federation the two namespaces can have different home
regions.

**Proposal: two halves, one in each namespace.**

- Each namespace writes its own half of the join: its acceptance, in its
  own rows, in its own home region, under its own fence. No row of a join
  belongs to two namespaces, and no row has two homes.
- A join is active in a region when that region holds both halves.
- A read across a join (the units for a classification, the outputs of a
  unit) is forwarded to the home of the other namespace, or uses a replica
  only in a region that the other namespace lists as its standby. The
  answer holds the time of that data. It never opens a
  transaction across two namespaces.
- When the data of the other namespace is not in the region yet, the read
  answers that the join is not available, never an error of the reader.
- Either namespace ends the join with a delete of its half. The join is
  inactive in each region when the delete arrives there.
- On one installation with no federation, the replication lag is zero, so
  the same rules hold with no extra code.

So a join holds no row that needs a synchronous write between regions.

## 9. A federation of self-hosted installations

The design is the same for self-hosted installations. An operator can join
two installations in a federation, for example one in each region of the
company. Nothing in this RFC depends on OpenDustpan Cloud.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- The multi-cluster replication of Temporal, which ADR 4 follows: one
  active cluster for each namespace, a failover version, and asynchronous
  replication.
- AWS KMS multi-Region keys and Google Cloud KMS multi-region key locations
  (section 3).
- OpenID Connect Discovery 1.0 and RFC 7517 (JWK): the JWKS that holds the
  next key before its use (section 6).

## Open questions

1. Does the owner accept the router of section 2, with mutual TLS between
   members?
2. The key-encryption key in each region (section 3).
3. Is the synchronous name claim at the control home (section 4, rule 4)
   acceptable?
4. Do joins between namespaces in different regions (section 8) ship with
   the first federation, or later?
5. What replication lag does OpenDustpan Cloud measure and publish before
   it turns federation on?
6. A home refuses writes when it cannot renew its lease at the control home (section 5). So a partition from the control home stops the writes of each namespace in the cut-off regions. Is that acceptable, or does each region need its own lease authority? Recommendation: accept it for the first federation, and publish the lease time with the lag.


## Outcome

Dropped on 2026-10-19. The project owner decided the federation model in
[RFC 88](0088-federation-model.md), and
[ADR 248](../adr/0248-federation-model.md) records it. There are no
regions, no control home, no namespace directory with one writer, no home
lease and no automatic promotion, and a partition does not stop writes.
Section 8 (joins between namespaces) moves into the "Not decided" section of ADR 248, with
"installation" for "region". Open questions 3 and 6 are moot.

The open design items of ADR 248 that came from this RFC (forwarding, the
replication mechanics, keys, the discovery document and joins) have a
proposal in [RFC 126](0126-placement-replication-and-moves.md).
