---
rfc: 58
title: Bound memory of large request bodies
status: accepted
adr: 45
created: 2026-10-11
---

# RFC 58. Bound memory of large request bodies

Status: accepted as ADR 45.

## Problem

Checkpoint and import bodies allow 256 MiB each. The handler keeps the raw
body, a second copy after gzip decoding, and more copies through JSON decoding
and the store row. Secrets batches allow 200 MiB, about 267 MiB on the wire
before base64 decoding and encryption copies. A few concurrent requests can
use about 1 GB of memory each and exhaust the server.

## Options

1. Do nothing. Large stacks keep working, but concurrent large requests can
   exhaust memory.
2. Lower the defaults and stream gzip into the decoder, with a semaphore by
   bytes in flight. A full gate answers 503 with Retry-After. Operators raise
   the limits by flags for large stacks.
3. Store large bodies on disk before decoding. This bounds memory without
   lowering limits, but adds temporary storage and cleanup.

## Proposal

Take option 2. Lower the checkpoint default to 32 MiB and the secrets batch
default to 32 MiB, set by flags. Stream gzip decompression into the JSON
decoder so only one copy is held. Gate concurrent large bodies by bytes in
flight with a 503 and Retry-After when full. This changes the 256 MiB limit in
ADR 24 and the secrets limits in ADR 23, so it needs a new ADR on acceptance.

## Open questions

What measured checkpoint and batch sizes need higher limits in practice.

## Outcome

Accepted on 2026-10-12 ([ADR 45](../adr/0045-large-bodies-stream-to-blobs.md)).
Option 2 shipped in #241. ADR 45 records it and adds streaming to blob
storage for both tools, so the limits become a policy.
