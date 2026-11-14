# 220. Isolation between namespaces on shared infrastructure (owner)

Status: accepted · 2026-10-18

Changed by:

- ADR 273 changes decision 12: a sharing rule needs no acceptance of both
  namespaces.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 363 extends decisions 1, 2 and 3: their work starts after RFC 103 step 8.

## Context

ADR 215 makes OpenDustpan Cloud multitenant, with the namespace as the
tenant. The project owner asked on 2026-10-18, as the project manager
relayed, for the isolation between tenants: the database, object storage,
the fence, rate limits, limits against a noisy neighbor, and the tests that
prove that no namespace reads another.

What exists:

- Each row of a namespace holds its stable namespace ID (ADR 166 decision
  10).
- ADR 72 decision 5 puts control tables in a separate PostgreSQL schema,
  outside the restore swap. Decision 9 adds an in-process cache.
- ADR 166 decision 14: one 404 rule for an object that the caller cannot
  read.
- RFC 68 "Fair-use limits": a storage limit, history retention and a
  request rate, with token and namespace buckets. "A limit never stops work
  in progress."
- ADR 45: a bound on request bodies in flight.

The AWS whitepaper "SaaS Tenant Isolation Strategies" names three models:
silo (one stack for each tenant), pool (shared resources, isolation by
policy) and bridge (a mix). ADR 215 has no installation for each customer,
so the data of OpenDustpan Cloud uses the pool model.

An earlier draft of this ADR isolated accounts. The owner did not take the
account, so this ADR isolates namespaces.

## Decision

### The database

1. **Pooled rows with a namespace ID.** All namespaces share one database
   and one schema. Each row that belongs to a namespace holds its namespace
   ID. This includes the principals, groups, custom roles, grants, tokens,
   sessions, sign-in settings and keys whose home is a namespace (ADR 215
   decision 9). A row of the installation holds no namespace ID.
   - **Why not a schema for each namespace.** Each migration would run once
     for each namespace, and the restore swap of ADR 72 decision 5 renames
     schemas. A connection pool would serve one schema at a time. A schema
     for each namespace also gives no isolation that row-level security
     does not give.
2. **Composite foreign keys.** A row that names another row of a namespace
   names it with the pair (namespace ID, ID), for example
   `FOREIGN KEY (namespace_id, unit_id) REFERENCES units (namespace_id, id)`.
   So the database refuses a row that points to a row of another namespace.
3. **Row-level security on PostgreSQL.** Each table with a namespace ID
   gets these statements:
   - `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` and
     `ALTER TABLE ... FORCE ROW LEVEL SECURITY`, so the policy also applies
     to the owner of the table.
   - A policy that lets a row through only when its namespace ID is in the
     setting `dustpan.namespace_ids` of the transaction.
   - The database role of the server for requests owns no table and has no
     `BYPASSRLS`. The migrations run with another role, which owns the
     tables.
   - Each request runs its SQL in an explicit transaction. The first
     statement is `SET LOCAL dustpan.namespace_ids = ...`, with the
     namespaces that the request may read: the namespace of the path, or
     for `namespaces/-` the namespaces where the caller has a grant (ADR
     216 decision 10). `SET LOCAL` ends with the transaction, so a pooled
     connection never keeps the value of another request.
   - The lookups that find the namespaces of a request run before the
     value is set: a token or a session by its hash, a namespace by its
     name, an object by its ID, and the namespaces where a principal holds
     a grant. Each is a `SECURITY DEFINER` function, owned by the migration
     role, that answers only IDs (namespace, principal, home). The request
     role can execute these functions and reads no table outside the
     policy. The test of decision 18 checks the list of these functions.
   - A transaction that sets no value sees no namespace row. So a query
     that forgets the namespace answers nothing, never the rows of another
     namespace.
   - Operator work across namespaces (an installation backup, a restore, a
     migration, the retention expiry) uses a separate database role with
     `BYPASSRLS`. Only code of the operator scope (ADR 216) opens it.
4. **Directory rows are not isolated by row-level security.** The
   principals, groups and roles of the installation hold no namespace ID,
   so no policy filters them. A member list of a namespace is a query over
   the grants on the namespace, joined to the principals that hold them. No
   route of a namespace reads the directory of the installation directly.
5. **Each query names the namespace too.** Row-level security is the second
   check. Each store query of a namespace row also has `namespace_id = $n`
   (or `= ANY($n)`) in its `WHERE` clause. SQLite has no row-level
   security, so this is the only check there. A cache key (ADR 72 decision
   9) holds the namespace ID.
6. **SQLite.** OpenDustpan Cloud runs PostgreSQL only. RFC 76 asks whether
   an installation on SQLite can use `identity.principalHome: namespace`.

### Object storage

7. **A prefix for each namespace.** Each blob key of a namespace starts with
   `namespaces/<namespace ID>/`. The item key blobs of ADR 151 go to
   `namespaces/<namespace ID>/item-keys/<item key ID>/v<version>`. No tag shipped a
   blob key (ADR 73), so the server keeps no old layout and no conversion.
8. **A scoped view of the blob port.** A domain service of a namespace gets
   a view of the blob store for that namespace. The view refuses each key
   outside its prefix with `blob.ErrOutOfScope`, before any call to the
   adapter. The `blobtest` suite checks the view.
9. **One identity for the bucket.** The server uses one cloud identity for
   its bucket. Isolation in the bucket comes from decisions 7 and 8 and
   from the encryption of ADR 217, not from a bucket policy for each
   namespace. Each blob of a namespace is sealed under a key of that
   namespace, so a blob read with the keys of another namespace does not
   open.

### The fence and the request path

10. **The state check in the write path.** Each write transaction reads the
    namespace row for its fence (ADR 4). The same read takes the namespace
    state, so a write in a `suspended` or `disabled` namespace fails in that
    transaction (ADR 215 decision 17).
11. **IDs never give access.** Namespace IDs, unit IDs, TFE IDs (`ws-`,
    `sv-`), job IDs and item key IDs are unique in the installation. The
    server finds an object by its ID and the namespaces of the request,
    together. An ID of another namespace answers 404, as for an ID that
    does not exist (ADR 166 decision 14). The answer holds no name, ID or
    count of the other namespace.
12. **Joins across namespaces.** A namespace link, an output-sharing rule
    and a dependency edge across namespaces exist only with the acceptance
    of both namespaces (ADR 216 decision 8). A namespace backup and a
    blueprint never cross namespaces (ADR 218).

### Limits against a noisy neighbor

13. **A share of each shared resource.** Each of these settings has an
    installation value, and a namespace value that is only lower:
    - `rateLimit.requestsPerSecond` and `rateLimit.burst` for the namespace
      bucket of RFC 68. A request takes a token from its token bucket and
      from its namespace bucket.
    - `limits.namespace.bodiesInFlightBytes`: the share of the bound on
      request bodies in flight (ADR 45) that one namespace can hold.
    - `limits.namespace.databaseConnections`: the share of the database
      pool that the requests of one namespace can hold at one time.
    - `limits.namespace.operationsInFlight`: the share of durable
      operations (ADR 72) of one namespace that the instances run at one
      time. A scheduler picks the next operation of the namespace that has
      the fewest in flight.
    - `limits.storageBytes`: the storage limit, shown as "storage limit"
      (owner). It replaces the name `quota.storageBytes` of RFC 68, because
      the catalog of ADR 115 uses "quota" for another concept. RFC 68
      "Storage quota" keeps its other rules.

    At a share limit, a new request waits up to 5 seconds, then answers 429 with `Retry-After`. At the storage limit, the server refuses the start of new work at once, with no wait.
14. **Work in progress is exempt from shares.** The calls of an update, a
    run or a job phase in progress (lease renewal, checkpoints, events, log
    chunks, the final state write) never wait for a share and never get 429
    from a share. The exempt calls of RFC 68 (probes, metrics) stay exempt.
15. **Metrics.** The metrics of these limits have no namespace label (ADR
    32 decision 5). The count of refusals goes to the audit scope of the
    namespace, one entry for each minute with refusals, so the tenant sees
    them.

### Tests that prove no read across namespaces

16. **A conformance case for each store port.** `storetest` and each other
    `*test` suite get a case with two namespaces, A and B, that hold the
    same unit names, group names and role names. Each read port, with the
    namespaces of A, answers no row of B. Each write port, with an object ID
    of B, changes nothing and answers the not-found error.
17. **A route test across namespaces.** A test makes two namespaces with
    the same objects and a principal of each. For each route of the route
    table, inside and outside `/api/dustpan/v1` (ADR 216 decision 15), it
    calls the route with a token of A, and with the IDs and names of the
    objects of B in each path parameter and in each body field that holds
    an ID. Each call must answer 404, 403 or 401, and no answer body may
    hold a byte of an ID or a name of B. A new route without a case fails
    the test.
18. **A row-level security test.** On PostgreSQL, a test runs each query of
    `queries.sql` in a transaction with no namespace set, and checks that it
    answers no namespace row. A second test checks that the request role
    owns no table and has no `BYPASSRLS`, and that each table with a
    namespace ID has `FORCE ROW LEVEL SECURITY`. A third test compares
    the `SECURITY DEFINER` functions that the request role can execute
    with the list of decision 3, and fails for each function that is not
    on the list.
19. **A foreign key test.** A test inserts a row of namespace A that names
    a row of namespace B, and checks that each dialect refuses it.
20. **A blob scope test.** A test calls each blob read of each domain
    service with a key of another namespace and checks `blob.ErrOutOfScope`.
21. **An e2e step.** `make e2e` runs the Pulumi CLI and OpenTofu against
    two namespaces on one server, each with its own principal. The CLI with
    the token of A lists, reads, locks and writes in A. It gets a not-found
    error for each stack and workspace of B.
22. **A cache test.** A test fills the in-process cache from namespace A
    and reads the same key for namespace B. It must miss.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- AWS whitepaper "SaaS Tenant Isolation Strategies" and the AWS SaaS Lens:
  the pool model with a tenant context on each request (decisions 1 to 5)
  and noisy-neighbor limits (decisions 13 and 14).
- The PostgreSQL documentation on row security policies: `FORCE ROW LEVEL
  SECURITY` for table owners, and `BYPASSRLS` (decision 3).
- Google Cloud Architecture Center, multi-tenant SaaS: a tenant ID in the
  data and a check on each request.
- NIST SP 800-53 AC-4 (information flow enforcement) and SC-4 (information
  in shared system resources): decisions 3, 5, 11 and 14. Decisions 16 to
  22 are the tests that show the controls work.

## Not decided here

- The default value of each limit on OpenDustpan Cloud. The operator sets
  them in the static layer.
- `identity.principalHome: namespace` on SQLite (decision 6, RFC 76).

## Consequences

- The schema changes of ADR 215 add the namespace ID to each table of
  decision 1, the composite foreign keys of decision 2, and, on
  PostgreSQL, the policies and the two database roles of decision 3.
- `pkg/blob` gets the scoped view and `ErrOutOfScope`. `blobtest` checks
  it.
- The tests of decisions 16 to 22 land with the first change that sets
  `identity.principalHome: namespace` in a test. That change cannot merge
  before the tests pass.
