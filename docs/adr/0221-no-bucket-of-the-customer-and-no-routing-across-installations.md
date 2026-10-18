# 221. No bucket of the customer, and no routing or reads across installations (owner)

Status: accepted · 2026-10-18 · closes the bucket question of RFC 32, and RFC 37 for installations (decision 5)

## Context

RFC 32 asks: "Is a storage location per namespace needed (Scalr lets
customers bring their own bucket)?" ADR 31 left a bucket for each namespace
open.

RFC 37 designs a placement map, a router that forwards a request to the
installation that holds its namespace, and a read view over many
installations. The owner said on 2026-10-12: "We can proxy traffic between
instances."

The project owner decided on 2026-10-18, as the project manager relayed:

1. No bucket that the customer brings: it is too complex now. The advice
   for data residency is to self-host.
2. No routing and no reads across installations.

## Decision

1. **No bucket of the customer (owner).** OpenDustpan Cloud stores the blobs
   of each account in the object storage of its shard, under the prefix of
   ADR 220 decision 5. An account cannot name its own bucket. The blob port
   keeps one store for each installation. RFC 32 asks whether a storage
   location for each namespace is needed: no.
2. **Data residency: self-host (owner).** A customer that must keep its
   data in a place that it controls runs its own installation. The docs of
   OpenDustpan Cloud say this, and link the reference architectures of RFC
   68. A customer key (ADR 217) keeps control of the keys, not of the place
   of the data.
3. **No routing across installations (owner).** No server forwards,
   redirects or proxies a request to another installation. A request
   reaches its installation through its host name and DNS (ADR 215
   decision 9). A namespace that is not on the installation answers as an
   unknown namespace.
4. **No reads across installations (owner).** No API answer merges data of
   more than one installation. `/api/user/stacks` and the reads with
   `namespaces/-` (ADR 210 decision 4) answer the account of the request on
   its installation only.
5. **What stays open in RFC 37.** Regions in one installation (ADR 4), a
   request to a region that is not the home region, replication, and the
   move of a namespace. These stay in RFC 37. The operator's move of an
   account between shards goes to RFC 76.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- AWS SaaS Lens, the silo model: a tenant that needs its own place for its
  data gets its own stack. For decision 2, that stack is a self-hosted
  installation.

## Consequences

- No placement map, no `pkg/placement` and no forwarding middleware for
  installations. The `Retry-After` and 421 rules of RFC 37 apply only to
  regions.
- RFC 32 and RFC 37 get outcome lines.
- The docs of OpenDustpan Cloud get a section on data residency.
