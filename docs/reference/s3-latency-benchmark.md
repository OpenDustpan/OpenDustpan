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
It writes throwaway credentials to a temporary directory with mode 0700 and removes the directory, the containers and the network at the end.
The results go to `bench-s3-out/`: `results.json`, `results.md`, `images.txt`, and one report and one log of the load harness for each step in `load/`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `BENCH_S3_ARGS` | empty | Extra flags of `cmd/s3bench`, for example `-quick`, `-profiles eu-metro` or `-limits` |
| `BENCH_S3_OUT` | `bench-s3-out` | Output directory |
| `BENCH_S3_NAME` | `odp-bench-s3` | Name prefix of the containers and the network |
| `BENCH_S3_PORT` | `18474` | First host port. The run uses it and the next 3 ports |

A full run with `-limits` takes about 5 hours on a host with 4 CPUs.
A run with `-quick` takes less than 1 hour.

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
| Blob PUT | 10 KiB, 1 MiB and 100 MiB. Three modes take turns, one write each: the `blob/s3` adapter `Put` (it stages the body in a file, hashes it and sends `If-None-Match: *`), one signed raw PUT with the header, and one signed raw PUT without the header under a unique key | Adapter: code. Raw plain PUT: model of the new write |
| Modeled sequences | The requests of each design in their order, with PostgreSQL model tables: a fence row and a row that names each blob key. A fenced transaction is BEGIN, a `SELECT ... FOR SHARE` of the fence row, an INSERT and COMMIT: 4 round trips | Model |
| Orphan sweep | 1,000 objects under one unit directory. The timed part is one LIST, one read of the keep set, and one DELETE for each orphan, one after the other, as the stack and workspace services sweep | Adapter calls: code. Keep-set read: model |
| CLI flows | `go run ./load -flows blobs` on S3 and PostgreSQL through the proxies: the OpenTofu state upload and the Pulumi checkpoint save, and a backup and a restore of a fixed data set through the server API. MinIO opens with the conditional-write probe, as the server binary does. Garage opens without it, because the server binary refuses a store that ignores `If-None-Match` | Code |
| Limit sweep (`-limits`) | The CLI flows on MinIO while the S3 round trip rises with the database at loopback, then while the database round trip rises with S3 at loopback | Code |

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

Shorter pairs outside Europe fit `eu-metro`: Ashburn to New York 6 ms, Tokyo to Osaka 10 to 12 ms, Virginia to Ohio 13 ms.
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

No source gave measured inter-region values for GCP, or measured p50 and p99 across AZs in AWS us-east-1 or eu-central-1, Azure West Europe or GCP europe-west.

## Limits of the method

- Toxiproxy ends the TCP connection on each side, so it adds delay but no TCP window limit and no bandwidth limit. A real 100 MiB transfer over 150 ms is slower than this run shows. The 100 MiB rows compare the two designs. They do not predict real transfer times.
- The stores, the database, the proxy and the benchmark share one host. A busy CPU adds noise to every row alike. Compare columns in one row, not runs of different hosts.
- Toxiproxy cannot express a delay below 1 ms. The `same-az` profile measures the loopback path through the proxy.
- p99 of fewer than 100 samples is one of the slowest samples. The tables give the sample count.
- MinIO and Garage run as one node each. A multi-node store adds its own internal round trips.

## Follow-up: network partitions

This benchmark does not test partitions.
A partition test needs these parts:

- A Toxiproxy `timeout` or `reset_peer` toxic on the S3 path, on the database path, and on both, for a set time during each flow: the PUT, the fenced transaction, the commit, and the sweep.
- Two or more server instances on one database, so that one instance loses S3 while the other keeps it.
- An oracle for each flow: after the partition heals, each unit reads back the last committed state, no row names a missing blob, and the sweep removes each orphan after the grace period.
- Checks of the durable operations: a backup or a restore that loses the database for longer than the lease (30 seconds) must end as failed, with an audit record, and another instance must be able to take it over.
- A store that answers 503 or `SlowDown` during the partition, to test the retry bound of the S3 adapter (5 attempts).
- Runs of the real CLIs through the e2e suite, because the CLI retry rules decide what a person sees: the OpenTofu `cloud` backend retries a 5xx 30 times, the `http` backend 2 times, and the Pulumi CLI does not retry `CreateUpdate` and `StartUpdate`.
