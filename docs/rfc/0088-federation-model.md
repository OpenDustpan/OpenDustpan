---
rfc: 88
title: "Federation model: installations, request forwarding, asynchronous replication, manual failover"
status: draft
created: 2026-10-19
---

# RFC 88. Federation model: installations, request forwarding, asynchronous replication, manual failover

Status: draft. It records an owner decision and proposes the ADR that
supersedes the parts of the accepted ADRs that assume regions. It decides
nothing else.

## Problem

ADR 4, RFC 37 and RFC 79 model a namespace that is authoritative in one
region. RFC 79 then adds a control home, a namespace directory, a home lease
and a promotion that waits for the lease of the old home. These parts make
the writes of a namespace depend on a call to another place. RFC 79 open
question 6 shows the cost: a network partition from the control home stops
the writes of each namespace on the far side.

On 2026-10-19 the project owner decided the model, as the project manager
relayed. The text below uses "installation" where the first message said
"instance", as the owner corrected it on the same day. An installation can
run many server instances (ADR 72). That is separate from federation.

> We don't have the concept of regions. We have installations which own a
> namespace which contain collections which contain units. Installations
> don't know or care which region they're in at the moment. At best this is
> a placement issue/implementation detail for the hosted version. All that
> a federation does is: 1. Optionally: allow request forwarding between
> federated installations. 2. Allow asynchronous geo-replication between
> federated installations and allow for manual failover. We can not have a
> write quorum. Federated members might be 400ms away from one another and
> this will break e.g. RAFT (or make writes unsustainably slow).

The owner accepted these consequences:

- No home lease, and no control home.
- No automatic promotion. Each failover is an operator action.
- A network partition does not stop writes.
- No consensus protocol and no write quorum.

This RFC lists each ADR decision, RFC section, glossary entry and code path
that assumes the old model, with a proposed change. It then proposes the
text of the superseding ADR.

## The model

1. **The hierarchy.** An installation owns namespaces. A namespace holds
   collections. A collection holds units (ADR 51). Nothing in the domain
   model sits between an installation and its namespaces.
2. **No regions in the domain.** A region is where a hosting provider runs
   an installation. The server does not read, store or show it. OpenDustpan
   Cloud can choose where it runs each installation. That choice is a
   placement detail of the hosted service, not a field of a namespace.
3. **One home installation for each namespace.** Each namespace has one
   installation that accepts its writes: its home installation. The
   namespace row names it. The installation that creates a namespace is its
   home until a promotion.
4. **A federation is optional, and it adds two things only.**
   - **Request forwarding (optional).** A member forwards a request for a
     namespace to the home installation of that namespace.
   - **Asynchronous replication and manual failover.** A home installation
     sends the changes of a namespace to each standby installation of that
     namespace. An operator promotes a standby installation by hand.
5. **No write waits for another installation.** A write commits in one
   transaction on the home installation, with the fence (ADR 4). No write
   calls a peer, waits for an acknowledgment, or renews a lease.

## What stays

### The fence and the failover version

The failover-version fence of ADR 4 fits the owner decision, and it stays.
The code shows that it is local and needs no lease:

- `namespace.Fence` holds the namespace name and its failover version
  (`pkg/namespace/namespace.go`).
- Each store checks the fence in the transaction of the write and returns
  `namespace.ErrFenced` when the version changed. PostgreSQL reads the
  namespace row `FOR SHARE`, and SQLite uses `BEGIN IMMEDIATE`.
- A restore already raises the failover version of every namespace, so the
  leases and locks from before the restore stop working
  (`pkg/store/sqlite/queries.sql`, `pkg/store/postgres/restore.go`, ADR 38
  decision 7).
- The conformance suites test a stale fence in each port that writes in a
  namespace (`BumpFence` in `jobtest`, `runnertest`, `gatetest`,
  `notifytest`, `chatopstest`, `catalogtest`).
- Nothing in `pkg/`, `internal/` or `cmd/` implements a home lease, a
  control home, a namespace directory, a quorum or a promotion. Replication
  is not built.

A manual promotion fits this mechanism with no change to the fence:

- The standby installation writes the promotion in one local transaction.
  The namespace row then names the standby as the home installation, and
  the failover version goes up by one.
- Each replicated change carries the failover version of its namespace. A
  replica refuses a change with a lower failover version than the one it
  holds. So the new home refuses each late change of the old home.
- When the promotion change reaches the old home, the old home applies it.
  Its namespace row then names another installation, so `WritableHere`
  returns `ErrNotHome`, and each write with the old fence returns
  `ErrFenced`. The old home stops its writes to that namespace without a
  lease.

### Other parts that stay

- ADR 47 decision 6: a namespace moves between installations for failover
  or disaster recovery. A promotion is that move.
- ADR 64 decision 10: each audit entry of a namespace holds its failover
  version, so a verifier sees where a forced promotion forked the chain.
- ADR 72: an installation runs many instances on one database. Federation
  does not change it.
- RFC 37 design point 5: replication and backup are two controls. A setup
  with replication still keeps backups and tested restores.
- RFC 79 section 8: a join between two namespaces has two halves, each
  written only by the home of its namespace. It needs no synchronous write
  between installations, so it fits the owner decision with "region"
  replaced by "installation".

## What goes

These parts of RFC 79 and RFC 87 contradict the owner decision. The
superseding ADR does not keep them.

- The control home, and the rows of the installation scope that it writes
  for each member (RFC 79 sections 3 and 4).
- The namespace directory as a federation-wide record with one writer
  (RFC 79 terms and section 3).
- The synchronous name claim at the control home (RFC 79 section 4 rule 4).
- The synchronous check of an installation principal at the control home
  (RFC 79 section 4 rule 5).
- The home lease, `federation.homeLeaseSeconds`, and the promotion that
  waits for the lease (RFC 79 section 5).
- The control home failover version (RFC 79 section 5).
- The issuer rule "with the home lease, two members never sign with one
  key" (RFC 79 section 6).
- A standby region inside one installation (ADR 4, RFC 37 design point 1
  option A, ADR 204 decision 4).

## Consequences of the decision

### A partition does not stop writes

A home installation accepts the writes of its namespaces whatever the state
of its links to peers. Replication catches up when the link returns. A
standby installation answers reads from its replica, and forwards writes
when forwarding is on.

### A forced promotion can give two writers

With no lease, an operator can promote a standby while the old home still
runs on the far side of a partition. Both installations then accept writes
for the namespace until the promotion change reaches the old home. The
owner accepted this. The proposal limits it:

- **Planned promotion.** The old home answers. The operator first demotes
  it: the old home writes a change that names the new home and raises the
  failover version, and it stops its writes. The standby applies each
  change up to that one and becomes the home. The promotion loses no change.
- **Forced promotion.** The old home does not answer. The operator promotes
  the standby with a flag that says the old home is not reachable. The
  changes that the old home wrote after its last replicated change do not
  reach the new home. The docs state this window as the replication lag at
  the time of the promotion. The docs tell the operator to stop the old
  home, or to delete it from DNS or the load balancer, before a forced
  promotion.
- **The divergent tail.** When the old home comes back, it applies the
  promotion change and stops its writes. The changes that it wrote after
  its last replicated change are its divergent tail. The server never
  merges them. Proposal: the old home keeps the tail readable for an
  export, and an operator then makes the old home a standby again from a
  fresh copy. Open question 4.

### Installation scope is local

A federation replicates namespaces only. Each installation keeps its own
installation scope: its installation principals, groups and roles, its
installation settings, its `@server` audit scope and its sessions. No row
of the installation scope has a writer on another installation. So no
installation needs a control home.

- The principals, groups, roles and tokens whose principal home is a
  namespace replicate with the namespace (ADR 215). A token of such a
  principal works on each installation that holds the namespace.
- A token of an installation principal works only on its own installation.
  The home refuses a forwarded request with such a token, unless the home
  installation made the token. RFC 37 design point 3 recommends one
  identity provider for each installation. Open question 3.

### Namespace names

No installation asks a peer before it creates a namespace. A namespace name
is unique on its installation (ADR 215 decision 5). Two members can each
create a namespace of the same name. The proposal:

- A standby installation refuses to replicate a namespace whose name it
  already uses with another namespace ID. It reports the conflict in the
  replication status, with both IDs.
- Forwarding finds a namespace by its name. A member that holds two
  candidates for one name answers an error, and never guesses.
- OpenDustpan Cloud avoids the conflict with its sign-up shim, which
  chooses one installation for each new namespace. That is a placement
  detail of the hosted service.

### The OIDC issuer

The issuer URL of a namespace never changes (ADR 204 decision 1). Without a
lease, two installations can sign at the same time after a forced
promotion. The proposal:

- Each member that holds a namespace answers its discovery document and
  JWKS from its replica. Only the home installation signs.
- The issuer base URL of a replicated namespace is a host name that the
  operator can move to the new home, for example a DNS name of the
  federation. A namespace that sets `issuerURL` keeps it.
- A planned promotion keeps the keys.
- A forced promotion makes the `next` key active, and deletes the old
  `active` key from the JWKS that the new home answers. A cloud that reads
  the JWKS from the new home then refuses a token that the old home signed
  during the partition. Tokens that the old home signed before the failure
  also stop working, and a job asks for a new one. Open question 5.

### Keys

Each installation has its own key-encryption key. The namespace key record
of a replicated namespace holds one wrap for each installation that holds
the namespace. RFC 79 section 3 recommends this for regions. The same
answer holds for installations. A customer key (ADR 217) in one KMS location
adds the latency of that location to each refresh on a far installation.
The docs can tell a customer to use a multi-Region key.

### Work in the background

Durable operations, schedules and retention of a namespace run only on its
home installation, with the lease of ADR 72 decision 2. That lease is local
to one database. It is not a home lease.

## Affected decisions, sections, glossary entries and code

Each row gives a proposed change: supersede, amend, rename or delete.
"Rename" means only the word "home region" or "region" changes, and the
meaning stays.

### ADRs

ADRs are immutable. The superseding ADR names each one, and each one gets
a status line that points to it.

| ADR | What it assumes | Proposed change |
| --- | --- | --- |
| 4 | Each namespace is authoritative in one home region. Other regions answer 421. Standby regions and promotion. | Supersede the first two decisions (home region, standby regions). Keep the fence decisions as written. |
| 12 | The shared core holds "home regions and fencing". | Rename to "home installations and fencing", in the status line. |
| 18 | A listing from a standby region reads replicated data. | Rename to standby installation. |
| 19 | The home region makes the ID of a stack. | Rename to home installation. |
| 30 | A restore keeps the home region and warns on `--region`. ADR 30 defers a restore into another region. | Amend: a restore keeps the home installation of each namespace, and warns when it is not this installation. |
| 38 | The manifest holds the home regions. | Rename to home installations. |
| 48 | Replication between regions is out of scope, and a future RFC decides how two regions reconcile. | Amend: there is no reconciliation. One writer for each namespace, and a forced promotion leaves a divergent tail that the server never merges. |
| 51 | A collection has no home region of its own. | Rename to home installation. |
| 145 | A namespace has its own home region, so each scope has its own sequence. | Rename to home installation. |
| 153 | An instance outside the home region of the source records no edge. | Amend: an installation that is not the home installation of the source records no edge. |
| 204 | A move to another region of the same installation is a promotion. Two signers never hold one key. | Amend decision 4: delete the case of a region inside one installation. Add the planned and forced promotion between installations. Decision 1 keeps the one-way door. |
| 215 | Decision 21 points to RFC 79 for routing between installations. | Amend decision 21 to point to the superseding ADR. |
| 218 | A namespace has one home region (decision 1 table). A reason to make a namespace is "another home region" (decision 5). Collections share the region (decision 2). | Amend: the row "Region" becomes "Home installation". Decision 5 says "the units need another home installation". |
| 241 | A failover makes later writes of an import fail, and `resume` on the new home region continues. | Rename to home installation. |

ADRs 216, 217, 219, 220 and 221 hold no region, home, lease, quorum or
consensus assumption. ADR 217 says "federated credential" for an Azure
feature, and ADR 220 says "lease renewal" for the Pulumi update lease. Both
are other concepts and stay. ADR 47 decision 6 and ADR 64 decision 10 fit
the decision and stay.

### RFCs

| RFC | Section | Proposed change |
| --- | --- | --- |
| 79 | Terms (namespace directory, control home), sections 3, 4 (rules 1, 4, 5), 5, 6 and 7 | Drop RFC 79 when the owner accepts the superseding ADR. Section 8 (joins) moves into the ADR with "installation" for "region". Open question 6 is moot: the owner chose writes over the lease. Open question 3 is moot: there is no control home. |
| 87 (PR #880) | Open question 6 (does a lease partition stop writes) | Moot. The owner decision is option c: a partition stops nothing, and only an operator promotes. |
| 87 (PR #880) | Open question 7 (which processes renew the home lease), and the sentence in "Components" on the renewal of the home lease | Moot. There is no home lease. Delete the sentence. |
| 37 | Terms ("home region in that installation"). Design point 1 option A "for regions". Design point 3 "Regions in one installation". Design point 5 "Regions, replication and backups". | Rename to installations. Keep the split of replication and backup, and the recovery objectives. |
| 2 | "A namespace homed elsewhere", listing from a standby region | Rename. |
| 4 | "Unit of tenancy, home region and fencing", two home regions in one view | Rename. |
| 8 | A backup stays in the home region, and a restore into another region | Rename to installation. |
| 17 | "Given home regions" | Rename. |
| 1 | "Home regions and authorization apply unchanged" | Rename. |
| 66 | ADR 4 moves a namespace for failover | No change: the text fits. |
| 68 | The setting `--region` / `DUSTPAN_REGION`, "moves write authority" | Amend: the setting becomes the installation name (open question 2). |
| 71 | A fork after a forced failover | No change: the text fits. |
| 76 | Points to federation and geo-replication | Point to this RFC. |

### Glossary

Do not edit `GLOSSARY.md` until the owner accepts the superseding ADR. Proposed
text for the section that replaces "Regions":

```markdown
### Federation

**Home installation**:
The one installation that accepts the writes of a namespace. The namespace row names it. A namespace gets the installation that creates it, and a promotion changes it.
_Avoid_: Home region, home instance, owning installation, primary, active installation

**Standby installation**:
An installation of a federation that holds a replicated copy of a namespace and accepts no writes to it. With request forwarding on, it forwards each write to the home installation.
_Avoid_: Secondary, replica installation, standby region

**Federation**:
A set of installations that an operator joins for request forwarding, asynchronous replication of namespaces, or both. A federation has no control home, no lease and no write quorum. Each installation keeps its own installation scope.
_Avoid_: Cluster, mesh, shard

**Federation member**:
One installation in a federation. Federation is opt-in, on a self-hosted installation and on OpenDustpan Cloud.
_Avoid_: Shard, cell, pod, node

**Request forwarding**:
The optional federation behavior in which a member sends a request for a namespace to its home installation and streams the answer back. The home installation checks the token and the authorization again.
_Avoid_: Proxying (alone), redirect

**Promotion**:
The operator action that makes a standby installation the home installation of a namespace. A planned promotion first demotes the old home. A forced promotion does not wait for the old home. The server never promotes on its own.
_Avoid_: Failover (as the name of the action), switchover, automatic failover

**Divergent tail**:
The changes that a former home installation wrote to a namespace after its last replicated change and before it applied a forced promotion. The server never merges them into the new home.
_Avoid_: Conflict, lost writes, split brain (as a name of the data)

**Failover version**:
A number that increases by one at each promotion of a namespace, and at each restore.
_Avoid_: Epoch, generation

**Fence**:
The namespace and failover version that a write carries. A store rejects the write when the failover version is no longer current.
_Avoid_: Token, guard
```

Other glossary changes:

- **Namespace**: "home region" becomes "home installation".
- **Installation**: add "An installation owns its namespaces. A namespace
  holds collections, and a collection holds units."
- **Home region** and **Standby region**: delete. Add "Home region" and
  "Region (of a namespace)" to the _Avoid_ lines above.
- **Federation member**: delete the reference to RFC 79, and the sentence
  that says federation is not decided. Delete the sentence "No API answer,
  error, ID or host name tells a tenant which member holds its namespace".
  Without forwarding, each member has its own public URL, and a standby
  answers 421.
- The entries that say "region" for a cloud region (the sweep target, the
  unmanaged finding) and for a language format stay.

### Code

No code changes in this RFC. Each row is a follow-up for the implementation.
No tag shipped a schema, so a rename of a column follows the collapse rule
of ADR 73, with no compatibility code (ADR 72).

| Path | What it assumes | Proposed change |
| --- | --- | --- |
| `pkg/namespace/namespace.go` | Package comment (region, promotes another region), `Region`, `ParseRegion`, `ErrInvalidRegion`, `ErrNotHome` text "not homed in this region", `Namespace.HomeRegion`, `Config.Region`, `Service.region`, `WritableHere` | Rename to the installation name and `HomeInstallation`. Keep `Fence`, `ErrFenced`, `ErrNotHome` and `WritableHere` with the new text. |
| `pkg/dustpan/dustpan.go` | `WithRegion` option, `Region` in the namespace and restore configuration | Rename the option to the installation name. |
| `cmd/opendustpan/main.go`, `internal/cli/settings.go`, `internal/cli/serve.go`, `internal/cli/reference.go` | `--region`, `DUSTPAN_REGION`, config key `region`, default `local`, "region this server is authoritative for" | Rename the setting (open question 2). `--s3-region` is a cloud region and stays. |
| `deploy/helm/opendustpan/` | `server.region`, the schema and the `--region` argument in `deployment.yaml` | Rename with the setting. The S3 `region` value stays. |
| `pkg/store/sqlite/`, `pkg/store/postgres/` | `home_region` column in `0100_baseline.sql` and `0105_namespace_ids.sql`, `queries.sql`, generated `internal/db` | Rename the column in the collapse, then `make generate`. |
| `pkg/store/internal/codec/codec.go` | `HomeRegion` in the row codec | Rename. |
| `pkg/backup/` (`backup.go`, `service.go`, `restore.go`, `restore_service.go`, `staging.go`, `portable_tables.go`) | Manifest `homeRegions`, the region warning, the `home_region` column of the portable format | Rename to home installations and the installation warning. |
| `internal/api/server/namespaces.go` | JSON `homeRegion`, "homed in the region of the server" | Rename to `homeInstallation`. The console may read this field: merge the console change before the next tag. |
| `internal/cli/client/namespace.go` | Output "created namespace X in region Y", `homeRegion` | Rename. |
| `internal/api/pulumi/pulumi.go`, `internal/api/tfe/errors.go`, `internal/api/inventory/inventory.go`, `internal/api/objectcontext/objectcontext.go`, `internal/api/profile/profile.go`, `internal/api/runnerapi/errors.go`, `internal/api/tofuhttp/tofuhttp.go` | `ErrNotHome` answers 421 with "namespace is not homed in this region" | Keep 421 until forwarding exists. Change the text to name the installation. |
| `pkg/collection/collection.go`, `pkg/secret/installation.go`, `pkg/stack/stack.go`, `pkg/workspace/service.go`, `pkg/gate/store.go`, `pkg/gate/gate.go`, `pkg/ipallow/ipallow.go` | Comments that say "home region" or "each region" | Rename in the comments. |
| `pkg/inventory/sweep.go` | Skips a namespace on `ErrNotHome` | No change. |
| Test fixtures (`collectiontest`, `historytest`, `profiletest`, `dependencytest`, `secrettest`, `audittest`, `accesstest`, `storetest`, `pkg/dustpan` tests and others) | `HomeRegion: "eu-west"` or `"local"` | Rename with the field. |
| Restore in both stores | Raises the failover version of every namespace | No change. |
| `docs/reference/configuration.md`, `docs/reference/helm-values.md`, `docs/guides/several-instances.md`, `docs/guides/deleted-units.md`, `docs/guides/backup-restore-upgrade.md` | The `--region` setting, "421: not homed in this region", home regions in the manifest, "restore into another region" | Change with the code, in the same pull request (ADR 214). |
| `CLAUDE.md`, `README.md` | Layout line "home region, failover-version fencing", "home region" | Rename with the code. |

## Options for the word

The owner asked for a word for the installation that accepts the writes of
a namespace.

1. **Home installation.** It keeps the word "home" that the code already
   uses (`ErrNotHome`, `WritableHere`, the error code `namespace.not-home`),
   so the rename touches the fewest names. "Principal home" (ADR 215) also
   uses "home" for "where an object belongs", with the values "the
   installation" or "one namespace". A reader can confuse "home
   installation" with "a principal whose home is the installation". The
   glossary entry must say that each is a different concept.
2. **Owning installation.** It matches the owner phrase "installations which
   own a namespace". The glossary already uses "Owner" for a context field
   and for an approval rule, and lists "Owner" under _Avoid_ in two entries.
   A third meaning adds to that.
3. **Home instance.** It clashes with **Instance**, one server process. An
   installation can run many instances. Do not use it.
4. **Primary installation.** The glossary avoids "primary" for regions.

**Recommendation: home installation.** It has the smallest change in code
and in error codes, and the clash with "principal home" is a clash of
context, not of meaning.

## Proposed superseding ADR

The next free ADR number at the time of writing is 248.

> # 248. Federation: installations, request forwarding, asynchronous replication and manual promotion
>
> Status: proposed. Supersedes ADR 4 decisions 1 and 2. Amends ADRs 12,
> 18, 19, 30, 38, 48, 51, 145, 153, 204, 215, 218 and 241.
>
> ## Context
>
> ADR 4 gave each namespace a home region and planned standby regions.
> RFC 79 added a control home, a home lease and a namespace directory, so a
> write could depend on another place. On 2026-10-19 the owner decided
> that OpenDustpan has no regions, and that a federation has no write
> quorum. Members can be 400 ms apart. A consensus protocol at that
> distance makes writes too slow or stops them.
>
> ## Decision
>
> 1. **Installations own namespaces.** An installation owns its
>    namespaces. A namespace holds collections, and a collection holds
>    units. The server has no region concept. Where a hosting provider runs
>    an installation is a placement detail.
> 2. **One home installation for each namespace.** The namespace row names
>    its home installation. Only the home installation writes the rows and
>    blobs of the namespace. Another installation answers `ErrNotHome`.
> 3. **The installation name.** The setting that named the region now names
>    the installation. The operator sets it, and it is `local` by default.
> 4. **The fence stays.** Each write in a namespace carries a
>    `namespace.Fence`, and the store checks it in the same transaction, as
>    ADR 4 decides. The failover version goes up by one at each promotion
>    and at each restore.
> 5. **Federation is opt-in and does two things.** With federation off, an
>    installation has none of the behavior of this ADR beyond decisions 1
>    to 4. With federation on, it can do one or both of these:
>    - **Request forwarding (optional).** A member forwards a request for a
>      namespace to its home installation. It streams the body, makes one
>      hop only, and passes the token of the caller unchanged. The home
>      installation checks the token and the authorization again. Members
>      trust each other through mutual TLS. Without forwarding, a standby
>      installation answers 421 to a write.
>    - **Asynchronous replication.** The home installation sends each
>      change of a namespace to each standby installation of the namespace.
>      Each change carries the failover version of its namespace and a
>      sequence number. A standby applies the changes in order, and refuses
>      a change with a lower failover version than the one it holds.
> 6. **No write waits for a peer.** A write never calls another
>    installation, waits for an acknowledgment, or renews a lease. There is
>    no control home, no home lease, no write quorum and no consensus
>    protocol. A network partition does not stop the writes of a home
>    installation.
> 7. **Promotion is manual.** Only an operator promotes a standby
>    installation. A planned promotion first demotes the old home, and
>    loses no change. A forced promotion does not wait for the old home.
>    The changes that the old home wrote after its last replicated change
>    are its divergent tail. The server never merges a divergent tail.
> 8. **The installation scope is local.** A federation replicates
>    namespaces only. Each installation keeps its own installation
>    principals, groups, roles, settings, `@server` audit scope and
>    sessions. The principals, groups and tokens whose home is a namespace
>    replicate with the namespace.
> 9. **Names.** A namespace name is unique on its installation. A standby
>    refuses to replicate a namespace whose name it uses for another
>    namespace ID, and reports the conflict.
> 10. **Joins between namespaces.** Each namespace writes its own half of a
>     join, on its own home installation, under its own fence. A read
>     across a join answers with the time of the data it read, and never
>     opens a transaction across two namespaces.
> 11. **Background work runs at home.** Durable operations, schedules and
>     retention of a namespace run only on its home installation.
>
> ## Consequences
>
> - `GLOSSARY.md` replaces the section "Regions" with "Federation":
>   **Home installation**, **Standby installation**, **Federation**,
>   **Federation member**, **Request forwarding**, **Promotion**,
>   **Divergent tail**, **Failover version** and **Fence**.
> - `pkg/namespace` renames `Region` and `HomeRegion`. The store adapters
>   rename the `home_region` column in the collapse before the next tag.
> - The setting `--region` gets the new name. The installation API answers
>   `homeInstallation`, and the console changes before the next tag.
> - A forced promotion can leave two writers until the promotion reaches
>   the old home. The docs tell the operator to stop the old home first,
>   and state the replication lag as the window of lost changes.
> - RFC 79 gets the status `dropped`. RFC 87 open questions 6 and 7 are moot.
>
> ## Benchmarks
>
> Designed against these, as a comparison only, never a compliance claim:
>
> - The multi-cluster replication of Temporal: one active cluster for each
>   namespace, a failover version, asynchronous replication and a manual
>   failover.
> - PostgreSQL streaming replication with a manual promotion of a standby.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- The multi-cluster replication of Temporal, which ADR 4 follows: one active
  cluster for each namespace, a failover version, asynchronous replication,
  and a failover that an operator starts.
- PostgreSQL streaming replication: a standby that an operator promotes with
  `pg_promote`, and a former primary that needs `pg_rewind` or a new base
  backup before it follows again. That is the model of the divergent tail.
- OpenID Connect Discovery 1.0 and RFC 7517 (JWK): a JWKS that a verifier
  reads, and from which the issuer can delete a key.

## Open questions

1. Does the owner accept the superseding ADR text above?
2. **The installation name.** Does the `--region` setting become
   `--installation` (`DUSTPAN_INSTALLATION`, an operator-set name, `local`
   by default), or does the server make a random installation ID at first
   start? Recommendation: an operator-set name. A random ID changes when an
   operator restores into a new database, and the namespace rows then name
   an installation that does not exist.
3. **Tokens of installation principals on a forward.** Does a forwarded
   request with a token of an installation principal of another member get
   refused, as this RFC proposes? Recommendation: yes, for the first
   federation. One identity provider for the members gives a person a
   token on each.
4. **The divergent tail.** Does the old home keep its divergent tail
   readable for an export, before an operator makes it a standby from a
   fresh copy? Recommendation: yes. The server never merges it.
5. **The issuer after a forced promotion.** Does a forced promotion
   activate the `next` key and delete the old `active` key from the JWKS?
   Recommendation: yes. A planned promotion keeps the keys.
6. **A word for the installation that accepts the writes.** See "Options
   for the word". Recommendation: home installation.

## Follow-up issues

Titles only. The project manager files them after the owner accepts the ADR.

1. Write ADR 248 and add status lines to ADRs 4, 12, 18, 19, 30, 38, 48, 51,
   145, 153, 204, 215, 218 and 241
2. Replace the "Regions" section of GLOSSARY.md with "Federation"
3. Drop RFC 79 and rename regions to installations in RFCs 1, 2, 4, 8, 17,
   37, 68 and 76
4. Delete open questions 6 and 7 and the home lease sentence from RFC 87
5. Rename namespace.Region and HomeRegion to the installation name and
   HomeInstallation
6. Rename the --region setting, its environment variable, its config key
   and the Helm value
7. Rename the home_region column in both stores in the next collapse
8. Rename homeRegions in the backup manifest and the restore warning
9. Rename homeRegion in the installation API and the namespace client
   command, with the console change
10. Name the installation in the ErrNotHome answer text of each adapter
11. Rename "home region" in code comments and test fixtures
12. Update the configuration, Helm values, several-instances, deleted-units
    and backup guides, CLAUDE.md and README for the installation name
13. Design the replication stream: changes, sequence, failover version and
    the refusal of a lower version
14. Design the planned and the forced promotion: the API route, the action
    and the audit entry
15. Design the divergent tail: detection, export and the return of the old
    home as a standby
16. Design request forwarding between members with mutual TLS
17. Design the namespace key record with one wrap for each installation
18. Design the issuer keys at a forced promotion
