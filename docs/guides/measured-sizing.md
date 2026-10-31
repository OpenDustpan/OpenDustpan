# Measured sizing of several instances

This page shows the numbers of one load run of one to three instances on one PostgreSQL database and one S3-compatible store.
Use it to compare the cost of the parts and to start a sizing.
It is not a promise.
OpenDustpan makes no service level agreement, and these numbers come from one host on one day.
Run the [load harness](load.md) on your own hardware before you size an installation.

## The machine and the setup

The run date is 2026-10-31.

| Part | Value |
| --- | --- |
| Host | One virtual machine with 4 CPUs (Intel Xeon, 2.80 GHz) and 16 GB of memory, Linux 6.18, amd64 |
| Server | `opendustpan serve` from `go build` of the repository at the time of the run (a dev build), plain HTTP on loopback, 1 to 3 processes |
| Database | PostgreSQL 18.6, one local server, `max_connections` 200, other settings at their defaults, data on the local disk |
| Object store | MinIO built from the Go module `github.com/minio/minio` (version `v0.0.0-20260212201848-7aac2a2c5b7c`), one process, data on the local disk |
| Balancer | The round-robin balancer of the harness, with a health check on `/health/ready` every 250 ms and no retries |
| Load | 32 simulated users, 500 seeded units, 10 s warm-up, 60 s of measurement |

Each user repeats one cycle:

1. Create a stack and run one Pulumi update on it: create, start, one checkpoint save of 16 KiB, complete.
2. Run one OpenTofu apply on its own workspace: lock, declare a state version, upload 8 KiB of state, unlock.
3. Read one page of 100 workspaces, and read the inventory.

One cycle is 11 requests.
The mix is not the mix of your teams.
The workspace page is the most costly request of the cycle, so a mix with fewer list reads costs less for each request.

## The limits of the measurement

- The harness, the balancer, the server processes, PostgreSQL and MinIO share 4 CPUs. The numbers show the shared host, not a host for each part.
- The measured CPU of all parts together was 2.4 to 3.5 cores of 4. The host had no free CPU for more load at three instances, so two and three instances reach the same throughput in the saturation run.
- The network is loopback. A real network adds time to each request and to each database round trip.
- One run for each setting. Nobody repeated the runs, so the numbers have an error that this page does not measure.
- The times run from the start of a request to the response headers.
- The CPU of PostgreSQL counts the postmaster and its live child processes. Child processes that ended in the window are not in the count.
- The load comes from clients that never retry a request. A real CLI retries a request that gets 503.
- The recovery figures of a database or an object store failure are not in this page. See [High availability](high-availability.md).

## Throughput at saturation

The users run with no wait between cycles. Each setting used a new database and a new bucket prefix.

| Instances | Requests each second | Pulumi updates each second | OpenTofu applies each second | Errors |
| --- | --- | --- | --- | --- |
| 1 | 316 | 28.8 | 28.7 | 0 of 18959 |
| 2 | 409 | 37.2 | 37.2 | 0 of 24535 |
| 3 | 409 | 37.3 | 37.2 | 0 of 24568 |

From one to two instances the throughput rose by about 30 percent.
From two to three instances it did not rise, because the host had no free CPU.
So the table shows the cost of the parts, not the limit of a host with more CPUs.

## Latency of the hot routes at saturation

Times use milliseconds: p50 / p95 / p99.

| Route | 1 instance | 2 instances | 3 instances |
| --- | --- | --- | --- |
| tofu lock | 92 / 132 / 147 | 51 / 97 / 122 | 53 / 128 / 160 |
| tofu state declare | 62 / 104 / 179 | 40 / 76 / 98 | 49 / 99 / 135 |
| tofu state upload | 59 / 98 / 135 | 37 / 71 / 89 | 47 / 94 / 120 |
| tofu unlock | 34 / 55 / 85 | 20 / 43 / 57 | 24 / 60 / 81 |
| pulumi update create | 13 / 24 / 32 | 10 / 22 / 32 | 13 / 37 / 56 |
| pulumi update start | 34 / 53 / 67 | 20 / 40 / 53 | 26 / 57 / 78 |
| pulumi checkpoint save | 45 / 66 / 82 | 32 / 56 / 70 | 43 / 85 / 108 |
| pulumi update complete | 32 / 47 / 54 | 18 / 38 / 52 | 23 / 57 / 77 |
| inventory list | 12 / 23 / 30 | 10 / 25 / 34 | 14 / 40 / 59 |
| tofu workspace list | 674 / 936 / 1083 | 584 / 995 / 1136 | 453 / 1089 / 1200 |

The route `tofu workspace list` reads 100 workspaces, so it is the slowest.
The write routes get faster when you add an instance, because each instance has 4 database connections by default and the requests queue for them.

## Latency at a fixed load

The users wait 1 second between cycles, so the offered load is the same for each setting and below the saturation of one instance.
The times use milliseconds: p50 / p95 / p99.

| Route | 1 instance | 2 instances | 3 instances |
| --- | --- | --- | --- |
| Requests each second | 175 | 229 | 271 |
| tofu lock | 98 / 180 / 208 | 44 / 131 / 177 | 19 / 71 / 107 |
| tofu state declare | 64 / 119 / 147 | 35 / 89 / 118 | 22 / 59 / 84 |
| tofu state upload | 66 / 117 / 143 | 36 / 83 / 102 | 23 / 62 / 84 |
| tofu unlock | 39 / 60 / 71 | 16 / 51 / 77 | 9 / 32 / 50 |
| pulumi update create | 15 / 28 / 35 | 8 / 28 / 40 | 5 / 20 / 33 |
| pulumi update start | 43 / 74 / 83 | 18 / 58 / 77 | 11 / 37 / 49 |
| pulumi checkpoint save | 69 / 104 / 119 | 35 / 84 / 121 | 24 / 61 / 89 |
| pulumi update complete | 41 / 75 / 88 | 17 / 52 / 72 | 9 / 35 / 52 |
| inventory list | 8 / 19 / 29 | 5 / 15 / 22 | 5 / 19 / 28 |
| tofu workspace list | 548 / 717 / 873 | 275 / 517 / 673 | 88 / 329 / 438 |

## What the parts used

The CPU is in cores, averaged over the 60 s window.
The cost is core-milliseconds for each request.

| Setting | Instances (cores) | PostgreSQL (cores) | MinIO (cores) | Harness (cores) | Connections (mean / max) | Transactions each second | Instance cost | PostgreSQL cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Saturation, 1 instance | 1.24 | 1.16 | 0.36 | 0.21 | 4 / 4 | 10608 | 3.9 | 3.7 |
| Saturation, 2 instances | 1.43 | 1.28 | 0.52 | 0.24 | 8 / 8 | 13721 | 3.5 | 3.1 |
| Saturation, 3 instances | 1.49 | 1.34 | 0.53 | 0.23 | 12 / 12 | 13802 | 3.6 | 3.3 |
| Fixed load, 1 instance | 0.75 | 0.68 | 0.20 | 0.13 | 4 / 4 | 5872 | 4.3 | 3.9 |
| Fixed load, 2 instances | 0.92 | 0.73 | 0.27 | 0.16 | 8 / 8 | 7738 | 4.0 | 3.2 |
| Fixed load, 3 instances | 1.17 | 0.91 | 0.36 | 0.21 | 12 / 12 | 9101 | 4.3 | 3.4 |

Each server process held 4 database connections.
The host has 4 CPUs, and the pool default of the driver is the larger of 4 and the CPU count.
The database URL can set `pool_max_conns`.
The table below shows saturation runs with `pool_max_conns=16`.

| Instances | Requests each second | Connections | `tofu lock` p50 / p95 / p99 (ms) | `tofu workspace list` p50 / p95 / p99 (ms) |
| --- | --- | --- | --- | --- |
| 1 | 357 | 16 | 70 / 108 / 126 | 609 / 730 / 780 |
| 2 | 386 | 32 | 76 / 149 / 186 | 394 / 707 / 844 |

A larger pool raised the throughput of one instance by about 13 percent.
It did not raise the throughput of two instances, because the host ran out of CPU.

## A rolling restart of one instance

After the 60 s window the harness stopped instance 0 with SIGTERM, started it again, and kept the load for 30 s.
The load was the fixed load of the table above.
The balancer takes an instance out of its pool when its readiness probe fails, as a load balancer does.
The restarted instance was ready in 0.1 to 0.2 s, because the binary and the schema were in place.
The stop took 10.0 s in each run, because the server drains for 10 s before it stops.

| Instances | Requests from SIGTERM to ready again | Errors | Error rate | Requests in the 30 s after | Errors after |
| --- | --- | --- | --- | --- | --- |
| 1 | 1221 | 1193 | 97.7 percent | 5730 | 0 |
| 2 | 2076 | 0 | 0.0 percent | 6548 | 0 |
| 3 | 2798 | 0 | 0.0 percent | 7681 | 0 |

With two or three instances, no request failed during the restart.
With one instance there is no other instance. The balancer answered 503 with `Retry-After: 1` from the first failed probe until the new process was ready.
In the run, 1193 of 1221 requests got 503 in 10.1 s.
These clients never retry.
A Pulumi or OpenTofu command retries a request that gets 503.
See [High availability](high-availability.md) for the retry times of each CLI version.

## A sizing table from these numbers

The table multiplies the measured cost of a request by a request rate.
It uses 4.0 core-milliseconds for the server processes and 3.5 for PostgreSQL, which are the middle of the fixed-load and saturation runs.
Your mix of requests will differ.
Size from your own run.

| Requests each second | Server cores (all instances) | PostgreSQL cores | Database connections with 2 instances | Database connections with 3 instances |
| --- | --- | --- | --- | --- |
| 50 | 0.2 | 0.2 | 8 | 12 |
| 100 | 0.4 | 0.3 | 8 | 12 |
| 200 | 0.8 | 0.7 | 8 | 12 |
| 400 | 1.6 | 1.4 | 8 | 12 |

Use these rules with the table:

- Run at least two instances for availability. The restart run shows that two instances lose no request when one restarts.
- Add instances for CPU and for database connections, not for the database. Each instance used about the same CPU for each request.
- PostgreSQL used about as much CPU as all server processes together. Give it as much CPU as the server processes.
- Check the database limit `max_connections`. Each instance holds 4 connections by default, or the value of `pool_max_conns`, and PostgreSQL needs some for other tools.
- MinIO used 0.2 to 0.5 cores in these runs.
- Leave free CPU on each host. The saturation runs had no free CPU left.

## Run it again

Use the scale mode of the [load harness](load.md#measure-several-instances) with your own hardware.
Run it for one, two and three instances with the same `-clients` and `-think`, and add `-restart` for the restart run.
