# 221. No bucket of the customer (owner)

Status: accepted · 2026-10-18 · closes the bucket question of RFC 32

## Context

RFC 32 asks: "Is a storage location per namespace needed (Scalr lets
customers bring their own bucket)?" ADR 31 left a bucket for each namespace
open.

The project owner decided on 2026-10-18, as the project manager relayed: no
bucket that the customer brings, because it is too complex now. The advice
for data residency is to self-host.

An earlier draft of this ADR also decided "no routing and no reads across
installations". The owner reversed that part: routing between
installations is allowed as an opt-in capability. RFC 77 designs it. This
ADR keeps only the bucket decision.

## Decision

1. **No bucket of the customer (owner).** OpenDustpan Cloud stores the blobs
   of each namespace in its own object storage, under the prefix of ADR 220
   decision 7. A namespace cannot name its own bucket. The blob port keeps
   one store for each installation. RFC 32 asks whether a storage location
   for each namespace is needed: no.
2. **Data residency: self-host (owner).** A customer that must keep its
   data in a place that it controls runs its own installation. The docs of
   OpenDustpan Cloud say this, and link the reference architectures of RFC
   68. A customer key (ADR 217) gives control of the keys, not of the place
   of the data.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- AWS SaaS Lens, the silo model: a tenant that needs its own place for its
  data gets its own stack. For decision 2, that stack is a self-hosted
  installation.

## Consequences

- `pkg/blob` gets no store for each namespace.
- RFC 32 gets an outcome line.
- The docs of OpenDustpan Cloud get a section on data residency.
