# 220. Isolation between accounts on shared infrastructure (owner)

Status: accepted · 2026-10-18

## Context

ADR 215 makes OpenDustpan Cloud multitenant: many accounts on one shard.
The project owner asked on 2026-10-18, as the project manager relayed, for
the isolation between tenants: the database, object storage, the fence,
rate limits for each account, limits against a noisy neighbor, and the tests
that prove that no account reads another.

What exists:

- Rows of a namespace hold its namespace ID (ADR 166 decision 10, the stable
  namespace ID).
- ADR 72 decision 5 puts control tables in a separate PostgreSQL schema,
  outside the restore swap. Decision 9 adds an in-process cache.
- ADR 166 decision 14: one 404 rule for an object that the caller cannot
  read.
- RFC 68 "Fair-use limits": a storage limit, history retention and a
  request rate, with token and namespace buckets. "A limit never stops work
  in progress."
- RFC 58 and ADR 45: a bound on request bodies in flight.

The AWS whitepaper "SaaS Tenant Isolation Strategies" names three models:
silo (one stack for each tenant), pool (shared resources, isolation by
policy) and bridge (a mix). ADR 59 has no dedicated tier, and ADR 215 has
no installation for each customer, so the data of a shard uses the pool
model. Shards are a bridge that only the operator controls.

## Decision

### The database

1. **Pooled rows with an account ID.** All accounts of a shard share one
   database and one schema. Each row that belongs to an account holds its
   account ID: each row with a namespace ID, and each account row
   (principals, groups, roles, grants, tokens, sessions, settings, audit
   entries, keys). A row of the installation scope holds no account ID.
   - **Why not a schema for each account.** Each migration would run once
     for each account, and the restore swap of ADR 72 decision 5 renames
     schemas. A connection pool would serve one schema at a time. A schema
     for each account also gives no isolation that row-level security does
     not give.
2. **Row-level security on PostgreSQL.** Each table with an account ID has
   a PostgreSQL row-level security policy:
   `account_id = current_setting('dustpan.account_id', true)`. Each transaction
   of an account request sets it with `SET LOCAL`, from the account that
   the request resolved (ADR 215 decision 9). A transaction that sets no
   account sees no account row. So a query that forgets the account answers
   nothing, never another account's rows.
   - The database role of the server for requests has no `BYPASSRLS`.
   - Operator work across accounts (an installation backup, a restore, a
     migration, the retention expiry) uses a second database role with
     `BYPASSRLS`. Only code of the operator scope (ADR 216) opens it.
3. **Each query names the account too.** Row-level security is the second
   check. Each store query of an account row also has `account_id = $n` in
   its `WHERE` clause, so SQLite, which has no row-level security, has the
   first check. A cache key (ADR 72 decision 9) holds the account ID.
4. **SQLite.** SQLite has no row-level security. Until RFC 76 decides, an
   installation on SQLite holds one account only. OpenDustpan Cloud runs
   PostgreSQL only.

### Object storage

5. **A prefix for each account.** Each blob key of an account starts with
   `accounts/<account ID>/namespaces/<namespace ID>/`. Item key blobs of
   ADR 151 go to `accounts/<account ID>/namespaces/<namespace ID>/item-keys/<item key ID>`.
   No tag shipped a blob key (ADR 73), so the server keeps no old layout.
6. **A scoped view of the blob port.** A domain service of an account gets
   a view of the blob store for that account. The view refuses each key
   outside its prefix with `blob.ErrOutOfScope`, before any call to the
   adapter. The `blobtest` suite checks the view.
7. **One identity for the bucket.** The server uses one cloud identity for
   the bucket of a shard. Isolation in the bucket comes from decisions 5
   and 6 and from the encryption of ADR 217, not from a bucket policy for
   each account. Each blob of an account is sealed under a key of its
   namespace, so a blob read with another account's keys does not open.

### The fence and the request path

8. **The account check in the write path.** Each write transaction reads
   the state of its account in the same statement that reads the namespace
   fence, as ADR 72 decision 4 reads the installation state. A write in a
   `suspended` or `deleted` account fails in that transaction.
9. **IDs never give access.** Namespace IDs, unit IDs, TFE IDs (`ws-`,
   `sv-`), job IDs and item key IDs are unique in the installation. The
   server finds an object by its ID and the account of the request,
   together. An ID of another account answers 404, as for an ID that does
   not exist (ADR 166 decision 14).
10. **No feature reads across accounts.** A namespace link (ADR 163), an
    output-sharing rule, a dependency edge, a namespace backup and a
    blueprint each join objects of one account only. A request that names
    an object of another account answers the error of an unknown object.

### Limits against a noisy neighbor

11. **An account bucket for the request rate.** The request rate limits of
    RFC 68 get an account bucket, with the setting
    `rateLimit.account.requestsPerSecond` and its burst. A request takes a
    token from its token bucket, its namespace bucket and its account
    bucket. The exempt calls of RFC 68 (work in progress, probes, metrics)
    stay exempt from the account bucket.
12. **A share of each shared resource.** Each of these settings has an
    installation value and an account value that is only lower:
    - `limits.account.bodiesInFlightBytes`: the share of the bound on
      request bodies in flight (ADR 45) that one account can hold.
    - `limits.account.databaseConnections`: the share of the database pool
      that the requests of one account can hold at one time.
    - `limits.account.operationsInFlight`: the share of durable operations
      (ADR 72) of one account that the instances run at one time. A
      scheduler picks the next operation of the account that has the fewest
      in flight.
    - `limits.storageBytes`: the storage limit (ADR 215 decision 12).

    At a share limit, a new request waits up to 5 seconds, then answers 429
    with `Retry-After`. Work in progress of the account is never refused
    because of the share of another account.
13. **Metrics.** The metrics of these limits have no account label (ADR 32
    decision 5). The count of refusals goes to the audit scope of the
    account, one entry for each minute with refusals, so the tenant sees
    them.

### Tests that prove no read across accounts

14. **A conformance case for each store port.** `storetest` and each other
    `*test` suite get a case with two accounts, A and B, that hold the
    same names: the same namespace names, unit names and group names. Each
    read port, with the context of A, answers no row of B. Each write port,
    with an object ID of B, changes nothing and answers the not-found
    error.
15. **A route test across accounts.** A test makes two accounts with the
    same objects. For each route of the route table, it calls the route on
    the host of A, with a token of A, and with the IDs of the objects of B
    in each path parameter and in each body field that holds an ID. Each
    call must answer 404 or 401, and no answer body may hold a byte of an
    ID or a name of B. A new route without a case fails the test, as a
    route without an action fails the route test of ADR 17.
16. **A row-level security test.** On PostgreSQL, a test runs each query of
    `queries.sql` in a transaction with no account set and checks that it
    answers no account row. A second test checks that the request role
    has no `BYPASSRLS`.
17. **A blob scope test.** A test calls each blob read of each domain
    service with a key of another account and checks `blob.ErrOutOfScope`.
18. **An e2e step.** `make e2e` runs the Pulumi CLI and OpenTofu against
    two accounts on one server. The CLI of account A lists, reads, locks
    and writes in its own namespaces, and gets a not-found error for each
    namespace and workspace of account B with the same name.
19. **A cache test.** A test fills the in-process cache from account A and
    reads with account B the same key. It must miss.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- AWS whitepaper "SaaS Tenant Isolation Strategies" and the AWS SaaS Lens:
  the pool model with a tenant context on each request (decisions 1 to 3)
  and noisy-neighbor limits (decisions 11 and 12).
- Google Cloud Architecture Center, multi-tenant SaaS: a tenant ID in the
  data and a check on each request.
- SOC 2 CC6.1 (logical access restricted to authorized users) and ISO/IEC
  27001:2022 Annex A 8.3 (information access restriction): decisions 2, 3,
  9 and 10. Decisions 14 to 19 are the evidence that the controls work.
- ISO/IEC 27001:2022 Annex A 8.6 (capacity management): decisions 11 and 12.

## Not decided here

- The default value of each limit on OpenDustpan Cloud. The operator sets
  them in the static layer.
- More than one account on SQLite (decision 4, RFC 76).

## Consequences

- The migration of ADR 215 adds the account ID to each table of decision
  1, and, on PostgreSQL, the policies of decision 2 and the second
  database role.
- `pkg/blob` gets the scoped view and `ErrOutOfScope`. `blobtest` checks
  it.
- The tests of decisions 14 to 19 land with the first change that lets an
  installation hold a second account. That change cannot merge before the
  tests pass.
