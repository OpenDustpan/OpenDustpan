# Round 17: performance check

Range: 4a78446d11cb..4daa46bceabe (108 commits, 1299 files).

## Findings

- #3808: event receipt insert takes a table lock and counts all rows on each delivery.
  PostgreSQL 18, 1 client: 1.0 ms at 10k rows, 2.2 ms at 50k, 3.4 ms at 100k (the bound).
  8 clients at 100k rows: 28 ms latency, 284 transactions per second in total.

## Checked, no finding

- internal/logguard: BenchmarkHandle, JSON handler, 3 attributes. Plain 1.1 us, 4 allocs.
  With guard 2.4-3.0 us, 9 allocs. About 1.8 us more for each record. Not filed.
- pkg/workspace upload: BenchmarkUpload32MiB 235 ms, 6.7 MB/op. Has its own memory guard.
- Index check of new queries: collection_members(unit_id) is the primary key.
  inventory_locked and stack_tags_name_value are new partial and covering indexes.

## Not covered

pkg/store/sqlite queries, pkg/firstadmin, internal/trustrulesvc (mtls_exchange),
internal/api/server, internal/api/inventory, pkg/forge/github, pkg/session,
notification LATERAL join query, durable operation queries, e2e and docs.
