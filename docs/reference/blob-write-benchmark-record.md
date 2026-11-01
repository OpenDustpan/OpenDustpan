# Blob write benchmark record

This page is the benchmark record of the blob write design in which the database decides which write wins.
It compares two paths for each blob family:

- The old path: a PUT with `If-None-Match: *` under a fixed key. The store refuses a second write to the key.
- The new path: a PUT under a key that is unique for each attempt. The fenced database row decides which attempt wins.

The page gives no service level and makes no compliance claim.
The numbers hold for the stated host, stores and versions only.

## Result

Measured on MinIO, the store that enforces `If-None-Match`, at `same-az`, `cross-az` and `eu-metro`.
The first run is from 2026-10-30. The unit import, the restore copy and the audit families ran again on 2026-11-01. Before that run, the models changed to follow the code. The section "Run of 2026-11-01" has those numbers. The older tables of these families stay for the record.

| Family | Model follows | same-az | cross-az | eu-metro |
| --- | --- | --- | --- | --- |
| Pulumi checkpoint | No family code yet | meets | meets | meets |
| Pulumi journal batch | No family code yet | meets | meets (10 runs) | meets |
| Job log chunk | No family code yet | meets (10 runs) | meets | meets |
| Backup target copy | No family code yet | meets | meets | meets |
| Unit import upload | Main, with the unit file written for the first version only | meets (10 runs) | meets | meets |
| Restore copy, key absent | Main, with a fence check before the PUT of the live key | meets (10 runs) | meets (10 runs) | does not meet (10 runs) |
| Restore copy, key present | Main | meets | meets | meets |
| Audit checkpoint write | Main | does not meet (10 runs) | does not meet | does not meet |
| Audit checkpoint check | Main | meets (10 runs) | meets | meets |

- The hot paths (checkpoint, journal batch, job log chunk, backup copy) meet the measure at each profile. These families did not run again on 2026-11-01. Their models and their code did not change.
- The audit checkpoint write misses the measure at each profile: p50 +57 % at `same-az`, +58 % at `cross-az` and +30 % at `eu-metro`. The same write with one LIST of the scope, which no code has, is within 1.7 % at each profile.
- The restore copy with an absent key misses the measure at `eu-metro` only: p50 +8.6 %, throughput -8.0 %. The cost is the fence check, 1 database round trip.
- The unit import meets the measure at each profile when it writes the unit file for the first version of a unit only.
- The audit checkpoint check meets the measure at each profile. The first run measured a layout that the code does not use.

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
| Runs of each path | 5, in the order old, new, old, new. The three rows that missed on p99 only ran again with 10 runs |
| Client concurrency | 16 workers |
| Profiles | `same-az` (no added delay), `cross-az` (1 ms round trip), `eu-metro` (10 ms round trip, jitter of ±1 ms in each direction). The delay applies to the S3 path and to the database path |
| The measure | For each family, the median p50 and the median p99 of the new path are at most 5 % higher than on the old path. The median throughput is at most 5 % lower |
| Garage plan | MinIO ran each family at the 3 profiles. Garage ran the audit families and the candidates at `same-az` only, each on fresh containers. The LIST of Garage slows down after many deletes in a bucket, and a run of the audit write took hours. Garage gives no verdict, so the reduced plan changes no verdict |

[S3 latency benchmark](s3-latency-benchmark.md) gives the sources of the profiles and the limits of the method.

## The code that each model follows

Each model sends the requests of its code path in their order, against PostgreSQL model tables.
A part of the code that is the same on both paths and that sends no request stays out of the model.

| Family | Object size | Code | Old path | New path |
| --- | --- | --- | --- | --- |
| Pulumi checkpoint | 262,409 bytes | No family code yet. The model follows the decision: a PUT, then the fenced transaction of the row | PUT `If-None-Match` + fenced transaction (4 round trips) | PUT under an attempt key + fenced transaction |
| Pulumi journal batch | 8,856 bytes | No family code yet, as the checkpoint | Same as the checkpoint | Same as the checkpoint |
| Job log chunk | 16 KiB | No family code yet, as the checkpoint | Same as the checkpoint | Same as the checkpoint |
| Backup target copy | 262,409 bytes | No family code yet | PUT `If-None-Match` | PUT under an attempt key |
| Unit import upload | 262,409 bytes + 256 + 512 | `Source.Put` in `internal/unitimport/upload/upload.go` and `CommitUploadVersion` in `pkg/store/postgres/unitimport_upload.go`, the commit row of an upload | PUT body, PUT unit file (a refusal counts as present), PUT facts, all with `If-None-Match` and fixed keys, then one audited, fenced transaction (8 round trips) | The same PUTs under attempt keys, with the unit file for the first version of a unit only, then the same transaction. The old path writes the unit file for each version, and the store refuses all but the first |
| Restore copy, key absent | 262,409 bytes | `RestoreService.copyBlob` in `pkg/backup/restore_steps.go`. The fence check is not in main for an absent key at the time of the run. The model follows the decision, and a change adds the check | GET live (absent), DELETE temporary key, GET backup, PUT temporary key, GET temporary key, PUT live, DELETE temporary key. The PUTs send `If-None-Match` | The same, without the header, with a fence check before the PUT of the live key |
| Restore copy, key present | 262,409 bytes | `copyBlob` and `verifyLiveBlob`, main at 584e482 | GET live, compare size and hash, no write | The same |
| Audit checkpoint write | 1 KiB | `Checkpoints.Write` and `storeNew` in `pkg/audit/checkpoint.go` | LIST epoch, read marks, LIST scope, read last entry, LIST epoch, PUT `If-None-Match` | The same with a second LIST of the scope, and the PUT under `<sequence>-<attempt>` in the directory of the scope, the layout of `CheckpointKey` |
| Audit checkpoint check | 10 × 1 KiB | `Checkpoints.Verify` in `pkg/audit/checkpoint.go`. The code before the copies did the same requests | LIST scope, GET 10 planned checkpoints, read last entry | LIST scope, GET the copy of each of 10 planned checkpoints, read last entry |

The scope of the audit models holds 100 checkpoints.
The sizes of the checkpoint, the journal batch and the unit import body come from the load harness.
The other sizes are assumptions, because the load harness records none of them.

## MinIO results

The rows of the unit import, the restore copy and the audit families in this section are from the first run. Their models did not follow the code: the audit models used the layout `<sequence>/<attempt>`, and the code stores `<sequence>-<attempt>`. The section "Run of 2026-11-01" has the numbers that follow the code.

Values are the median of the 5 runs.
Δ is the new path against the old path.

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | Pulumi checkpoint | 26.4 / 63.5 | 26.7 / 61.4 | 565 / 562 | +1.2 % | -3.3 % | -0.6 % | meets |
| same-az | Pulumi journal batch | 15.0 / 39.6 | 15.6 / 39.8 | 973 / 948 | +3.9 % | +0.7 % | -2.6 % | meets |
| same-az | job log chunk | 15.3 / 38.8 | 14.2 / 40.8 | 900 / 986 | -6.9 % | +5.3 % | +9.6 % | does not meet |
| same-az | backup target copy | 22.8 / 56.5 | 22.3 / 53.5 | 649 / 674 | -2.0 % | -5.3 % | +3.9 % | meets |
| same-az | unit import upload (upload code) | 47.0 / 136.0 | 38.6 / 284.1 | 310 / 252 | -17.8 % | +108.9 % | -18.7 % | does not meet |
| same-az | restore copy, key absent (copyBlob) | 70.6 / 119.6 | 72.3 / 125.9 | 220 / 216 | +2.3 % | +5.2 % | -1.9 % | does not meet |
| same-az | restore copy, key present (copyBlob) | 7.8 / 23.6 | 8.1 / 24.5 | 1843 / 1802 | +2.9 % | +3.7 % | -2.2 % | meets |
| same-az | audit checkpoint write (Write) | 41.1 / 87.1 | 123.6 / 215.0 | 369 / 127 | +200.7 % | +146.9 % | -65.7 % | does not meet |
| same-az | audit checkpoint check (check) | 59.0 / 105.6 | 81.9 / 167.5 | 263 / 189 | +38.7 % | +58.6 % | -28.1 % | does not meet |
| cross-az | Pulumi checkpoint | 29.4 / 59.0 | 29.4 / 60.7 | 516 / 517 | -0.1 % | +2.9 % | +0.2 % | meets |
| cross-az | Pulumi journal batch | 16.0 / 29.2 | 16.1 / 31.8 | 956 / 925 | +0.8 % | +9.0 % | -3.2 % | does not meet |
| cross-az | job log chunk | 17.4 / 33.5 | 18.0 / 34.1 | 877 / 843 | +3.5 % | +1.8 % | -3.9 % | meets |
| cross-az | backup target copy | 22.5 / 51.4 | 22.9 / 50.4 | 670 / 666 | +1.5 % | -1.9 % | -0.6 % | meets |
| cross-az | unit import upload (upload code) | 170.8 / 206.5 | 168.8 / 206.6 | 93 / 94 | -1.2 % | +0.0 % | +0.8 % | meets |
| cross-az | restore copy, key absent (copyBlob) | 72.2 / 119.0 | 74.9 / 148.7 | 215 / 205 | +3.7 % | +24.9 % | -4.6 % | does not meet |
| cross-az | restore copy, key present (copyBlob) | 8.1 / 21.4 | 8.0 / 21.3 | 1821 / 1825 | -1.7 % | -0.6 % | +0.2 % | meets |
| cross-az | audit checkpoint write (Write) | 44.5 / 85.9 | 129.4 / 215.6 | 342 / 121 | +191.0 % | +151.1 % | -64.5 % | does not meet |
| cross-az | audit checkpoint check (check) | 64.1 / 101.7 | 88.0 / 152.8 | 243 / 179 | +37.2 % | +50.3 % | -26.6 % | does not meet |
| eu-metro | Pulumi checkpoint | 57.4 / 74.0 | 57.3 / 72.2 | 275 / 275 | -0.1 % | -2.4 % | +0.2 % | meets |
| eu-metro | Pulumi journal batch | 54.0 / 60.8 | 53.9 / 61.7 | 295 / 295 | -0.2 % | +1.5 % | +0.1 % | meets |
| eu-metro | job log chunk | 54.0 / 62.2 | 54.0 / 61.9 | 295 / 295 | +0.1 % | -0.4 % | +0.0 % | meets |
| eu-metro | backup target copy | 23.0 / 44.5 | 22.6 / 46.2 | 660 / 663 | -1.7 % | +3.8 % | +0.5 % | meets |
| eu-metro | unit import upload (upload code) | 1004.2 / 1036.7 | 1005.9 / 1036.3 | 16 / 16 | +0.2 % | -0.0 % | -0.2 % | meets |
| eu-metro | restore copy, key absent (copyBlob) | 96.5 / 126.5 | 105.7 / 135.5 | 163 / 149 | +9.5 % | +7.1 % | -8.5 % | does not meet |
| eu-metro | restore copy, key present (copyBlob) | 12.7 / 20.3 | 12.6 / 19.7 | 1211 / 1227 | -0.7 % | -3.0 % | +1.3 % | meets |
| eu-metro | audit checkpoint write (Write) | 74.3 / 94.0 | 139.7 / 200.2 | 212 / 113 | +88.1 % | +113.0 % | -47.0 % | does not meet |
| eu-metro | audit checkpoint check (check) | 136.2 / 152.4 | 142.8 / 167.8 | 117 / 111 | +4.8 % | +10.1 % | -4.9 % | does not meet |

### The three rows that missed on p99 only, with 10 runs

In these families the two paths send the same requests, except the header and the key name.
With 10 runs of each path, all three meet the measure.

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | job log chunk | 16.6 / 41.2 | 15.8 / 40.3 | 903 / 944 | -4.8 % | -2.2 % | +4.5 % | meets |
| cross-az | Pulumi journal batch | 16.7 / 32.0 | 16.1 / 29.1 | 905 / 951 | -3.6 % | -9.0 % | +5.1 % | meets |
| same-az | audit checkpoint write (first model) | 10.1 / 28.9 | 10.1 / 27.2 | 1427 / 1420 | +0.4 % | -6.1 % | -0.5 % | meets |

| Row | p99 of the old runs (ms) | p99 of the new runs (ms) |
| --- | --- | --- |
| Pulumi journal batch, `cross-az` | 27.9 to 39.5 | 26.2 to 32.5 |
| Job log chunk, `same-az` | 35.1 to 47.5 | 38.9 to 45.6 |
| Audit checkpoint write (first model), `same-az` | 23.9 to 33.9 | 20.7 to 33.4 |

## Run of 2026-11-01

The models of the audit write and the audit check now use the layout of the code: `<sequence>-<attempt>` in the directory of the scope. The unit import model writes the unit file for the first version of a unit only.
The first pass ran 5 runs of each path on MinIO at the three profiles. It overlapped builds and tests on the same host. So some rows ran again on an idle host with 10 runs. These are the unit import and the restore copy at `same-az`. They are also the restore copy at `cross-az` and `eu-metro`, and the audit rows at `same-az`. Two runs of the 10-run pass answered 507 from MinIO. They ran again.
Values are the median of the runs. Δ is the new path against the old path.

| Profile | Family | Runs | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | unit import upload | 10 | 54.4 / 137.5 | 54.6 / 88.9 | 271 / 291 | +0.4 % | -35.3 % | +7.5 % | meets |
| same-az | restore copy, key absent | 10 | 75.5 / 133.1 | 78.2 / 134.5 | 206 / 199 | +3.5 % | +1.1 % | -3.6 % | meets |
| same-az | audit checkpoint write | 10 | 42.8 / 91.9 | 67.3 / 139.8 | 354 / 228 | +57.2 % | +52.1 % | -35.7 % | does not meet |
| same-az | audit checkpoint check | 10 | 60.2 / 109.1 | 60.8 / 108.7 | 258 / 256 | +1.0 % | -0.4 % | -1.1 % | meets |
| same-az | audit checkpoint write, one LIST (candidate) | 10 | 42.6 / 91.9 | 42.5 / 90.7 | 356 / 358 | -0.3 % | -1.3 % | +0.5 % | no verdict |
| cross-az | unit import upload | 5 | 187.4 / 240.0 | 180.2 / 222.8 | 85 / 88 | -3.8 % | -7.2 % | +4.2 % | meets |
| cross-az | restore copy, key absent | 10 | 73.3 / 121.5 | 76.3 / 126.4 | 214 / 204 | +4.1 % | +4.1 % | -4.4 % | meets |
| cross-az | audit checkpoint write | 5 | 47.6 / 93.6 | 75.3 / 144.6 | 320 / 204 | +58.3 % | +54.4 % | -36.2 % | does not meet |
| cross-az | audit checkpoint check | 5 | 66.0 / 108.4 | 66.6 / 108.3 | 235 / 233 | +0.9 % | -0.0 % | -0.8 % | meets |
| cross-az | audit checkpoint write, one LIST (candidate) | 5 | 46.8 / 90.5 | 47.5 / 92.1 | 326 / 321 | +1.6 % | +1.7 % | -1.5 % | no verdict |
| eu-metro | unit import upload | 5 | 1043.5 / 1119.7 | 1033.2 / 1095.5 | 15 / 15 | -1.0 % | -2.2 % | +0.8 % | meets |
| eu-metro | restore copy, key absent | 10 | 99.1 / 136.8 | 107.6 / 141.2 | 159 / 147 | +8.6 % | +3.2 % | -8.0 % | does not meet |
| eu-metro | audit checkpoint write | 5 | 76.9 / 103.0 | 100.2 / 139.5 | 205 / 156 | +30.2 % | +35.4 % | -23.7 % | does not meet |
| eu-metro | audit checkpoint check | 5 | 138.7 / 159.9 | 138.4 / 162.2 | 115 / 115 | -0.2 % | +1.5 % | +0.1 % | meets |
| eu-metro | audit checkpoint write, one LIST (candidate) | 5 | 75.2 / 96.1 | 75.3 / 97.8 | 210 / 209 | +0.1 % | +1.7 % | -0.3 % | no verdict |

- The restore copy at `eu-metro` has a tight spread (new p50 105.6 to 108.3 ms, old p50 97.8 to 100.6 ms). The miss is not noise. It is the fence check: 1 database round trip, which is 10 ms at this profile.
- The audit checkpoint write costs one more LIST of the scope on the new path (`storeNew` lists the copies again before it writes). That extra LIST alone explains the miss. With the layout of the code and one LIST, the write is within 1.7 % at each profile.
- The audit checkpoint check meets the measure at each profile. The layout `<sequence>-<attempt>` lists as fast as one key for each sequence on MinIO.

## Families that do not meet the measure

| Family | Cause | Path kind | One option to reduce it |
| --- | --- | --- | --- |
| Audit checkpoint write | The new path lists the scope a second time in `storeNew`, before it writes | Background: each checkpoint of the audit chain | Drop the second LIST and keep a bound on the copies of one sequence in another way. See the candidates |
| Restore copy, key absent, `eu-metro` | One fence check, 1 database round trip, before the PUT of the live key. It keeps a holder that lost its claim from a write of the live key | Background, in maintenance mode | Keep the check. The restore copies up to 8 blobs at the same time. So the time of the whole restore grows less than the time of one blob |

## Candidates

A candidate is a model of a design that no code has.
It gets no verdict.
The rows are in the section "Run of 2026-11-01".

- The audit checkpoint write with one LIST of the scope is within 1.7 % of the old path. This holds at each profile (p50 -0.3 %, +1.6 %, +0.1 %).
- `storeNew` lists the copies of the sequence again just before it writes. When a copy exists, it writes nothing. So the count of copies of one sequence is at most the count of writers that list it at the same moment. Without that second LIST, a writer decides on the first LIST, before the read of the last entry. Then more writers can store a copy of one sequence. A change that takes the LIST away must keep a bound on the copies in another way. The check reads at most 16 copies of one sequence, and more than 16 are a mismatch.

## Properties of the code outside the verdict

- **Unit import commits of one namespace queue up.**
  The audited, fenced commit locks the namespace row for its whole transaction, about 7 round trips. At `eu-metro` that is about 63 ms, so one namespace commits at most about 16 versions each second, on both paths. The blob design does not change this.
- **A conditional PUT above 256 KiB on a present key closes the connection on MinIO.**
  MinIO refuses the write before it reads the body.
  So the Go server closes the connection. Each refused write then needs a new connection. In the first model of the restore, 3 runs of 10,000 such writes used up the 28,232 ports of the host.
Then 1,770 writes failed. The restore code of main reads the live key first and never sends such a write.

## First models

The first protocol run used models that did not follow the code.
They stay here for the record, and they have no verdict.
The audit check model sent a LIST for each position, but the code lists once for each check.
The restore models sent a conditional PUT to a present key, but the code reads the key first.
The unit import model added a fenced transaction that the code does not have.

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | unit import upload | 27.2 / 84.5 | 31.5 / 93.1 | 531 / 463 | +15.8 % | +10.1 % | -12.8 % |
| same-az | restore write (key absent) | 23.1 / 54.9 | 27.8 / 62.2 | 650 / 546 | +20.2 % | +13.4 % | -16.1 % |
| same-az | restore write (key present) | 20.9 / 31.5 | 7.7 / 22.0 | 756 / 1908 | -63.4 % | -30.2 % | +152.3 % |
| same-az | audit checkpoint write | 9.2 / 22.5 | 9.4 / 24.3 | 1617 / 1603 | +1.9 % | +8.1 % | -0.9 % |
| same-az | audit checkpoint check (read) | 3.1 / 11.0 | 7.1 / 19.6 | 4415 / 2094 | +129.3 % | +79.0 % | -52.6 % |
| cross-az | unit import upload | 28.6 / 62.6 | 33.8 / 62.0 | 527 / 453 | +18.2 % | -1.0 % | -14.0 % |
| cross-az | restore write (key absent) | 22.3 / 50.8 | 25.8 / 53.1 | 678 / 591 | +15.5 % | +4.6 % | -12.8 % |
| cross-az | restore write (key present) | 22.7 / 32.9 | 8.1 / 20.1 | 696 / 1828 | -64.4 % | -38.9 % | +162.6 % |
| cross-az | audit checkpoint write | 10.3 / 24.0 | 10.1 / 22.5 | 1462 / 1514 | -2.0 % | -6.5 % | +3.6 % |
| cross-az | audit checkpoint check (read) | 3.5 / 8.8 | 7.8 / 16.4 | 4288 / 1948 | +125.2 % | +86.4 % | -54.6 % |
| eu-metro | unit import upload | 57.3 / 72.3 | 98.1 / 118.4 | 276 / 162 | +71.3 % | +63.7 % | -41.3 % |
| eu-metro | restore write (key absent) | 22.7 / 43.0 | 39.7 / 55.7 | 662 / 394 | +74.4 % | +29.4 % | -40.4 % |
| eu-metro | restore write (key present) | 21.8 / 32.6 | 12.6 / 19.2 | 724 / 1241 | -42.2 % | -41.1 % | +71.5 % |
| eu-metro | audit checkpoint write | 13.4 / 19.8 | 13.2 / 19.4 | 1167 / 1187 | -1.2 % | -1.9 % | +1.6 % |
| eu-metro | audit checkpoint check (read) | 10.8 / 13.4 | 21.7 / 26.1 | 1471 / 733 | +100.9 % | +94.2 % | -50.2 % |

## Garage results

Garage ignores `If-None-Match`, so the old path writes again where MinIO refuses.
These rows have no verdict.

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | Pulumi checkpoint | 72.5 / 133.6 | 76.9 / 141.3 | 216 / 204 | +6.1 % | +5.8 % | -5.6 % |
| same-az | Pulumi journal batch | 66.0 / 123.9 | 58.8 / 110.3 | 237 / 266 | -10.9 % | -11.0 % | +12.2 % |
| same-az | job log chunk | 71.3 / 129.1 | 70.8 / 131.6 | 221 / 221 | -0.7 % | +1.9 % | +0.0 % |
| same-az | backup target copy | 75.8 / 140.8 | 77.0 / 137.5 | 207 / 204 | +1.6 % | -2.4 % | -1.0 % |
| same-az | unit import upload (upload code) | 118.7 / 183.9 | 118.2 / 181.9 | 133 / 133 | -0.4 % | -1.1 % | +0.2 % |
| same-az | restore copy, key absent (copyBlob) | 257.9 / 358.3 | 265.4 / 361.5 | 62 / 60 | +2.9 % | +0.9 % | -3.2 % |
| same-az | restore copy, key present (copyBlob) | 48.0 / 63.8 | 48.0 / 63.9 | 431 / 433 | -0.0 % | +0.1 % | +0.5 % |
| same-az | audit checkpoint write (Write) | 209.8 / 318.0 | 298.2 / 453.2 | 75 / 53 | +42.1 % | +42.5 % | -30.1 % |
| same-az | audit checkpoint check (check) | 95.6 / 125.8 | 115.4 / 150.9 | 167 / 138 | +20.8 % | +19.9 % | -17.2 % |
| cross-az | Pulumi checkpoint | 73.4 / 132.7 | 77.2 / 134.8 | 213 / 206 | +5.2 % | +1.6 % | -3.3 % |
| cross-az | Pulumi journal batch | 68.8 / 121.5 | 68.2 / 121.3 | 230 / 231 | -0.9 % | -0.2 % | +0.4 % |
| cross-az | job log chunk | 72.5 / 127.4 | 71.5 / 127.8 | 217 / 218 | -1.4 % | +0.3 % | +0.5 % |
| cross-az | backup target copy | 73.3 / 135.0 | 74.9 / 136.5 | 214 / 210 | +2.1 % | +1.1 % | -1.8 % |
| cross-az | unit import upload (upload code) | 186.0 / 221.5 | 185.6 / 220.5 | 85 / 86 | -0.2 % | -0.4 % | +0.2 % |
| cross-az | restore copy, key absent (copyBlob) | 267.2 / 364.6 | 274.3 / 392.3 | 59 / 58 | +2.7 % | +7.6 % | -2.7 % |
| cross-az | restore copy, key present (copyBlob) | 49.0 / 64.5 | 48.8 / 64.3 | 414 / 415 | -0.5 % | -0.3 % | +0.1 % |
| eu-metro | Pulumi checkpoint | 83.4 / 137.3 | 85.9 / 137.2 | 184 / 181 | +3.0 % | -0.1 % | -1.7 % |
| eu-metro | Pulumi journal batch | 69.4 / 116.7 | 71.0 / 120.5 | 219 / 214 | +2.4 % | +3.3 % | -2.3 % |
| eu-metro | job log chunk | 71.5 / 122.7 | 74.5 / 120.1 | 212 / 207 | +4.3 % | -2.1 % | -2.4 % |
| eu-metro | backup target copy | 79.7 / 135.9 | 78.3 / 134.7 | 200 / 202 | -1.7 % | -0.9 % | +1.0 % |
| eu-metro | unit import upload (upload code) | 1004.7 / 1067.5 | 1003.6 / 1065.6 | 16 / 16 | -0.1 % | -0.2 % | +0.3 % |
| eu-metro | restore copy, key absent (copyBlob) | 268.1 / 362.8 | 277.6 / 383.7 | 59 / 57 | +3.6 % | +5.8 % | -3.8 % |
| eu-metro | restore copy, key present (copyBlob) | 56.6 / 68.4 | 56.6 / 68.6 | 346 / 347 | -0.1 % | +0.4 % | +0.3 % |

Garage candidates, at `same-az` only:

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | audit checkpoint check, flat layout (candidate) | 95.0 / 125.2 | 139.1 / 179.9 | 167 / 115 | +46.4 % | +43.8 % | -31.5 % |
| same-az | audit checkpoint write, flat layout (candidate) | 214.6 / 332.1 | 337.1 / 538.9 | 74 / 46 | +57.1 % | +62.2 % | -37.1 % |
| same-az | audit checkpoint write, one LIST (candidate) | 207.8 / 324.3 | 228.5 / 356.5 | 76 / 69 | +10.0 % | +9.9 % | -9.3 % |

On Garage the flat layout does not help: the check is +46 % and the write is +57 %.
On Garage the write with one LIST is +10 %.

Garage, after the models follow the code, at `same-az` only, 5 runs, on fresh containers:

| Profile | Family | Old p50 / p99 (ms) | New p50 / p99 (ms) | Old / new ops/s | Δ p50 | Δ p99 | Δ ops/s |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | audit checkpoint write | 178.2 / 277.6 | 268.4 / 412.5 | 88 / 59 | +50.7 % | +48.6 % | -33.5 % |
| same-az | audit checkpoint check | 99.7 / 138.0 | 121.8 / 164.7 | 159 / 130 | +22.1 % | +19.3 % | -17.8 % |
| same-az | audit checkpoint write, one LIST (candidate) | 178.3 / 267.0 | 199.6 / 313.3 | 88 / 79 | +11.9 % | +17.3 % | -11.1 % |

On Garage the layout of the code costs more than on MinIO for the check (+22 %). The write with one LIST is +12 %. Garage lists slower when a bucket holds many deleted objects. So these rows depend on the state of the bucket.

## Runs done again

- MinIO, all families of the first run: ran again from the start. MinIO answered 507 on part of the writes, because it empties its trash slower than the run deletes objects. The final runs empty the MinIO trash every 10 seconds. The trash holds objects that MinIO deleted already.
- MinIO, restore copy with an absent key, `eu-metro`, old run 2: 1,731 writes answered 507. It ran again 30 minutes later.
- MinIO, restore write with a present key (first model): ran again with a wait of 65 seconds after each run. In that time the closed connections leave TIME_WAIT.
- Garage, `eu-metro`, first models: 4 runs had 1 error each, a 503 from Garage. They ran again 1 to 2 hours later.
- Garage, audit families and candidates: each ran on fresh containers. On a container whose bucket held about 180,000 deleted objects, the LIST of the check took 3,057 ms at p50.

- MinIO, 2026-11-01: the first pass of the unit import, the restore copy and the audit families overlapped builds and tests on the same host. The rows with wide spread ran again on an idle host with 10 runs.
- MinIO, `same-az`, 10-run pass of 2026-11-01: two runs answered 507 on part of the writes. These were new run 9 of the unit import and new run 10 of the restore copy. They ran again. The second runs have no error.

No result in the tables has an error.

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
