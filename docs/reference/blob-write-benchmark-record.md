# Blob write benchmark record

This page is the benchmark record of the blob write design in which the database decides which write wins.
It compares two paths for each blob family:

- The old path: a PUT with `If-None-Match: *` under a fixed key. The store refuses a second write to the key.
- The new path: a PUT under a key that is unique for each attempt. The fenced database row decides which attempt wins.

The page gives no service level and makes no compliance claim.
The numbers hold for the stated host, stores and versions only.

## Result

Measured on 2026-10-30, on MinIO, at the profiles `same-az`, `cross-az` and `eu-metro`.

- 12 of the 27 MinIO rows meet the measure.
- 9 rows do not meet it because the new path adds a round trip, a LIST or a transaction. These are the unit import upload, the restore write with an absent key, and the audit checkpoint check, at each profile.
- 3 rows do not meet it on p99 only. In these families the two paths send the same requests. The difference is inside the spread of the runs.
- 3 rows are invalid: the restore write with a present key had errors on MinIO.
- The hot paths of Pulumi checkpoints and of journal batches meet the measure at each profile on p50 and on throughput.

Garage gets no verdict, because it does not enforce `If-None-Match`, so its old path does not run as intended.

## What ran

| Item | Value |
| --- | --- |
| Date | 2026-10-30 |
| Host | Intel Xeon processor at 2.30 GHz, 4 CPUs, 16 GB of memory, Linux on amd64, Docker 29.9.2. The stores, the database, the proxy and the benchmark share this host |
| Store that enforces conditional writes | MinIO RELEASE.2026-10-23T20-06-51Z (`cgr.dev/chainguard/minio` at `sha256:e70b2b1e74d79457f808710483bd726ff364529ca30736eaa15a6ab547d3a5a7`), one node, data on the disk |
| Store that ignores conditional writes | Garage v2.1.0 (`dxflrs/garage` at `sha256:4c9b34c113e61358466e83fd6e7d66e6d18657ede14b776eb78a93ee8da7cf6a`), one node |
| Database | PostgreSQL 18.6 |
| Operations for each run | 10,000 |
| Runs of each path | 5, in the order old, new, old, new |
| Client concurrency | 16 workers |
| Profiles | `same-az` (no added delay), `cross-az` (1 ms round trip), `eu-metro` (10 ms round trip, jitter of ±1 ms in each direction). The delay applies to the S3 path and to the database path |
| The measure | For each family, the median p50 and the median p99 of the new path are at most 5 % higher than on the old path. The median throughput is at most 5 % lower |

[S3 latency benchmark](s3-latency-benchmark.md) gives the sources of the profiles and the limits of the method.

All families are models.
Each one sends the requests of its path in their order, against PostgreSQL model tables.
No family uses server code yet, because no change of a family has merged.
A fenced transaction is 4 round trips.
They are the start, a read of the fence row with a share lock, an insert, and the commit.
The insert writes the row that names the blob key.

| Family | Object size | Old path | New path | Path kind |
| --- | --- | --- | --- | --- |
| Pulumi checkpoint | 262,409 bytes | PUT `If-None-Match` + fenced transaction | PUT under an attempt key + fenced transaction | Hot: each checkpoint save of an update |
| Pulumi journal batch | 8,856 bytes | Same as the checkpoint | Same as the checkpoint | Hot: each journal write of an update |
| Job log chunk | 16 KiB | Same as the checkpoint | Same as the checkpoint | Hot: each log upload of a runner |
| Unit import upload | 262,409 bytes | PUT `If-None-Match` + fenced transaction | Fenced transaction that records the attempt + PUT under an attempt key + fenced transaction | Rare: one for each import |
| Backup target copy | 262,409 bytes | PUT `If-None-Match` | PUT under an attempt key | Background: each blob of a backup |
| Restore write, key absent | 262,409 bytes | PUT `If-None-Match` | GET of the live key + fence check + PUT | Background: each blob of a restore, in maintenance mode |
| Restore write, key present | 262,409 bytes | PUT `If-None-Match`, refused | GET of the live key, compare of the size and the hash, no PUT | Background: as above |
| Audit checkpoint write | 1 KiB | PUT `If-None-Match` | PUT under an attempt key | Background: each checkpoint of the audit chain |
| Audit checkpoint check | 1 KiB | GET of the one copy | LIST of the prefix of the position + GET of each copy (1 copy) | Background: a check of the audit chain |

The sizes of the checkpoint, the journal batch and the unit import come from the load harness.
The sizes of the job log chunk and of the audit checkpoint are assumptions, because the load harness records neither.
A job log chunk of 16 KiB is near the mean that the runner protocol allows: 256 MiB in 20,000 chunks.

## MinIO results

Values are the median of the 5 runs.
Δ is the new path against the old path.

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | Pulumi checkpoint | 26.4 / 63.5 | 26.7 / 61.4 | 565 / 562 | +1.2 % | -3.3 % | -0.6 % | meets |
| same-az | Pulumi journal batch | 15.0 / 39.6 | 15.6 / 39.8 | 973 / 948 | +3.9 % | +0.7 % | -2.6 % | meets |
| same-az | job log chunk | 15.3 / 38.8 | 14.2 / 40.8 | 900 / 986 | -6.9 % | +5.3 % | +9.6 % | does not meet |
| same-az | unit import upload | 27.2 / 84.5 | 31.5 / 93.1 | 531 / 463 | +15.8 % | +10.1 % | -12.8 % | does not meet |
| same-az | backup target copy | 22.8 / 56.5 | 22.3 / 53.5 | 649 / 674 | -2.0 % | -5.3 % | +3.9 % | meets |
| same-az | restore write (key absent) | 23.1 / 54.9 | 27.8 / 62.2 | 650 / 546 | +20.2 % | +13.4 % | -16.1 % | does not meet |
| same-az | restore write (key present) | 23.5 / 38.9 | 7.9 / 25.7 | 669 / 1591 | -66.3 % | -34.0 % | +137.6 % | invalid (5632 errors) |
| same-az | audit checkpoint write | 9.2 / 22.5 | 9.4 / 24.3 | 1617 / 1603 | +1.9 % | +8.1 % | -0.9 % | does not meet |
| same-az | audit checkpoint check (read) | 3.1 / 11.0 | 7.1 / 19.6 | 4415 / 2094 | +129.3 % | +79.0 % | -52.6 % | does not meet |
| cross-az | Pulumi checkpoint | 29.4 / 59.0 | 29.4 / 60.7 | 516 / 517 | -0.1 % | +2.9 % | +0.2 % | meets |
| cross-az | Pulumi journal batch | 16.0 / 29.2 | 16.1 / 31.8 | 956 / 925 | +0.8 % | +9.0 % | -3.2 % | does not meet |
| cross-az | job log chunk | 17.4 / 33.5 | 18.0 / 34.1 | 877 / 843 | +3.5 % | +1.8 % | -3.9 % | meets |
| cross-az | unit import upload | 28.6 / 62.6 | 33.8 / 62.0 | 527 / 453 | +18.2 % | -1.0 % | -14.0 % | does not meet |
| cross-az | backup target copy | 22.5 / 51.4 | 22.9 / 50.4 | 670 / 666 | +1.5 % | -1.9 % | -0.6 % | meets |
| cross-az | restore write (key absent) | 22.3 / 50.8 | 25.8 / 53.1 | 678 / 591 | +15.5 % | +4.6 % | -12.8 % | does not meet |
| cross-az | restore write (key present) | 22.9 / 36.7 | 8.0 / 21.4 | 687 / 1383 | -65.0 % | -41.7 % | +101.3 % | invalid (7118 errors) |
| cross-az | audit checkpoint write | 10.3 / 24.0 | 10.1 / 22.5 | 1462 / 1514 | -2.0 % | -6.5 % | +3.6 % | meets |
| cross-az | audit checkpoint check (read) | 3.5 / 8.8 | 7.8 / 16.4 | 4288 / 1948 | +125.2 % | +86.4 % | -54.6 % | does not meet |
| eu-metro | Pulumi checkpoint | 57.4 / 74.0 | 57.3 / 72.2 | 275 / 275 | -0.1 % | -2.4 % | +0.2 % | meets |
| eu-metro | Pulumi journal batch | 54.0 / 60.8 | 53.9 / 61.7 | 295 / 295 | -0.2 % | +1.5 % | +0.1 % | meets |
| eu-metro | job log chunk | 54.0 / 62.2 | 54.0 / 61.9 | 295 / 295 | +0.1 % | -0.4 % | +0.0 % | meets |
| eu-metro | unit import upload | 57.3 / 72.3 | 98.1 / 118.4 | 276 / 162 | +71.3 % | +63.7 % | -41.3 % | does not meet |
| eu-metro | backup target copy | 23.0 / 44.5 | 22.6 / 46.2 | 660 / 663 | -1.7 % | +3.8 % | +0.5 % | meets |
| eu-metro | restore write (key absent) | 22.7 / 43.0 | 39.7 / 55.7 | 662 / 394 | +74.4 % | +29.4 % | -40.4 % | does not meet |
| eu-metro | restore write (key present) | 22.6 / 37.1 | 12.6 / 19.5 | 692 / 1232 | -44.4 % | -47.5 % | +78.0 % | invalid (2737 errors) |
| eu-metro | audit checkpoint write | 13.4 / 19.8 | 13.2 / 19.4 | 1167 / 1187 | -1.2 % | -1.9 % | +1.6 % | meets |
| eu-metro | audit checkpoint check (read) | 10.8 / 13.4 | 21.7 / 26.1 | 1471 / 733 | +100.9 % | +94.2 % | -50.2 % | does not meet |

## Families that do not meet the measure

| Family | Cause | Path kind | One option to reduce it |
| --- | --- | --- | --- |
| Audit checkpoint check | One LIST for each position before the GET. The LIST costs more than the GET: at `same-az` the new path is 7.1 ms against 3.1 ms | Background | List the copies of many positions with one paged LIST of up to 1,000 objects, then GET each copy. One LIST then serves up to 1,000 positions |
| Unit import upload | One more fenced transaction, 4 database round trips, records the attempt before the PUT. At `eu-metro` this adds 41 ms at p50 and 46 ms at p99 | Rare | Record the attempt in one statement that checks the fence, or in the transaction that creates the import. This is 1 round trip or none |
| Restore write, key absent | One GET of the live key (1 S3 round trip) and one fence check (1 database round trip) before each PUT. At `eu-metro` this adds 17 ms at p50 and 13 ms at p99 | Background, in maintenance mode | Find the present keys with one paged LIST of the live prefix for each 1,000 blobs, and check the fence once for each batch |
| Pulumi journal batch (`cross-az`, p99 +9.0 %), job log chunk (`same-az`, p99 +5.3 %), audit checkpoint write (`same-az`, p99 +8.1 %) | No extra request. The two paths send the same requests, except the header and the key name. The p99 values of the old runs and of the new runs overlap. For the journal batch they are 28.5 to 33.4 ms against 28.8 to 35.7 ms | Hot (journal batch, job log chunk) and background (audit checkpoint write) | No change of the code reduces it. More runs, or a host that runs only the benchmark, narrow the spread of p99 |

## Invalid results

The restore write with a present key is invalid on MinIO at each profile.
In run 3 of the old path, 1,770 of the 10,000 conditional PUTs failed with a reset connection, in each profile.
After it, the GETs of the new path in runs 3 to 5 failed with reset connections too.
The run did not find the cause.
On Garage, this family had no error.
There the new path is a GET and a compare, and it writes nothing.
It is 30 % to 38 % faster at p50 than the old path.

## Garage results

Garage ignores `If-None-Match`, so the old path writes again where MinIO refuses.
These rows have no verdict.

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | Pulumi checkpoint | 72.5 / 133.6 | 76.9 / 141.3 | 216 / 204 | +6.1 % | +5.8 % | -5.6 % |
| same-az | Pulumi journal batch | 66.0 / 123.9 | 58.8 / 110.3 | 237 / 266 | -10.9 % | -11.0 % | +12.2 % |
| same-az | job log chunk | 71.3 / 129.1 | 70.8 / 131.6 | 221 / 221 | -0.7 % | +1.9 % | +0.0 % |
| same-az | unit import upload | 78.8 / 146.3 | 83.1 / 150.5 | 198 / 188 | +5.5 % | +2.9 % | -5.1 % |
| same-az | backup target copy | 75.8 / 140.8 | 77.0 / 137.5 | 207 / 204 | +1.6 % | -2.4 % | -1.0 % |
| same-az | restore write (key absent) | 78.8 / 144.7 | 90.8 / 154.8 | 199 / 174 | +15.2 % | +7.0 % | -12.5 % |
| same-az | restore write (key present) | 77.4 / 137.7 | 48.1 / 64.2 | 204 / 420 | -37.9 % | -53.4 % | +105.4 % |
| same-az | audit checkpoint write | 15.0 / 29.0 | 15.1 / 29.5 | 1050 / 1047 | +0.7 % | +1.9 % | -0.3 % |
| same-az | audit checkpoint check (read) | 4.2 / 9.4 | 102.3 / 213.7 | 3716 / 156 | +2337.5 % | +2163.0 % | -95.8 % |
| cross-az | Pulumi checkpoint | 73.4 / 132.7 | 77.2 / 134.8 | 213 / 206 | +5.2 % | +1.6 % | -3.3 % |
| cross-az | Pulumi journal batch | 68.8 / 121.5 | 68.2 / 121.3 | 230 / 231 | -0.9 % | -0.2 % | +0.4 % |
| cross-az | job log chunk | 72.5 / 127.4 | 71.5 / 127.8 | 217 / 218 | -1.4 % | +0.3 % | +0.5 % |
| cross-az | unit import upload | 82.1 / 141.8 | 87.4 / 144.4 | 194 / 182 | +6.4 % | +1.9 % | -6.3 % |
| cross-az | backup target copy | 73.3 / 135.0 | 74.9 / 136.5 | 214 / 210 | +2.1 % | +1.1 % | -1.8 % |
| cross-az | restore write (key absent) | 72.8 / 134.9 | 84.3 / 138.0 | 216 / 189 | +15.8 % | +2.3 % | -12.7 % |
| cross-az | restore write (key present) | 74.1 / 128.9 | 48.7 / 64.1 | 213 / 410 | -34.3 % | -50.3 % | +92.3 % |
| cross-az | audit checkpoint write | 14.9 / 29.0 | 15.1 / 29.1 | 1052 / 1037 | +1.3 % | +0.6 % | -1.4 % |
| cross-az | audit checkpoint check (read) | 4.2 / 8.6 | 143.0 / 339.8 | 3709 / 103 | +3291.0 % | +3849.3 % | -97.2 % |
| eu-metro | Pulumi checkpoint | 83.4 / 137.3 | 85.9 / 137.2 | 184 / 181 | +3.0 % | -0.1 % | -1.7 % |
| eu-metro | Pulumi journal batch | 69.4 / 116.7 | 71.0 / 120.5 | 219 / 214 | +2.4 % | +3.3 % | -2.3 % |
| eu-metro | job log chunk | 71.5 / 122.7 | 74.5 / 120.1 | 212 / 207 | +4.3 % | -2.1 % | -2.4 % |
| eu-metro | unit import upload | 84.4 / 142.3 | 106.5 / 148.4 | 180 / 146 | +26.2 % | +4.3 % | -18.8 % |
| eu-metro | backup target copy | 79.7 / 135.9 | 78.3 / 134.7 | 200 / 202 | -1.7 % | -0.9 % | +1.0 % |
| eu-metro | restore write (key absent) | 83.2 / 146.0 | 94.1 / 145.6 | 190 / 168 | +13.0 % | -0.3 % | -12.0 % |
| eu-metro | restore write (key present) | 81.0 / 136.2 | 56.8 / 70.0 | 197 / 345 | -29.9 % | -48.6 % | +75.0 % |
| eu-metro | audit checkpoint write | 16.5 / 27.6 | 16.8 / 29.1 | 937 / 918 | +2.0 % | +5.4 % | -2.1 % |
| eu-metro | audit checkpoint check (read) | 10.8 / 13.2 | 137.9 / 321.4 | 1478 / 106 | +1176.6 % | +2329.0 % | -92.8 % |

The LIST of Garage is slow under a concurrency of 16.
On a fresh container, the audit checkpoint check (LIST + GET) took 102 ms to 143 ms at p50.
A GET alone took about 4 ms.
An earlier run used a container whose bucket had about 180,000 objects that the run wrote and deleted.
There the check took 3,057 ms at p50 and 9,226 ms at p99.
That run is not in the table: its family ran again on a fresh container.

## Runs done again

- Garage, `eu-metro`: 4 runs had 1 error each, a 503 from Garage. They ran again 1 to 2 hours later. They are journal batch new run 3, unit import upload new runs 2 and 3, and restore write with an absent key new run 4. Their families have a gap of 1 to 2 hours between two runs.
- Garage, audit checkpoint check, all profiles: ran again on a fresh container, see above.
- MinIO, all families: ran again from the start. In the earlier runs MinIO answered 507 on part of the writes. MinIO empties its trash slower than the run deletes objects, and the disk filled up. The final run empties the MinIO trash every 10 seconds. The trash holds objects that MinIO deleted already.

## Run it again

```sh
make bench-s3-protocol
```

The target runs `scripts/bench-s3.sh` with `BENCH_S3_MODE=protocol` and writes `bench-s3-out/protocol.json` and `bench-s3-out/protocol.md`.
A run that stops keeps `protocol.json`.
The next run with the same `BENCH_S3_OUT` and the same plan skips the results without errors, and runs the other results again.
A run with another plan stops with an error.

| Variable | Default | Meaning |
| --- | --- | --- |
| `BENCH_S3_PROTOCOL_PROFILES` | `same-az cross-az eu-metro` | Profiles of the run, split by spaces |
| `BENCH_S3_ARGS` | empty | Extra flags of `cmd/s3bench`: `-ops`, `-runs`, `-concurrency`, `-stores`, `-families` |
| `BENCH_S3_MINIO_TMPFS` | empty | Size of a tmpfs for the MinIO data. Empty keeps the data on the disk |
