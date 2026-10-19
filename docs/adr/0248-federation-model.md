# 248. Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover

Status: accepted · 2026-10-19. The project owner decided it. Accepts RFC 88. Supersedes the first two decisions of ADR 4 (the home region and the standby regions) and RFC 79. Amends ADRs 12, 18, 19, 30, 38, 48, 51, 145, 153, 204, 215, 218 and 241.

## Context

ADR 4 gave each namespace a home region, and planned standby regions that
an administrator promotes. RFC 79 added a control home, a namespace
directory, a home lease and a promotion that waits for the lease of the old
home. With these parts, the writes of a namespace depend on a call to
another place. A network partition from the control home then stops the
writes of each namespace on the far side.

On 2026-10-19 the project owner decided the model, as the project manager
relayed:

> We don't have the concept of regions. We have installations which own a
> namespace which contain collections which contain units. Installations
> don't know or care which region they're in at the moment. At best this is
> a placement issue/implementation detail for the hosted version. All that
> a federation does is: 1. Optionally: allow request forwarding between
> federated installations. 2. Allow asynchronous geo-replication between
> federated installations and allow for manual failover. We can not have a
> write quorum. Federated members might be 400ms away from one another and
> this will break e.g. RAFT (or make writes unsustainably slow).

On the same day the owner answered the open questions of RFC 88. RFC 88
lists each ADR decision, RFC section, glossary entry and code path that
this decision changes.

## Decision

1. **Installations own namespaces.** An installation owns its namespaces.
   A namespace holds collections, and a collection holds units (ADR 51).
   Nothing in the domain model sits between an installation and its
   namespaces.
2. **No regions.** The server has no region concept. It does not read,
   store or show a region. Where a hosting provider runs an installation is
   a placement detail of OpenDustpan Cloud, not a field of a namespace. The
   word "region" stays only for a cloud region, for example `--s3-region`
   and the account scopes of a cloud sweep.
3. **The primary installation.** Each namespace has one primary
   installation. It owns the namespace and accepts its writes. The
   namespace row names it. The installation that creates a namespace is its
   primary installation until a promotion. Only the primary installation
   writes the rows and blobs of the namespace. Another installation answers
   `ErrNotHome`.
4. **Replica installations.** A replica installation holds an asynchronous
   copy of a namespace and accepts no writes to it. It answers reads from
   its copy.
5. **The installation name.** The setting `--installation` names an
   installation, with the environment variable `DUSTPAN_INSTALLATION`
   and the config key `installation`. It is a readable name that the
   operator sets, and it is `local` by default. It replaces `--region`,
   `DUSTPAN_REGION` and the config key `region`. The server does not make a
   random installation ID, because a restore into a new database must keep
   the name that the namespace rows hold.
6. **The fence stays.** Each write in a namespace carries a
   `namespace.Fence`, and the store checks it in the same transaction, as
   ADR 4 decides. The failover version goes up by one at each promotion and
   at each restore. A manual promotion needs no change to the fence.
7. **A federation is opt-in and does two things only.** With federation
   off, an installation has none of the behavior of decisions 8 to 13.
   With federation on, it can do one or both of these:
   - **Request forwarding (optional).** A member forwards a request for a
     namespace to its primary installation. It streams the body, makes one
     hop only, and passes the token of the caller unchanged. The primary
     installation checks the token and the authorization again. Members
     trust each other through mutual TLS. Without forwarding, a replica
     installation answers 421 to a write, and each member has its own
     public URL.
   - **Asynchronous replication with manual failover.** The primary
     installation sends each change of a namespace to each replica
     installation of the namespace. Each change carries the failover
     version of its namespace and a sequence number. A replica applies the
     changes in order, and refuses a change with a lower failover version
     than the one it holds.
8. **The unit of failover is the namespace.** A promotion moves one
   namespace. It does not move an installation or a collection.
9. **No write waits for a peer.** A write never calls another
   installation, waits for an acknowledgment, or renews a lease. There is
   no write quorum, no consensus protocol, no home lease, no control home
   and no namespace directory with one writer. A network partition does not
   stop the writes of a primary installation. Replication catches up when
   the link returns.
10. **Promotion is manual.** Only an operator promotes a replica
    installation. The server never promotes on its own.
    - A **planned promotion** first demotes the old primary installation.
      The old primary writes a change that names the new primary and raises
      the failover version, and it stops its writes. The replica applies
      each change up to that one. A planned promotion loses no change.
    - A **forced promotion** does not wait for the old primary. The
      operator gives a flag that says the old primary is not reachable. The
      docs state the replication lag at the time of the promotion as the
      window of lost changes, and tell the operator to stop the old primary,
      or delete it from DNS or the load balancer, first.
    - When the promotion change reaches the old primary, it applies the
      change. Its namespace row then names another installation, so it
      answers `ErrNotHome`, and each write with the old fence returns
      `ErrFenced`.
11. **The divergent tail.** The changes that the old primary wrote after
    its last replicated change and before it applied a forced promotion are
    its divergent tail. The old primary keeps the divergent tail readable
    for an export. The server never merges it back into the new primary. An
    operator then makes the old primary a replica again from a fresh copy.
12. **The signing keys at a forced promotion.** The issuer URL of a
    namespace never changes (ADR 204 decision 1). Each member that holds a
    namespace answers its discovery document and its published key set
    from its copy. Only the primary installation signs. A planned promotion
    keeps the keys. A forced promotion makes the `next` signing key active
    and deletes the old `active` key from the published key set. A verifier
    that reads the key set from the new primary then refuses a token that
    the old primary signed.
13. **The installation scope is local.** A federation replicates
    namespaces only. Each installation keeps its own installation
    principals, groups, roles, settings, `@server` audit scope and sessions.
    The principals, groups, roles and tokens whose principal home is a
    namespace replicate with the namespace (ADR 215). The primary
    installation refuses a forwarded request with a token that another
    installation made for its own installation principal.
14. **Namespace names.** A namespace name is unique on its installation
    (ADR 215 decision 5). No installation asks a peer before it creates a
    namespace. A replica refuses to replicate a namespace whose name it uses
    for another namespace ID, and reports the conflict with both IDs.
    Forwarding finds a namespace by its name, and answers an error when a
    member holds two candidates for one name.
15. **Keys.** Each installation has its own key-encryption key. The
    namespace key record of a replicated namespace holds one wrap for each
    installation that holds the namespace.
16. **Joins between namespaces.** Each namespace writes its own half of a
    join, on its own primary installation, under its own fence. A read
    across a join answers with the time of the data it read, and never
    opens a transaction across two namespaces.
17. **Background work runs on the primary installation.** Durable
    operations, schedules and retention of a namespace run only on its
    primary installation, with the lease of ADR 72 decision 2. That lease
    is local to one database.

## Superseded and amended decisions

| ADR | Change |
| --- | --- |
| 4 | The first two decisions (the home region, the standby regions) superseded by decisions 1 to 5, 7 and 10. The fence decisions stay. |
| 12 | "Home regions and fencing" in the shared core reads "primary installations and fencing". |
| 18 | A listing from a standby region reads as a listing from a replica installation. |
| 19 | The home region that makes the ID of a stack reads as the primary installation. |
| 30 | A restore keeps the primary installation of each namespace, and warns when it is not this installation. The restore into another region is moot. |
| 38 | The home regions of the manifest read as the primary installations. |
| 48 | There is no reconciliation between installations. Each namespace has one writer, and a forced promotion leaves a divergent tail that the server never merges. |
| 51 | A collection has no primary installation of its own. |
| 145 | Each namespace has its own primary installation, so each scope has its own sequence. |
| 153 | An installation that is not the primary installation of the source records no edge. |
| 204 | Decision 4: this ADR deletes the move of a namespace to another region of the same installation. Planned and forced promotions between installations follow decision 12. Decision 1 keeps the one-way door. |
| 215 | Decision 21: routing between installations follows this ADR, not RFC 79. |
| 218 | The row "Region" of the decision 1 table reads "Primary installation". Decision 2: collections share the primary installation of their namespace. Decision 5 reads "the units need another primary installation". |
| 241 | After a failover, `resume` continues on the new primary installation. |

ADR 47 decision 6 (a namespace moves between installations for failover or
disaster recovery) and ADR 64 decision 10 (each audit entry holds the
failover version) fit this decision and stay.

## Consequences

- `GLOSSARY.md` replaces the section "Regions" with "Federation":
  **Installation name**, **Primary installation**, **Replica
  installation**, **Federation**, **Federation member**, **Request
  forwarding**, **Promotion**, **Divergent tail**, **Failover version**
  and **Fence**.
- `pkg/namespace` renames `Region` and `HomeRegion`. The store adapters
  rename the `home_region` column in the collapse before the next tag. No
  tag shipped the schema, so the rename has no compatibility code.
- `--region` becomes `--installation`. The installation API answers
  `primaryInstallation`, and the console changes before the next tag.
- A forced promotion can give two writers until the promotion change
  reaches the old primary. The docs tell the operator to stop the old
  primary first.
- RFC 79 gets the status `dropped`. RFC 87 open questions 6 and 7 are moot.
- Replication, promotion, forwarding and the divergent tail are not built.
  Each one gets its own design issue before the code.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- The multi-cluster replication of Temporal: one active cluster for each
  namespace, a failover version, asynchronous replication and a failover
  that an operator starts.
- PostgreSQL streaming replication: a standby that an operator promotes,
  and a former primary that needs a rewind or a new base backup before it
  follows again.
- OpenID Connect Discovery 1.0 and RFC 7517 (JWK): a key set that a
  verifier reads, and from which the issuer can delete a key.
