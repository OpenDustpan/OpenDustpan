// Command s3bench measures the blob writes of OpenDustpan on real
// S3-compatible stores behind Toxiproxy, for each network profile.
//
// It compares the two designs of a create-only blob write: a PUT with
// If-None-Match: * (the code today), and a plain PUT under a unique key with
// the database row that decides which write wins (the database-arbitrated
// design). It measures:
//
//   - blob PUT latency for 10 KiB, 1 MiB and 100 MiB, through the blob/s3
//     adapter and as raw signed requests with and without the header;
//   - modeled write sequences of both designs: the real requests in the order
//     each design sends them, against PostgreSQL model tables;
//   - an orphan sweep of 1,000 objects: one LIST, one read of the keep set,
//     and one DELETE for each orphan;
//   - the OpenTofu state upload, the Pulumi checkpoint save, a backup and a
//     restore through the load harness (go run ./load -flows blobs);
//   - with -limits, the same CLI flows while the S3 or the PostgreSQL round
//     trip rises step by step.
//
// Run it with make bench-s3: scripts/bench-s3.sh starts MinIO, Garage,
// PostgreSQL 18 and Toxiproxy in containers pinned by digest, writes
// throwaway credentials to a private directory, and runs cmd/s3bench.
// docs/reference/s3-latency-benchmark.md explains the profiles and the
// results.
package main
