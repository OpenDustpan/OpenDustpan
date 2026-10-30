# S3 latency benchmark

The S3 latency benchmark measures the blob writes of OpenDustpan on real S3-compatible stores, at the network distances of real deployments.
It compares two designs of a create-only blob write:

- The conditional design: a PUT with `If-None-Match: *`. The store refuses a second write to the same key. This is the code today.
- The database-arbitrated design: a plain PUT under a key that is unique for each attempt. The fenced database row decides which write wins.

The page gives no service level and makes no compliance claim.
The numbers hold for the stated host and images only.

## Run it

Docker must run.

```sh
make bench-s3
```

`scripts/bench-s3.sh` starts four containers on a private Docker network.
It publishes their ports on 127.0.0.1 only.
The Toxiproxy API has no authentication. The script publishes it on 127.0.0.1 only, so only a process on the host can change the proxies.
It writes throwaway credentials to a temporary directory with mode 0700 and deletes the directory, the containers and the network at the end.
The results go to `bench-s3-out/`: `results.json`, `results.md`, `images.txt`, and one report and one log of the load harness for each step in `load/`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `BENCH_S3_ARGS` | empty | Extra flags of `cmd/s3bench`, for example `-quick`, `-profiles eu-metro` or `-limits` |
| `BENCH_S3_OUT` | `bench-s3-out` | Output directory |
| `BENCH_S3_NAME` | `odp-bench-s3` | Name prefix of the containers and the network |
| `BENCH_S3_PORT` | `18474` | First host port. The run uses it and the next 3 ports |

A full run with `-limits` took about 5 hours on the host below.
A run with `-quick` makes fewer repetitions. This page gives no measured time for it.

## Images

Each image is pinned by digest in `scripts/bench-s3.sh`.

| Image | Digest | Role |
| --- | --- | --- |
| `cgr.dev/chainguard/minio` | `sha256:e70b2b1e74d79457f808710483bd726ff364529ca30736eaa15a6ab547d3a5a7` | S3 store that enforces `If-None-Match`. The MinIO project no longer publishes images to Docker Hub or Quay |
| `dxflrs/garage` (v2.1.0) | `sha256:4c9b34c113e61358466e83fd6e7d66e6d18657ede14b776eb78a93ee8da7cf6a` | S3 store that ignores `If-None-Match` |
| `postgres` (18) | `sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6` | Database of the load harness and of the model tables |
| `ghcr.io/shopify/toxiproxy` (2.12.0) | `sha256:9378ed52a28bc50edc1350f936f518f31fa95f0d15917d6eb40b8e376d1a214e` | Adds the delay of each profile |

## What it measures

Toxiproxy sits between the benchmark and each store, and between the benchmark and PostgreSQL.
Each profile sets the same delay on the S3 path and on the database path.
This is the case that costs the database-arbitrated design the most, because that design can add database round trips.

| Part | What runs | Code or model |
| --- | --- | --- |
| Blob PUT | 10 KiB, 1 MiB and 100 MiB in three modes. The modes take turns, one write each. See the list below | Adapter: code. Raw plain PUT: model of the new write |
| Modeled sequences | The requests of each design in their order, against PostgreSQL model tables. One table holds a fence row. One table holds a row that names each blob key | Model |
| Orphan sweep | 1,000 objects under one unit directory. The timed part is one LIST, one read of the keep set, and one delete request for each orphan. The deletes run one after the other, as in the stack and workspace services | Adapter calls: code. Keep-set read: model |
| CLI flows | `go run ./load -flows blobs` on S3 and PostgreSQL through the proxies. See the list below | Code |
| Limit sweep (`-limits`) | The CLI flows on MinIO at each step. First the S3 round trip rises with the database at loopback. Then the database round trip rises with S3 at loopback | Code |

The three PUT modes:

- The `blob/s3` adapter `Put`. It stages the body in a file, hashes it and sends `If-None-Match: *`.
- One signed raw PUT with the header.
- One signed raw PUT without the header, under a unique key.

A fenced transaction of the model is 4 round trips.
They are the transaction start, a read of the fence row with a share lock, the insert, and the commit.

The CLI flows are the OpenTofu state upload and the Pulumi checkpoint save.
They also include a backup and a restore of a fixed data set through the server API.
MinIO opens with the conditional-write probe, as the server binary does.
Garage opens without the probe.
The server binary refuses a store that ignores `If-None-Match`, so Garage runs only without the probe.

The three modeled flows follow the request sequences of the database-arbitrated design:

| Flow | Conditional design | Database-arbitrated design |
| --- | --- | --- |
| Pulumi checkpoints, journal batches, job log chunks | PUT `If-None-Match` + fenced transaction | PUT under a unique key + fenced transaction + 1 row read (the worst case of 0 or 1 extra round trip) |
| Unit import upload | PUT `If-None-Match` + fenced transaction | Fenced transaction that claims the upload + PUT under a unique key + fenced transaction |
| Backup target, restore, audit checkpoints | PUT `If-None-Match` | LIST of the directory (10 objects) + PUT under a unique key |

## Profiles

Toxiproxy adds half of the round trip time (RTT) on the request direction and half on the answer direction.
Its latency toxic takes whole milliseconds, and it adds a uniform jitter in [-j, +j] on each direction.
A bound j on each direction gives a standard deviation of j × √(2/3) over the round trip.

| Profile | Added RTT (ms) | Jitter bound j (ms) | Measured values it stands for |
| --- | --- | --- | --- |
| `same-az` | 0 | 0 | 0.05 to 0.3 ms RTT, standard deviation below 0.1 ms. Below the 1 ms step of Toxiproxy, so the run measures the loopback path through the proxy |
| `cross-az` | 1 | 0 | 0.5 to 1.7 ms RTT, p99 − p50 of 0.1 to 0.3 ms. The jitter is below the 1 ms step |
| `eu-metro` | 10 | 1 | Frankfurt, Amsterdam and Paris: 6 to 8 ms on an ISP backbone, 10 to 13 ms between cloud regions, standard deviation about 1 ms |
| `continent` | 65 | 4 | US East to US West 56 to 73 ms, Singapore to Tokyo 68 to 72 ms, standard deviation about 3 ms |
| `intercontinental` | 150 | 6 | Virginia to Tokyo 145 to 162 ms, Frankfurt to Singapore 153 to 167 ms, standard deviation about 5 ms |

Some shorter pairs outside Europe fit `eu-metro`.
Examples are Ashburn to New York (6 ms), Tokyo to Osaka (10 to 12 ms) and Virginia to Ohio (13 ms).
Frankfurt to Virginia is 81 to 94 ms and Frankfurt to Sydney is 255 to 301 ms.
The limit sweep covers these values.

### Sources

"Fetched" means that the page was opened on 2026-10-27 and the number was on it.
"Not fetched" means a search snippet or a second-hand source only.

| Source | What it measures | Date of the data | Status |
| --- | --- | --- | --- |
| [Azure network round-trip latency statistics](https://learn.microsoft.com/en-us/azure/networking/azure-network-latency) | P50 between Azure regions over 30 days | to 2026-07-30 | fetched |
| [What are Azure Availability Zones?](https://learn.microsoft.com/en-us/azure/reliability/availability-zones-overview) | Design target: RTT between zones below about 2 ms | 2026-02-11 | fetched |
| [cloudping.co](https://www.cloudping.co/) and its JSON API | p50 and p99 between AWS regions over TCP from Lambda functions, 1 week and 1 month windows. The values include Lambda and TCP overhead | to 2026-10-27 | fetched (the method page: not fetched) |
| [AWS Architecture Blog, Availability Zone affinity](https://aws.amazon.com/blogs/architecture/improving-performance-and-reducing-cost-using-availability-zone-affinity/) | AWS planning values: p50 0.3 ms in one AZ, 1.5 ms across AZs | 2021, updated 2025-04 | fetched |
| [Classmethod, Tokyo and Osaka AZ latency](https://dev.classmethod.jp/articles/tokyo-osaka-az-latency/) | sockperf p50, p99 and p99.9 for AWS AZ pairs in Tokyo and Osaka | 2026-07-13 | fetched |
| [Zenn, latency between AWS AZs](https://zenn.dev/tsumita7/articles/aws-mesuring-latency-among-az-2023) | netperf TCP_RR mean and standard deviation for AWS Tokyo and Osaka | 2023-03-11 | fetched |
| [Google Cloud Blog, measuring network latency in the cloud](https://cloud.google.com/blog/products/networking/using-netperf-and-ping-to-measure-network-latency) | ping and netperf in one GCP zone | 2020-06-17 | fetched |
| [Inter-AZ cloud network performance](https://maisonbisson.com/post/inter-az-cloud-network-performance/), quoting the ThousandEyes Cloud Performance Report 2018 | Mean RTT across AZs: AWS 0.82 ms, GCP 0.79 ms, Azure 1.05 ms | 2018 | fetched (the report itself: not fetched) |
| ThousandEyes report 2019 | Mean RTT across AZs: GCP 0.52 ms, Azure 0.72 ms, AWS 0.81 ms | 2019 | not fetched |
| [Hats Network latency pairs](https://hatsnet.io/docs/network/latency/pairs) | ISP backbone, 50 ICMP probes for each pair: mean, standard deviation and jitter | 2026-10-20 | fetched |
| [Verizon IP latency statistics](https://www.verizon.com/business/terms/latency/) | Monthly mean between Verizon backbone hubs | 2026-09 | fetched |

No source gave measured inter-region values for GCP.
No source gave measured p50 and p99 across AZs in AWS us-east-1 or eu-central-1, Azure West Europe or GCP europe-west.

## Results

Measured on 2026-10-27 and 2026-10-28.
The host had an Intel Xeon processor at 2.80 GHz with 4 CPUs, 16 GB of memory, Linux on amd64 and Docker 29.9.2.
A restart of the host stopped the first run after 4 profiles.
A second run on the same host measured the `intercontinental` profile and the limit sweep.

Times are in milliseconds, as p50 / p99.

### If-None-Match on a second write

| Store | First PUT | Second PUT with the same key | Enforced |
| --- | --- | --- | --- |
| MinIO | 200 | 412 | yes |
| Garage | 200 | 200 | no |

### Blob PUT, raw signed requests

The table compares the PUT with `If-None-Match: *` and the plain PUT under a unique key.
The two modes took turns, one write each.

| Profile | Store | 10 KiB, conditional | 10 KiB, plain | 1 MiB, conditional | 1 MiB, plain | 100 MiB, conditional | 100 MiB, plain |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | MinIO | 5.2 / 8.6 | 5.0 / 8.7 | 15.9 / 46.1 | 16.2 / 27.6 | 849 / 1193 | 958 / 1212 |
| same-az | Garage | 5.9 / 11.4 | 6.1 / 11.7 | 19.6 / 61.0 | 18.7 / 49.7 | 1360 / 1480 | 1376 / 1461 |
| cross-az | MinIO | 5.7 / 10.9 | 5.6 / 8.3 | 25.8 / 79.6 | 25.1 / 85.1 | 1013 / 1123 | 1083 / 1133 |
| cross-az | Garage | 7.3 / 10.5 | 7.2 / 11.0 | 22.4 / 69.7 | 21.7 / 64.1 | 1457 / 1497 | 1464 / 1566 |
| eu-metro | MinIO | 15.0 / 17.0 | 15.1 / 17.2 | 26.5 / 104.8 | 26.3 / 31.4 | 828 / 923 | 820 / 958 |
| eu-metro | Garage | 16.2 / 20.0 | 16.3 / 23.2 | 29.1 / 86.9 | 29.4 / 44.0 | 1461 / 1593 | 1414 / 1513 |
| continent | MinIO | 70.2 / 77.9 | 70.6 / 78.3 | 84.6 / 96.6 | 83.4 / 91.7 | 917 / 994 | 950 / 1041 |
| continent | Garage | 71.6 / 79.1 | 71.7 / 79.5 | 87.0 / 94.7 | 86.0 / 95.6 | 1364 / 1483 | 1403 / 1515 |
| intercontinental | MinIO | 154.8 / 185.7 | 154.1 / 166.0 | 174.3 / 186.1 | 176.9 / 183.7 | 896 / 1090 | 1079 / 1166 |
| intercontinental | Garage | 156.6 / 171.2 | 155.9 / 166.3 | 172.5 / 183.0 | 172.0 / 185.0 | 1420 / 1497 | 1500 / 1519 |

The samples were 100 for 10 KiB, 50 for 1 MiB and 8 for 100 MiB.
The 100 MiB rows have 8 samples only. Their differences between the two modes are within the noise of the run, in both directions.
The `blob/s3` adapter adds its local staging and hash to each write: about 5 ms for 1 MiB and 400 to 1,000 ms for 100 MiB.
`results.md` of a run holds the adapter column.

### Modeled write sequences

Both columns are models.
They send the real requests of each design in order, against the model tables.
The table shows MinIO. `results.md` of a run holds the Garage rows.
For the two flows without a LIST, the Garage p50 values are within 10 ms of MinIO.
In the LIST flow, the added time on Garage was up to 22 ms more than on MinIO.

| Profile | Flow | Conditional | Database-arbitrated | Added p50 |
| --- | --- | --- | --- | --- |
| same-az | Checkpoint, journal batch, job log chunk | 12.9 / 21.0 | 13.4 / 18.9 | +0.5 |
| same-az | Unit import upload | 13.6 / 25.7 | 16.5 / 25.9 | +2.9 |
| same-az | Backup target, restore, audit checkpoint | 10.1 / 46.7 | 12.8 / 22.0 | +2.7 |
| cross-az | Checkpoint, journal batch, job log chunk | 22.4 / 30.6 | 24.2 / 45.6 | +1.8 |
| cross-az | Unit import upload | 23.3 / 28.8 | 31.4 / 61.8 | +8.1 |
| cross-az | Backup target, restore, audit checkpoint | 12.9 / 22.6 | 17.2 / 24.2 | +4.3 |
| eu-metro | Checkpoint, journal batch, job log chunk | 66.1 / 181.8 | 77.6 / 85.5 | +11.5 |
| eu-metro | Unit import upload | 65.8 / 87.9 | 112.1 / 125.4 | +46.2 |
| eu-metro | Backup target, restore, audit checkpoint | 21.8 / 25.8 | 34.6 / 64.2 | +12.8 |
| continent | Checkpoint, journal batch, job log chunk | 342.8 / 362.3 | 410.2 / 422.3 | +67.4 |
| continent | Unit import upload | 343.1 / 360.2 | 612.5 / 1466.0 | +269.4 |
| continent | Backup target, restore, audit checkpoint | 78.1 / 169.5 | 147.8 / 187.6 | +69.6 |
| intercontinental | Checkpoint, journal batch, job log chunk | 770.4 / 786.5 | 922.9 / 950.4 | +152.6 |
| intercontinental | Unit import upload | 769.7 / 832.1 | 1384.5 / 1496.7 | +614.8 |
| intercontinental | Backup target, restore, audit checkpoint | 161.2 / 172.2 | 314.7 / 327.8 | +153.5 |

The added time is the round trips that the design adds.
The row read adds 1 database round trip.
The claim transaction adds 4 database round trips.
The LIST adds 1 S3 round trip.

### CLI flows through the load harness

The data set of the backup and the restore was 20 workspaces and 20 stacks.
Each workspace held a state of 64 KiB. Each stack held a checkpoint of 256 KiB.
Each flow had 20 timed attempts, with no failed attempt.
MinIO ran the server code of today, with the conditional-write probe.
Garage ran the same code without the probe.

| Profile | Store | OpenTofu state upload | Lock, upload, unlock | Pulumi checkpoint save | Update cycle | Backup | Restore |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same-az | MinIO | 48 / 70 | 87 / 117 | 28 / 31 | 62 / 97 | 2,043 | 4,902 |
| same-az | Garage | 46 / 66 | 88 / 114 | 26 / 35 | 55 / 69 | 1,636 | 5,308 |
| cross-az | MinIO | 115 / 208 | 295 / 482 | 46 / 55 | 129 / 224 | 2,684 | 9,688 |
| cross-az | Garage | 107 / 146 | 228 / 287 | 46 / 56 | 124 / 142 | 3,903 | 10,714 |
| eu-metro | MinIO | 534 / 690 | 1,164 / 1,358 | 183 / 348 | 642 / 876 | 8,261 | 34,022 |
| eu-metro | Garage | 542 / 588 | 1,176 / 1,300 | 180 / 197 | 580 / 610 | 8,463 | 33,555 |
| continent | MinIO | 3,213 / 3,627 | 7,080 / 7,411 | 1,143 / 1,222 | 3,492 / 3,672 | 42,683 | 181,735 |
| continent | Garage | 3,222 / 3,660 | 7,082 / 7,427 | 1,146 / 1,220 | 3,480 / 3,562 | 41,843 | 177,202 |
| intercontinental | MinIO | 7,615 / 8,487 | 16,442 / 17,287 | 2,607 / 2,744 | 8,538 / 8,827 | 94,838 | 413,497 |
| intercontinental | Garage | 7,628 / 8,568 | 16,436 / 17,341 | 2,598 / 2,627 | 8,521 / 8,815 | 95,097 | 399,137 |

### Orphan sweep of 1,000 objects

| Profile | Store | LIST | Keep-set read | 1,000 deletes | Total |
| --- | --- | --- | --- | --- | --- |
| same-az | MinIO | 73 | 3 | 2,549 | 2,626 |
| cross-az | MinIO | 62 | 5 | 4,085 | 4,151 |
| eu-metro | MinIO | 59 | 22 | 13,386 | 13,466 |
| continent | MinIO | 123 | 131 | 68,852 | 69,106 |
| intercontinental | MinIO | 227 | 302 | 153,820 | 154,349 |

Garage is within 10 % of these totals.

### Round trips of each flow

The limit sweep adds delay to one path at a time, on MinIO.
The slope of each flow gives its round trips on that path.
The data set of the sweep was 3 workspaces and 3 stacks.

| Flow | S3 round trips | Database round trips |
| --- | --- | --- |
| OpenTofu state upload | 2 | 50 |
| OpenTofu lock, upload, unlock | 2 | 110 |
| Pulumi checkpoint save | 2 | 13 |
| Pulumi update cycle | 3 | 52 |
| Backup of 3 workspaces and 3 stacks | 95 | 390 |
| Restore of that backup | 434 | 1,960 |

| Step | OpenTofu state upload | Pulumi checkpoint save | Update cycle | Backup | Restore |
| --- | --- | --- | --- | --- | --- |
| S3 0 ms | 39 / 39 | 22 / 22 | 49 / 53 | 1,431 | 3,478 |
| S3 100 ms | 257 / 264 | 231 / 231 | 365 / 376 | 11,472 | 47,651 |
| S3 500 ms | 1,051 / 1,057 | 1,026 / 1,039 | 1,570 / 1,572 | 49,588 | 220,855 |
| S3 1,000 ms | 2,056 / 2,069 | 2,030 / 2,034 | 3,062 / 3,072 | 96,836 | 437,169 |
| S3 2,000 ms | 4,057 / 4,070 | 4,034 / 4,037 | 6,063 / 6,080 | 191,779 | 869,260 |
| S3 5,000 ms | stopped by the 20-minute bound | stopped | stopped | 476,804 | stopped |
| Database 0 ms | 47 / 56 | 28 / 37 | 64 / 78 | 1,435 | 3,466 |
| Database 10 ms | 550 / 759 | 175 / 233 | 611 / 782 | 5,949 | 25,570 |
| Database 50 ms | 2,460 / 3,382 | 738 / 1,076 | 2,639 / 3,339 | 21,327 | 106,408 |
| Database 100 ms | 5,004 / 5,355 | 1,344 / 1,351 | 5,250 / 5,251 | 40,783 | 199,794 |
| Database 250 ms | 13,456 / 16,705 | 4,059 / 5,070 | 15,453 / 15,939 | 98,365 | 517,481 |
| Database 500 ms | stopped by the 20-minute bound | stopped | stopped | 204,368 | stopped |

Each step of the sweep has a bound of 20 minutes.
At S3 5,000 ms and at database 500 ms, the seed and the backup used most of that time.
So the bound stopped the restore and the timed flows.
No timeout of the server, of the S3 adapter or of the durable operations failed a request at any step.

## Limits found

- No step of the sweep made a request fail. Every flow finished up to an S3 round trip of 2,000 ms and a database round trip of 250 ms. The backup and the restore succeeded at these steps.
- The database round trip sets the time of each CLI flow. A state upload costs about 50 database round trips and 2 S3 round trips.
- The Pulumi CLI of today saves a checkpoint for each step of an update, one after the other. Each save costs about 13 database round trips and 2 S3 round trips. At a database round trip of 10 ms, 200 saves add about 36 seconds to an update. At 50 ms, they add about 150 seconds.
- The restore is the slowest flow. It costs about 1,960 database round trips and 434 S3 round trips for 3 workspaces and 3 stacks, so its time grows with the data. At a database round trip of 250 ms, it took 517 seconds for that small data set.
- The orphan sweeps run inside the request: in the update start, in the checkpoint save and in the OpenTofu state declare. Each sweep sends one LIST, and one delete for each orphan past the grace period, one after the other. 1,000 old orphans add 154 seconds to one request at 150 ms.
- The source code of each CLI gives these client limits. The OpenTofu service discovery has a timeout of 10 seconds. The Pulumi CLI renews its update lease of 5 minutes when 2.5 minutes remain. A renewal that takes more than about 112 seconds can let the lease expire. No measured flow came near these limits. The OpenTofu `cloud` backend, the `http` backend and the Pulumi CLI set no timeout on a slow answer.
- A reverse proxy in front of the server can have its own timeout, for example 60 seconds. A request takes more than 60 seconds when its round trips times the latency pass that value. An example is a state upload at a database round trip of about 1.2 seconds.

## Limits of the method

- Toxiproxy ends the TCP connection on each side, so it adds delay but no TCP window limit and no bandwidth limit. A real 100 MiB transfer over 150 ms is slower than this run shows. The 100 MiB rows compare the two designs. They do not predict real transfer times.
- The stores, the database, the proxy and the benchmark share one host. A busy CPU adds noise to every row alike. Compare columns in one row, not runs of different hosts.
- Toxiproxy cannot express a delay below 1 ms. The `same-az` profile measures the loopback path through the proxy.
- p99 of fewer than 100 samples is one of the slowest samples. The tables give the sample count.
- MinIO and Garage run as one node each. A multi-node store adds its own internal round trips.

## Follow-up: network partitions

This benchmark does not test partitions.
A partition test needs these parts:

- A Toxiproxy `timeout` or `reset_peer` toxic for a set time during each flow. Apply it on the S3 path, on the database path, and on both.
- Partitions at each step of a write: the PUT, the fenced transaction, the commit, and the sweep.
- Two or more server instances on one database, so that one instance loses S3 while the other keeps it.
- An oracle for each flow after the partition heals. Each unit reads back the last committed state. No row names a missing blob. The sweep deletes each orphan after the grace period.
- Checks of the durable operations. A backup or a restore that loses the database for longer than the lease (30 seconds) must end as failed, with an audit record. Another instance must be able to take it over.
- A store that answers 503 or `SlowDown` during the partition. This tests the retry bound of the S3 adapter (5 attempts).
- Runs of the real CLIs through the e2e suite, because the retry rules of each CLI decide what a person sees. The OpenTofu `cloud` backend retries a 5xx 30 times and the `http` backend 2 times. The Pulumi CLI does not retry `CreateUpdate` and `StartUpdate`.
