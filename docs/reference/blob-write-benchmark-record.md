# Blob write benchmark record

This page is the benchmark record of the blob write design in which the database decides which write wins.
It compares two paths for each blob family:

- The old path: a PUT with `If-None-Match: *` under a fixed key. The store refuses a second write to the key.
- The new path: a PUT under a key that is unique for each attempt. The fenced database row decides which attempt wins.

The page gives no service level and makes no compliance claim.
The numbers hold for the stated host, stores and versions only.

## Protocol

| Rule | This run |
| --- | --- |
| Both paths on the same store, from the same client host, in the same time window | Yes. The runs of one family follow each other on one store |
| The runs alternate: old, new, old, new | Yes |
| A store that enforces conditional writes | MinIO. The run also measures Garage, which ignores `If-None-Match` |
| At least 10,000 operations for each run of each family | 10,000 |
| At least 5 runs of each path | 5 |
| Throughput at a fixed client concurrency | 16 workers |
| The measure | For each family, the median p50 and the median p99 of the new path are at most 5 % higher than on the old path. The median throughput is at most 5 % lower |
| The read paths that change | The audit checkpoint check, and the read before each write of a restore |

## Run it

```sh
make bench-s3-protocol
```

The target runs `scripts/bench-s3.sh` with `BENCH_S3_MODE=protocol`.
The script starts the same containers as `make bench-s3`.
See [S3 latency benchmark](s3-latency-benchmark.md) for the images, the profiles and the limits of the method.
The results go to `bench-s3-out/protocol.json` and `bench-s3-out/protocol.md`.
A run that stops keeps `protocol.json`.
The next run with the same `BENCH_S3_OUT` skips the results that the file holds.

| Variable | Default | Meaning |
| --- | --- | --- |
| `BENCH_S3_PROTOCOL_PROFILES` | `same-az cross-az eu-metro` | Profiles of the run, split by spaces |
| `BENCH_S3_ARGS` | empty | Extra flags of `cmd/s3bench`: `-ops`, `-runs`, `-concurrency`, `-stores` |

## Families

All families are models.
Each one sends the requests of its path in their order, against PostgreSQL model tables.
A fenced transaction is 4 round trips.
They are the start, a read of the fence row with a share lock, an insert, and the commit.
The insert writes the row that names the blob key.
No family uses server code yet.
A family can switch to its server code when the change of that family merges.

| Family | Object size | Source of the size | Old path | New path |
| --- | --- | --- | --- | --- |
| Pulumi checkpoint | 262,409 bytes | The deployment of 256 KiB of the load harness | PUT `If-None-Match` + fenced transaction | PUT under an attempt key + fenced transaction |
| Pulumi journal batch | 8,856 bytes | A journal batch of 20 entries of the load harness | Same as the checkpoint | Same as the checkpoint |
| Job log chunk | 16 KiB | Assumption. The load harness records no job log. The runner protocol allows 256 MiB in 20,000 chunks. That is about 13 KiB for each chunk | Same as the checkpoint | Same as the checkpoint |
| Unit import upload | 262,409 bytes | The import size of 256 KiB of the load harness | PUT `If-None-Match` + fenced transaction | Fenced transaction that records the attempt + PUT under an attempt key + fenced transaction |
| Backup target copy | 262,409 bytes | As the checkpoint | PUT `If-None-Match` | PUT under an attempt key |
| Restore write | 262,409 bytes | As the checkpoint | PUT `If-None-Match` | GET of the live key (absent) + fence check + PUT |
| Audit checkpoint write | 1 KiB | Assumption. A signed checkpoint is a small JSON document | PUT `If-None-Match` | PUT under an attempt key |
| Audit checkpoint check | 1 KiB | As the audit checkpoint write | GET of the one copy | LIST of the prefix of the position + GET of each copy (1 copy) |

The restore write measures a live store that does not hold the key.
When the live store holds the key, the new path reads it and skips the write.
